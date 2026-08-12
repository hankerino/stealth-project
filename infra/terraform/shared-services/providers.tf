# Runs in the shared-services account via OrganizationAccountAccessRole.

provider "aws" {
  region = var.aws_region

  assume_role {
    role_arn = "arn:aws:iam::${var.shared_services_account_id}:role/OrganizationAccountAccessRole"
  }

  default_tags {
    tags = {
      Environment = var.environment
      Project     = var.project_name
      ManagedBy   = "Terraform"
    }
  }
}
