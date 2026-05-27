# Terragrunt Patterns for Multi-Environment Infrastructure

## Overview

Terragrunt is a thin wrapper for Terraform that provides extra tools for working with multiple Terraform modules, keeping configurations DRY, and managing remote state. This document covers enterprise-grade patterns for multi-environment deployments.

## Directory Structure

### Recommended Layout

```
terraform/
├── _modules/                    # Reusable Terraform modules
│   ├── networking/
│   ├── kubernetes/
│   ├── vault/
│   └── openstack/
├── infrastructure/              # Physical/VM infrastructure
│   ├── terragrunt.hcl
│   ├── backend.tf
│   ├── provider.tf
│   └── variables.tfvars
├── k8s_driver/                  # Kubernetes base components
│   ├── terragrunt.hcl
│   ├── backend.tf
│   ├── provider.tf
│   └── helm_values/
├── k8s_system/                  # Kubernetes system services
│   ├── terragrunt.hcl
│   └── ...
├── vault/                       # Vault configuration
│   ├── terragrunt.hcl
│   └── ...
└── openstack/                   # OpenStack deployment
    ├── terragrunt.hcl
    └── ...
```

## Root Configuration

### Root terragrunt.hcl

```hcl
# Root terragrunt.hcl at terraform/terragrunt.hcl

# =============================================================================
# REMOTE STATE CONFIGURATION
# =============================================================================
remote_state {
  backend = "consul"

  generate = {
    path      = "backend_generated.tf"
    if_exists = "overwrite"
  }

  config = {
    address = "consul.internal:8500"
    scheme  = "https"
    path    = "terraform-state/${path_relative_to_include()}/terraform.tfstate"
    lock    = true

    # TLS Configuration
    ca_file   = "/etc/ssl/certs/ca.crt"
    cert_file = "/etc/ssl/certs/client.crt"
    key_file  = "/etc/ssl/private/client.key"
  }
}

# =============================================================================
# PROVIDER GENERATION
# =============================================================================
generate "provider" {
  path      = "provider_generated.tf"
  if_exists = "overwrite"
  contents  = <<EOF
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
    vault = {
      source  = "hashicorp/vault"
      version = ">= 3.20.0"
    }
  }
}
EOF
}

# =============================================================================
# COMMON INPUTS
# =============================================================================
inputs = {
  environment = "production"
  region      = "eu-west-1"

  common_tags = {
    ManagedBy   = "terraform"
    Environment = "production"
    Project     = "platform"
  }
}
```

## Component Configuration

### Infrastructure terragrunt.hcl

```hcl
# terraform/infrastructure/terragrunt.hcl

include "root" {
  path = find_in_parent_folders()
}

# =============================================================================
# DEPENDENCIES
# =============================================================================
# Infrastructure has no dependencies - it's the base layer

# =============================================================================
# TERRAFORM CONFIGURATION
# =============================================================================
terraform {
  source = "../_modules//infrastructure"

  extra_arguments "custom_vars" {
    commands = get_terraform_commands_that_need_vars()

    arguments = [
      "-var-file=${get_terragrunt_dir()}/variables.tfvars"
    ]
  }
}

# =============================================================================
# VAULT AUTHENTICATION
# =============================================================================
generate "vault_auth" {
  path      = "vault_auth_generated.tf"
  if_exists = "overwrite"
  contents  = <<EOF
provider "vault" {
  address = "https://vault.internal"

  auth_login {
    path = "auth/approle/login"

    parameters = {
      role_id   = file("/run/secrets/vault_role_id")
      secret_id = file("/run/secrets/vault_secret_id")
    }
  }
}
EOF
}
```

### Kubernetes Driver terragrunt.hcl

```hcl
# terraform/k8s_driver/terragrunt.hcl

include "root" {
  path = find_in_parent_folders()
}

# =============================================================================
# DEPENDENCIES
# =============================================================================
dependency "infrastructure" {
  config_path = "../infrastructure"

  mock_outputs = {
    kubernetes_endpoint = "https://kube.internal:6443"
    kubernetes_ca_cert  = "mock-ca-cert"
  }

  mock_outputs_allowed_terraform_commands = ["validate", "plan"]
}

# =============================================================================
# TERRAFORM CONFIGURATION
# =============================================================================
terraform {
  source = "../_modules//k8s_driver"

  extra_arguments "custom_vars" {
    commands = get_terraform_commands_that_need_vars()

    arguments = [
      "-var-file=${get_terragrunt_dir()}/variables.tfvars"
    ]
  }
}

# =============================================================================
# INPUTS FROM DEPENDENCIES
# =============================================================================
inputs = {
  kubernetes_endpoint = dependency.infrastructure.outputs.kubernetes_endpoint
  kubernetes_ca_cert  = dependency.infrastructure.outputs.kubernetes_ca_cert
}
```

