# Copyright (C) - {{ORGANIZATION}}
# Contact: {{CONTACT_EMAIL}}

# =============================================================================
# OUTPUT VALUES
# =============================================================================
# Values exported for use by other modules or root configurations.

# -----------------------------------------------------------------------------
# RESOURCE IDENTIFIERS
# -----------------------------------------------------------------------------

# output "id" {
#   description = "The unique identifier of the resource"
#   value       = {{RESOURCE_TYPE}}.{{RESOURCE_NAME}}.id
# }

# output "name" {
#   description = "The name of the resource"
#   value       = {{RESOURCE_TYPE}}.{{RESOURCE_NAME}}.metadata[0].name
# }

# -----------------------------------------------------------------------------
# CONNECTION INFORMATION
# -----------------------------------------------------------------------------

# output "endpoint" {
#   description = "The endpoint URL for the service"
#   value       = "https://${var.name}.${var.namespace}.svc.cluster.local"
# }

# -----------------------------------------------------------------------------
# SENSITIVE OUTPUTS
# -----------------------------------------------------------------------------

# output "credentials" {
#   description = "Service credentials"
#   value       = {{RESOURCE_TYPE}}.{{RESOURCE_NAME}}.credentials
#   sensitive   = true
# }
