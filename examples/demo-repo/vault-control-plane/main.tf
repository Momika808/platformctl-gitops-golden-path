# Minimal stand-in for the vault-control-plane repository.
# In the platform this file wires roles.d/*.yaml and policies/*.hcl into
# vault_kubernetes_auth_backend_role and vault_policy resources.
terraform {
  required_providers {
    vault = {
      source = "hashicorp/vault"
    }
  }
}
