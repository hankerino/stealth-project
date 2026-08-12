# Partial backend configuration — backend blocks cannot interpolate variables.
# Inject the real values at init time (see README.md):
#
#   terraform init -backend-config=backend.hcl
#
# or individual flags:
#
#   terraform init \
#     -backend-config="bucket=<state-bucket>" \
#     -backend-config="key=org/terraform.tfstate" \
#     -backend-config="region=<region>" \
#     -backend-config="dynamodb_table=<lock-table>"

terraform {
  backend "s3" {
    encrypt = true
    # bucket         = injected at init
    # key            = injected at init
    # region         = injected at init
    # dynamodb_table = injected at init (state locking)
  }
}
