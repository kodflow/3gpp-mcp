# Copyright (C) - {{ORGANIZATION}}
# Contact: {{CONTACT_EMAIL}}

# =============================================================================
# {{MODULE_NAME_UPPER}} - {{MODULE_DESCRIPTION}}
# =============================================================================
# {{MODULE_LONG_DESCRIPTION}}

# -----------------------------------------------------------------------------
# LOCALS
# -----------------------------------------------------------------------------
# Computed values and configuration defaults

locals {
  # Common labels for all resources
  common_labels = {
    "app.kubernetes.io/name"       = var.name
    "app.kubernetes.io/instance"   = "${var.name}-${var.namespace}"
    "app.kubernetes.io/managed-by" = "terraform"
    "app.kubernetes.io/part-of"    = var.project
  }
}

# -----------------------------------------------------------------------------
# MAIN RESOURCE
# -----------------------------------------------------------------------------
# {{RESOURCE_DESCRIPTION}}

# resource "{{RESOURCE_TYPE}}" "{{RESOURCE_NAME}}" {
#   # Configuration here
# }

# =============================================================================
# OPERATIONAL NOTES
# =============================================================================
#
# Architecture:
# - Describe the architecture
#
# Troubleshooting:
# - Common issues and solutions
#
