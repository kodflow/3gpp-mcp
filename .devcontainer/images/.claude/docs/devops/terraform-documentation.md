# Terraform Documentation Patterns

## Overview

Structured documentation patterns for Terraform modules that ensure maintainability, clarity, and professional code quality. Includes terraform-docs automation and commenting conventions.

## File Structure Convention

```
terraform/_modules/<module_name>/
├── main.tf                 # Primary resources
├── r_<resource_type>.tf    # Resource-specific files
├── v_variables.tf          # Input variables
├── v_locals.tf             # Local values
├── outputs.tf              # Output values
├── versions.tf             # Provider requirements
├── config/                 # Configuration files
│   └── *.conf
├── source/                 # Scripts loaded into ConfigMaps
│   └── *.sh
├── docs/
│   └── _tfdocs/
│       ├── header.md       # terraform-docs header
│       └── footer.md       # terraform-docs footer
└── .terraform-docs.yml     # terraform-docs config
```

## Comment Structure Convention

### Main Section Headers

```hcl
# =============================================================================
# MAIN SECTION TITLE
# =============================================================================
# Description of the section, its purpose, and how it fits into the architecture.
# This section should explain the WHY, not just the WHAT.
```

### Sub-Section Headers

```hcl
# -----------------------------------------------------------------------------
# SUB-SECTION TITLE
# -----------------------------------------------------------------------------
# More specific explanation of this sub-section.
```

### Resource Documentation

```hcl
# =============================================================================
# KEYSTONE API DEPLOYMENT
# =============================================================================
# This resource provisions a Kubernetes Deployment that runs the OpenStack
# Keystone identity service API. It ensures high availability configuration,
# TLS termination, and Apache WSGI integration.

# -----------------------------------------------------------------------------
# METADATA AND LABELS
# -----------------------------------------------------------------------------
# Defines the Deployment name, namespace, and standardized Kubernetes labels.
# These labels are used for monitoring, logging, and lifecycle management.

resource "kubernetes_deployment" "keystone_api" {
  metadata {
    name      = "keystone-api"
    namespace = var.namespace

    # Standard Kubernetes labels for discovery and management
    labels = {
      "app.kubernetes.io/name"       = "keystone"
      "app.kubernetes.io/instance"   = "openstack-keystone"
      "app.kubernetes.io/component"  = "api"
      "app.kubernetes.io/managed-by" = "terraform"
      "app.kubernetes.io/part-of"    = "openstack"
    }

    # Auto-reload on ConfigMap changes
    annotations = {
      "reloader.stakater.com/auto" = "true"
    }
  }

  # ---------------------------------------------------------------------------
  # DEPLOYMENT EXECUTION CONFIGURATION
  # ---------------------------------------------------------------------------
  # Controls replica count, update strategy, and revision history.
  # The Deployment uses RollingUpdate strategy with surge and unavailability limits.

  spec {
    replicas                  = var.replicas
    progress_deadline_seconds = 600
    revision_history_limit    = 3

    # ...
  }
}
```

### Operational Notes Section

```hcl
# =============================================================================
# OPERATIONAL NOTES
# =============================================================================
#
# Architecture:
# - Single-tier CA hierarchy for internal infrastructure services
# - 10-year root certificate lifetime with 90-day leaf certificate rotation
# - RSA-2048 keys for broad compatibility across internal services
#
# Certificate Role Configuration:
# - Wildcard certificates enabled for flexible service naming
# - Server authentication focus for internal service communication
# - Subdomain support for hierarchical service organization
#
# Troubleshooting:
# - If certificates fail to issue, check Vault audit logs
# - Verify PKI role permissions match namespace requirements
# - Check CRL distribution point accessibility
#
```

## Variables Documentation

### v_variables.tf

```hcl
# Copyright (C) - Organization
# Contact: contact@organization.com

# =============================================================================
# INPUT VARIABLES
# =============================================================================
# All configurable parameters for this module.
# Variables are grouped by function for clarity.

# -----------------------------------------------------------------------------
# REQUIRED VARIABLES
# -----------------------------------------------------------------------------

variable "namespace" {
  description = "Kubernetes namespace for deployment"
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9-]+$", var.namespace))
    error_message = "Namespace must be lowercase alphanumeric with hyphens only."
  }
}

variable "service_name" {
  description = "Name of the service being deployed"
  type        = string
}

# -----------------------------------------------------------------------------
# OPTIONAL VARIABLES - DEPLOYMENT
# -----------------------------------------------------------------------------

variable "replicas" {
  description = "Number of pod replicas"
  type        = number
  default     = 1

  validation {
    condition     = var.replicas >= 1 && var.replicas <= 10
    error_message = "Replicas must be between 1 and 10."
  }
}

variable "image_tag" {
  description = "Container image tag"
  type        = string
  default     = "latest"
}

# -----------------------------------------------------------------------------
# OPTIONAL VARIABLES - SECURITY
# -----------------------------------------------------------------------------

variable "service_account_name" {
  description = "Kubernetes service account name"
  type        = string
  default     = null
}

variable "security_context" {
  description = "Pod security context configuration"
  type = object({
    run_as_user     = optional(number)
    run_as_group    = optional(number)
    fs_group        = optional(number)
    run_as_non_root = optional(bool)
  })
  default = null
}
```

