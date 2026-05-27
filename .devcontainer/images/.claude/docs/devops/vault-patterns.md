# Vault Patterns for Infrastructure

## Overview

HashiCorp Vault patterns for secrets management, PKI infrastructure, and Kubernetes integration. Based on production patterns from enterprise infrastructure deployments.

## PKI Infrastructure

### Multi-Tier PKI Architecture

Separate PKI backends for different trust domains:

```hcl
# =============================================================================
# VAULT PKI ENGINE - INTERNAL RESOURCES CERTIFICATE AUTHORITY
# =============================================================================
# Separate PKI backends for clear trust domain separation

# Internal Infrastructure PKI
resource "vault_mount" "pki" {
  path                      = "pki"
  type                      = "pki"
  description               = "PKI Backend for internal resources"
  max_lease_ttl_seconds     = 315360000  # 10 years
  default_lease_ttl_seconds = 7776000    # 90 days
}

# Kubernetes-specific PKI (separate trust domain)
resource "vault_mount" "pki_kubernetes" {
  path                      = "pki-kubernetes"
  type                      = "pki"
  description               = "PKI Backend for Kubernetes cluster"
  max_lease_ttl_seconds     = 315360000
  default_lease_ttl_seconds = 7776000
}
```

### PKI URL Configuration

```hcl
resource "vault_pki_secret_backend_config_urls" "pki_config" {
  backend = vault_mount.pki.path
  issuing_certificates = [
    "https://vault.internal/v1/pki/ca",
  ]
  crl_distribution_points = [
    "https://vault.internal/v1/pki/crl"
  ]
}
```

### Root Certificate Authority

```hcl
resource "vault_pki_secret_backend_root_cert" "internal_ca" {
  backend     = vault_mount.pki.path
  issuer_name = "internal-ca"
  type        = "internal"
  ttl         = "315360000"  # 10 years

  country      = "FR"
  organization = "MyOrg"
  ou           = "Infrastructure"
  common_name  = "Internal CA"
}
```

### Certificate Role Configuration

```hcl
resource "vault_pki_secret_backend_role" "internal_services" {
  backend      = vault_mount.pki.path
  issuer_ref   = vault_pki_secret_backend_root_cert.internal_ca.issuer_name
  name         = "internal-services"

  # Certificate details
  country      = ["FR"]
  organization = ["MyOrg"]
  ou           = ["Infrastructure"]

  # TTL configuration
  max_ttl   = "7776000"  # 90 days

  # Key configuration
  key_type  = "rsa"
  key_bits  = 2048
  key_usage = ["DigitalSignature", "NonRepudiation", "KeyEncipherment", "DataEncipherment"]
  ext_key_usage = ["ServerAuth"]

  # Domain configuration
  allowed_domains = ["internal", "svc.cluster.local"]
  allow_any_name              = false
  enforce_hostnames           = false
  allow_wildcard_certificates = true
  allow_bare_domains          = true
  allow_ip_sans               = false
  allow_localhost             = false
  allow_subdomains            = true

  basic_constraints_valid_for_non_ca = true
}
```

## Vault Secrets Operator (VSO) Integration

### Complete VSO Module

```hcl
# =============================================================================
# VAULT STATIC SECRET MANAGEMENT RESOURCES
# =============================================================================

# -----------------------------------------------------------------------------
# KUBERNETES SERVICE ACCOUNT
# -----------------------------------------------------------------------------
resource "kubernetes_service_account" "vault_operator" {
  metadata {
    name      = "vault-operator-secrets"
    namespace = var.namespace
  }
}

# -----------------------------------------------------------------------------
# VAULT POLICY
# -----------------------------------------------------------------------------
resource "vault_policy" "vault_operator" {
  name = "kubernetes-access-vault-secrets-${var.namespace}"

  policy = join("\n", [
    for secret in var.secrets: <<-EOP
    path "${secret.mount}/data/${secret.path}" {
      capabilities = ["read"]
    }
    path "${secret.mount}/metadata/${secret.path}" {
      capabilities = ["list"]
    }
    EOP
  ])
}

# -----------------------------------------------------------------------------
# VAULT AUTHENTICATION BACKEND ROLE
# -----------------------------------------------------------------------------
resource "vault_kubernetes_auth_backend_role" "vault_operator" {
  backend                          = var.auth_mount
  role_name                        = "vault-operator-${var.namespace}"
  bound_service_account_names      = [kubernetes_service_account.vault_operator.metadata[0].name]
  bound_service_account_namespaces = [var.namespace]
  token_ttl                        = 0
  token_period                     = 120
  token_policies                   = [vault_policy.vault_operator.name]
  audience                         = var.audience
}

# -----------------------------------------------------------------------------
# KUBERNETES RBAC
# -----------------------------------------------------------------------------
resource "kubernetes_cluster_role_binding" "vault_operator_auth_delegator" {
  metadata {
    name = "vault-operator-auth-delegator-${var.namespace}"
  }

  role_ref {
    api_group = "rbac.authorization.k8s.io"
    kind      = "ClusterRole"
    name      = "system:auth-delegator"
  }

  subject {
    kind      = "ServiceAccount"
    name      = kubernetes_service_account.vault_operator.metadata[0].name
    namespace = var.namespace
  }
}

# -----------------------------------------------------------------------------
# VAULT SECRETS OPERATOR - AUTHENTICATION
# -----------------------------------------------------------------------------
resource "kubernetes_manifest" "vault_auth" {
  manifest = {
    apiVersion = "secrets.hashicorp.com/v1beta1"
    kind       = "VaultAuth"
    metadata = {
      name      = "vault-auth-secrets-${var.namespace}"
      namespace = var.namespace
    }
    spec = {
      method     = "kubernetes"
      mount      = var.auth_mount
      kubernetes = {
        role           = vault_kubernetes_auth_backend_role.vault_operator.role_name
        serviceAccount = kubernetes_service_account.vault_operator.metadata[0].name
        audiences      = [var.audience]
      }
    }
  }
}

# -----------------------------------------------------------------------------
# VAULT SECRETS OPERATOR - STATIC SECRET RETRIEVAL
# -----------------------------------------------------------------------------
resource "kubernetes_manifest" "vault_static_secret" {
  for_each = { for s in var.secrets: s.name => s }

  manifest = {
    apiVersion = "secrets.hashicorp.com/v1beta1"
    kind       = "VaultStaticSecret"
    metadata = {
      name      = each.value.name
      namespace = var.namespace
    }
    spec = {
      vaultAuthRef = kubernetes_manifest.vault_auth.manifest.metadata.name
      mount        = each.value.mount
      path         = each.value.path
      type         = each.value.type
      version      = each.value.version
      refreshAfter = each.value.refreshAfter
      destination  = {
        create         = true
        name           = each.value.name
        labels         = each.value.labels
        type           = "Opaque"
        transformation = each.value.transformation
      }
    }
  }
}
```

