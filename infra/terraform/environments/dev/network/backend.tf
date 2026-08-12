# Partial backend config — same pattern as the org layer. Init with:
#   terraform init -backend-config=backend.hcl
terraform {
  backend "s3" {
    encrypt = true
    # bucket/key/region/dynamodb_table injected at init (see README)
  }
}