### v_locals.tf

```hcl
# Copyright (C) - Organization
# Contact: contact@organization.com

# =============================================================================
# LOCAL VALUES
# =============================================================================
# Computed values and configuration defaults.
# Locals reduce repetition and centralize logic.

locals {
  # ---------------------------------------------------------------------------
  # NAMING CONVENTION
  # ---------------------------------------------------------------------------
  full_name = "${var.service_name}-${var.namespace}"

  # ---------------------------------------------------------------------------
  # LABELS
  # ---------------------------------------------------------------------------
  common_labels = {
    "app.kubernetes.io/name"       = var.service_name
    "app.kubernetes.io/instance"   = local.full_name
    "app.kubernetes.io/managed-by" = "terraform"
    "app.kubernetes.io/part-of"    = var.project_name
  }

  # ---------------------------------------------------------------------------
  # CONFIGURATION
  # ---------------------------------------------------------------------------
  config_files = {
    for filename in fileset("${path.module}/config", "*") :
    filename => file("${path.module}/config/${filename}")
  }

  # ---------------------------------------------------------------------------
  # SCRIPTS
  # ---------------------------------------------------------------------------
  scripts = {
    for filename in fileset("${path.module}/source", "*.sh") :
    filename => file("${path.module}/source/${filename}")
  }
}
```

## terraform-docs Configuration

### .terraform-docs.yml

```yaml
formatter: "markdown table"
version: "0.20.0"

header-from: "./docs/_tfdocs/header.md"
footer-from: "./docs/_tfdocs/footer.md"

recursive:
  enabled: true
  path: ../_modules/
  include-main: true

sections:
  hide: []
  show:
    - header
    - inputs
    - modules
    - outputs
    - providers
    - requirements
    - footer

output:
  file: "README.md"
  mode: inject
  template: |-
    <!-- BEGIN_TF_DOCS -->
    {{ .Content }}
    <!-- END_TF_DOCS -->

output-values:
  enabled: false
  from: ""

sort:
  enabled: true
  by: required

content: |-
  {{ .Header }}

  ## Requirements

  {{ .Requirements }}

  ## Providers

  {{ .Providers }}

  ## Inputs

  {{ .Inputs }}

  ## Outputs

  {{ .Outputs }}

  {{ .Modules }}

  {{ .Footer }}

settings:
  anchor: true
  color: true
  default: true
  description: true
  escape: true
  hide-empty: true
  html: true
  indent: 2
  lockfile: true
  read-comments: true
  required: true
  sensitive: true
  type: true
```

### docs/_tfdocs/header.md

```markdown
# Module Name

[![Terraform](https://img.shields.io/badge/terraform->=1.5.0-blue.svg)](https://www.terraform.io/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

## Overview

Brief description of what this module does and its primary use cases.

## Features

- Feature 1
- Feature 2
- Feature 3

## Usage

\`\`\`hcl
module "example" {
  source = "../_modules/example"

  namespace    = "production"
  service_name = "my-service"
  replicas     = 3
}
\`\`\`

## Architecture

Describe the architecture and how components interact.
```

### docs/_tfdocs/footer.md

```markdown
## Related Modules

- [Module A](../module_a/)
- [Module B](../module_b/)

## Contributing

See [CONTRIBUTING.md](../../CONTRIBUTING.md) for guidelines.

## License

MIT License - see [LICENSE](../../LICENSE) for details.
```

## Automation

### Pre-commit Hook for terraform-docs

```yaml
# .pre-commit-config.yaml
repos:
  - repo: https://github.com/terraform-docs/terraform-docs
    rev: v0.20.0
    hooks:
      - id: terraform-docs-go
        args: ["--config", ".terraform-docs.yml"]
```

### Makefile Integration

```makefile
.PHONY: docs
docs:
	@find terraform/_modules -name ".terraform-docs.yml" -exec dirname {} \; | \
		xargs -I {} terraform-docs --config {}/.terraform-docs.yml {}

.PHONY: docs-check
docs-check:
	@find terraform/_modules -name ".terraform-docs.yml" -exec dirname {} \; | \
		xargs -I {} sh -c 'terraform-docs --config {}/.terraform-docs.yml {} --output-check'
```

## Best Practices

### 1. Comment Depth

| Level | Symbol | Use |
|-------|--------|-----|
| Main section | `# ===` | Major resource groups |
| Sub-section | `# ---` | Related resource configs |
| Inline | `#` | Individual attributes |

### 2. Documentation Completeness

- [ ] Every variable has a description
- [ ] Every output has a description
- [ ] Complex resources have section headers
- [ ] Operational notes for non-obvious configurations
- [ ] README auto-generated by terraform-docs

### 3. Naming Conventions

| Element | Convention | Example |
|---------|------------|---------|
| Resource files | `r_<type>.tf` | `r_deployment.tf` |
| Variable files | `v_*.tf` | `v_variables.tf`, `v_locals.tf` |
| Scripts directory | `source/` | `source/bootstrap.sh` |
| Config directory | `config/` | `config/app.conf` |

## Related Patterns

- [Terragrunt Patterns](./terragrunt-patterns.md)
- [Infrastructure as Code](./iac.md)
- [GitOps](./gitops.md)