### VSO Module Variables

```hcl
variable "namespace" {
  description = "Kubernetes namespace"
  type        = string
}

variable "auth_mount" {
  description = "Vault Kubernetes auth mount path"
  type        = string
  default     = "kubernetes"
}

variable "audience" {
  description = "Vault audience for Kubernetes auth"
  type        = string
  default     = "vault"
}

variable "secrets" {
  description = "List of secrets to retrieve from Vault"
  type = list(object({
    name           = string
    mount          = string
    path           = string
    type           = string
    version        = number
    refreshAfter   = string
    labels         = map(string)
    transformation = any
  }))
}
```

## Vault Agent with AppRole

### Agent Configuration Template (Jinja2)

```hcl
# Vault connection configuration
vault {
  address = "https://vault.internal"

  retry {
    num_retries = 5
  }
}

# Automatic authentication via AppRole
auto_auth {
  method "approle" {
    mount_path = "auth/approle"
    config = {
      role_id_file_path   = "/run/secrets/vault_role_id"
      secret_id_file_path = "/run/secrets/vault_secret_id"
      remove_secret_id_file_after_reading = false
    }
  }
}

# Prevent token leakage to disk
disable_mlock = true

{% for tpl in vault_templates %}
template {
  source      = "/vault/templates/{{ tpl }}.tpl"
  destination = "/certs/{{ tpl }}.bundle.pem"
  perms       = "0600"
  error_on_missing_key = true
}
{% endfor %}
```

### Vault Agent Podman Quadlet

```ini
[Unit]
Description=Vault Agent
After=network.target

[Container]
Image=docker.io/hashicorp/vault:latest
ContainerName=vault-agent

# Run as agent mode
Exec=vault agent -config=/vault/config/agent.hcl

# Network
Network=host

# Volumes
Volume=/etc/vault-agent/conf:/vault/config:ro
Volume=/etc/vault-agent/templates:/vault/templates:ro
Volume=/var/lib/vault-agent/certs:/certs:rw
Volume=/run/secrets:/run/secrets:ro

# Security
AddCapability=IPC_LOCK

[Service]
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

## Best Practices

### 1. Trust Domain Separation

- Separate PKI backends for different environments
- Internal infrastructure vs Kubernetes vs External services
- Clear naming conventions: `pki`, `pki-kubernetes`, `pki-external`

### 2. Certificate Lifecycle

| Certificate Type | TTL | Rotation |
|------------------|-----|----------|
| Root CA | 10 years | Manual |
| Intermediate CA | 5 years | Planned |
| Service certs | 90 days | Automatic |
| Short-lived tokens | 15 min | On-demand |

### 3. Audit and Compliance

```hcl
# Enable audit logging
resource "vault_audit" "file" {
  type = "file"
  path = "file"

  options = {
    file_path = "/vault/logs/audit.log"
  }
}
```

### 4. Key Security

- RSA-2048 for broad compatibility
- ECDSA P-256 for modern systems
- Always use `ServerAuth` extended key usage
- Avoid `allow_any_name = true` in production

## Related Patterns

- [Infrastructure as Code](./iac.md)
- [Immutable Infrastructure](./immutable-infrastructure.md)
- [GitOps](./gitops.md)
