# Copyright (C) - {{ORGANIZATION}}
# Contact: {{CONTACT_EMAIL}}

# =============================================================================
# TERRAFORM AND PROVIDER REQUIREMENTS
# =============================================================================

terraform {
  required_version = ">= 1.5.0"

  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = ">= 2.23.0"
    }
    helm = {
      source  = "hashicorp/helm"
      version = ">= 2.11.0"
    }
    # Add other providers as needed
    # vault = {
    #   source  = "hashicorp/vault"
    #   version = ">= 3.20.0"
    # }
  }
}
