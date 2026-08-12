# GitHub Actions self-hosted runner fleet.
#
# Flow: ASG launches into vpc-mgmt app subnets -> user-data installs Docker +
# the actions runner -> fetches the GitHub PAT from Secrets Manager -> swaps
# it for a short-lived registration token -> configures the runner as an
# ephemeral systemd service. Runners never hold long-lived GitHub credentials
# on disk beyond the runner's own OAuth identity.

# ------------------------------------------------------------- the secret
# Placeholder only — put the real PAT in out-of-band (README step). Terraform
# never sees the secret value.

resource "aws_secretsmanager_secret" "github_pat" {
  name        = "${var.environment}/ci/github-pat"
  description = "GitHub PAT (admin:org or repo scope) used by runners to fetch registration tokens"
}

resource "aws_secretsmanager_secret_version" "placeholder" {
  secret_id     = aws_secretsmanager_secret.github_pat.id
  secret_string = "REPLACE_ME"

  lifecycle {
    ignore_changes = [secret_string] # real value set via CLI/console
  }
}

# The CI role may read exactly this secret.
resource "aws_iam_role_policy" "ci_runner_secrets" {
  name = "${var.environment}-ci-runner-secrets"
  role = aws_iam_role.ci_runner.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid      = "ReadGithubPat"
      Effect   = "Allow"
      Action   = ["secretsmanager:GetSecretValue"]
      Resource = aws_secretsmanager_secret.github_pat.arn
    }]
  })
}

# ------------------------------------------------------------------- AMI ---

data "aws_ssm_parameter" "al2023_ami" {
  name = "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64"
}

# -------------------------------------------------------- launch template ---

locals {
  runner_scope_url = var.github_repo != "" ? "https://github.com/${var.github_owner}/${var.github_repo}" : "https://github.com/${var.github_owner}"
  runner_token_url = var.github_repo != "" ? "https://api.github.com/repos/${var.github_owner}/${var.github_repo}/actions/runners/registration-token" : "https://api.github.com/orgs/${var.github_owner}/actions/runners/registration-token"
  runner_labels    = var.github_repo != "" ? "self-hosted,linux,x64,${var.environment},repo-${var.github_repo}" : "self-hosted,linux,x64,${var.environment},org"

  runner_user_data = <<-EOT
    #!/bin/bash
    set -euxo pipefail

    dnf update -y
    dnf install -y docker jq
    systemctl enable --now docker

    RUNNER_HOME=/opt/actions-runner
    mkdir -p "$RUNNER_HOME"
    cd "$RUNNER_HOME"
    curl -fsSL -o runner.tar.gz "https://github.com/actions/runner/releases/download/v${var.runner_version}/actions-runner-linux-x64-${var.runner_version}.tar.gz"
    tar xzf runner.tar.gz
    ./bin/installdependencies.sh

    PAT=$(aws secretsmanager get-secret-value \
      --secret-id "${aws_secretsmanager_secret.github_pat.name}" \
      --region "${var.aws_region}" --query SecretString --output text)

    REG_TOKEN=$(curl -fsSL -X POST -H "Authorization: Bearer $PAT" \
      -H "Accept: application/vnd.github+json" "${local.runner_token_url}" | jq -r .token)

    useradd --system --home "$RUNNER_HOME" runner || true
    chown -R runner:runner "$RUNNER_HOME"
    usermod -aG docker runner

    # IMDSv2: token first, then the instance-id for the runner name.
    IMDS_TOKEN=$(curl -fsSL -X PUT -H 'X-aws-ec2-metadata-token-ttl-seconds: 300' http://169.254.169.254/latest/api/token || true)
    if [ -n "$IMDS_TOKEN" ]; then
      RUNNER_NAME=$(curl -fsSL -H "X-aws-ec2-metadata-token: $IMDS_TOKEN" http://169.254.169.254/latest/meta-data/instance-id)
    else
      RUNNER_NAME=$(hostname)
    fi

    sudo -u runner ./config.sh --unattended --ephemeral \
      --url "${local.runner_scope_url}" --token "$REG_TOKEN" \
      --name "$RUNNER_NAME" \
      --labels "${local.runner_labels}" --work _work

    ./svc.sh install runner
    ./svc.sh start
  EOT
}

resource "aws_launch_template" "ci_runner" {
  name_prefix = "${var.environment}-ci-runner-"

  image_id      = data.aws_ssm_parameter.al2023_ami.value
  instance_type = var.runner_instance_type

  iam_instance_profile {
    name = aws_iam_instance_profile.ci_runner.name
  }

  vpc_security_group_ids = [aws_security_group.ci_runner.id]

  user_data = base64encode(local.runner_user_data)

  metadata_options {
    http_tokens = "required" # IMDSv2
  }

  tag_specifications {
    resource_type = "instance"
    tags          = { Name = "${var.environment}-ci-runner" }
  }
}

# ------------------------------------------------------------------- ASG ---

resource "aws_autoscaling_group" "ci_runner" {
  name = "${var.environment}-ci-runner"

  min_size         = var.runner_asg.min
  desired_capacity = var.runner_asg.desired
  max_size         = var.runner_asg.max

  vpc_zone_identifier = local.mgmt_app_subnets
  health_check_type   = "EC2"

  launch_template {
    id      = aws_launch_template.ci_runner.id
    version = "$Latest"
  }

  # Roll instance replacement when the launch template changes (e.g. new
  # runner version).
  instance_refresh {
    strategy = "Rolling"
    preferences {
      min_healthy_percentage = 50
    }
  }

  tag {
    key                 = "Name"
    value               = "${var.environment}-ci-runner"
    propagate_at_launch = true
  }
}