### OpenStack terragrunt.hcl

```hcl
# terraform/openstack/terragrunt.hcl

include "root" {
  path = find_in_parent_folders()
}

# =============================================================================
# DEPENDENCIES
# =============================================================================
dependency "k8s_driver" {
  config_path = "../k8s_driver"

  mock_outputs = {
    cilium_ready = true
    istio_ready  = true
  }
}

dependency "vault" {
  config_path = "../vault"

  mock_outputs = {
    pki_mount_path = "pki"
    auth_mount     = "kubernetes"
  }
}

# =============================================================================
# TERRAFORM CONFIGURATION
# =============================================================================
terraform {
  source = "../_modules//openstack"
}

# =============================================================================
# INPUTS
# =============================================================================
inputs = {
  vault_pki_mount  = dependency.vault.outputs.pki_mount_path
  vault_auth_mount = dependency.vault.outputs.auth_mount
}
```

## Execution Patterns

### Apply All with Dependencies

```bash
# Apply all configurations respecting dependencies
cd terraform
terragrunt run-all apply

# Plan all
terragrunt run-all plan

# Destroy all (reverse order)
terragrunt run-all destroy
```

### Apply Specific Component

```bash
# Apply only k8s_driver
cd terraform/k8s_driver
terragrunt apply

# With auto-approve
terragrunt apply -auto-approve
```

### Parallel Execution

```bash
# Execute with parallelism
terragrunt run-all apply --terragrunt-parallelism 3
```

## Advanced Patterns

### Environment-Specific Variables

```hcl
# terraform/environments/production/terragrunt.hcl
include "root" {
  path = find_in_parent_folders("root.hcl")
}

locals {
  environment = "production"

  env_vars = {
    production = {
      replicas     = 3
      storage_size = "100Gi"
      node_count   = 5
    }
    staging = {
      replicas     = 1
      storage_size = "10Gi"
      node_count   = 2
    }
  }
}

inputs = local.env_vars[local.environment]
```

### Dynamic Provider Configuration

```hcl
generate "kubernetes_provider" {
  path      = "kubernetes_provider_generated.tf"
  if_exists = "overwrite"
  contents  = <<EOF
provider "kubernetes" {
  host                   = var.kubernetes_endpoint
  cluster_ca_certificate = base64decode(var.kubernetes_ca_cert)

  exec {
    api_version = "client.authentication.k8s.io/v1beta1"
    command     = "vault"
    args        = ["write", "-field=token", "auth/kubernetes/login", "role=terraform"]
  }
}
EOF
}
```

### Hooks for Validation

```hcl
terraform {
  before_hook "validate" {
    commands = ["apply", "plan"]
    execute  = ["tflint", "--config=${get_parent_terragrunt_dir()}/.tflint.hcl"]
  }

  before_hook "security" {
    commands = ["apply", "plan"]
    execute  = ["tfsec", "--soft-fail", "."]
  }

  after_hook "docs" {
    commands     = ["apply"]
    execute      = ["terraform-docs", "markdown", ".", "--output-file", "README.md"]
    run_on_error = false
  }
}
```

## Best Practices

### 1. State Management

| Backend | Use Case | Pros | Cons |
|---------|----------|------|------|
| Consul | On-premise | HA, locking | Requires cluster |
| S3 + DynamoDB | AWS | Scalable | AWS-only |
| GCS | GCP | Scalable | GCP-only |
| Azure Blob | Azure | Scalable | Azure-only |

### 2. Dependency Management

- Use `mock_outputs` for plan/validate without real dependencies
- Define clear dependency chains
- Avoid circular dependencies
- Use `skip = true` for optional dependencies

### 3. Security

- Never commit secrets
- Use Vault for all credentials
- Rotate service account credentials regularly
- Audit state access

### 4. CI/CD Integration

```yaml
# .gitlab-ci.yml example
stages:
  - validate
  - plan
  - apply

terragrunt:plan:
  stage: plan
  script:
    - cd terraform
    - terragrunt run-all plan -out=tfplan
  artifacts:
    paths:
      - terraform/**/tfplan

terragrunt:apply:
  stage: apply
  script:
    - cd terraform
    - terragrunt run-all apply tfplan
  when: manual
  only:
    - main
```

## Related Patterns

- [Infrastructure as Code](./iac.md)
- [GitOps](./gitops.md)
- [Immutable Infrastructure](./immutable-infrastructure.md)
- [Vault Patterns](./vault-patterns.md)
