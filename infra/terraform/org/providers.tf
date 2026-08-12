# Default provider = the ORGANIZATION MANAGEMENT account. Run terraform with
# credentials for an admin role in that account.

provider "aws" {
  region = var.aws_region

  default_tags {
    tags = {
      Environment = var.environment
      Project     = var.project_name
      ManagedBy   = "Terraform"
    }
  }
}

# Member accounts are reached through the OrganizationAccountAccessRole that
# AWS Organizations creates automatically in every account it provisions.

provider "aws" {
  alias  = "audit"
  region = var.aws_region

  assume_role {
    role_arn = "arn:aws:iam::${aws_organizations_account.accounts["audit-account"].id}:role/OrganizationAccountAccessRole"
  }

  default_tags {
    tags = {
      Environment = var.environment
      Project     = var.project_name
      ManagedBy   = "Terraform"
    }
  }
}

provider "aws" {
  alias  = "security"
  region = var.aws_region

  assume_role {
    role_arn = "arn:aws:iam::${aws_organizations_account.accounts["security-account"].id}:role/OrganizationAccountAccessRole"
  }

  default_tags {
    tags = {
      Environment = var.environment
      Project     = var.project_name
      ManagedBy   = "Terraform"
    }
  }
}
