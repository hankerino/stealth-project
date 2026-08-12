# Partial backend — init with: terraform init -backend-config=backend.hcl
terraform {
  backend "s3" {
    encrypt = true
  }
}
