# Copyright (C) - {{ORGANIZATION}}
# Contact: {{CONTACT_EMAIL}}

# =============================================================================
# INPUT VARIABLES
# =============================================================================
# All configurable parameters for this module.

# -----------------------------------------------------------------------------
# REQUIRED VARIABLES
# -----------------------------------------------------------------------------

variable "name" {
  description = "Name of the resource"
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9-]+$", var.name))
    error_message = "Name must be lowercase alphanumeric with hyphens only."
  }
}

variable "namespace" {
  description = "Kubernetes namespace for deployment"
  type        = string
}

# -----------------------------------------------------------------------------
# OPTIONAL VARIABLES - GENERAL
# -----------------------------------------------------------------------------

variable "project" {
  description = "Project name for labeling"
  type        = string
  default     = "default"
}

variable "environment" {
  description = "Environment (production, staging, development)"
  type        = string
  default     = "production"

  validation {
    condition     = contains(["production", "staging", "development"], var.environment)
    error_message = "Environment must be production, staging, or development."
  }
}

# -----------------------------------------------------------------------------
# OPTIONAL VARIABLES - DEPLOYMENT
# -----------------------------------------------------------------------------

variable "replicas" {
  description = "Number of replicas"
  type        = number
  default     = 1

  validation {
    condition     = var.replicas >= 1
    error_message = "Replicas must be at least 1."
  }
}

variable "image" {
  description = "Container image"
  type        = string
  default     = null
}

variable "image_tag" {
  description = "Container image tag"
  type        = string
  default     = "latest"
}

# -----------------------------------------------------------------------------
# OPTIONAL VARIABLES - RESOURCES
# -----------------------------------------------------------------------------

variable "resources" {
  description = "Container resource limits and requests"
  type = object({
    limits = optional(object({
      cpu    = optional(string)
      memory = optional(string)
    }))
    requests = optional(object({
      cpu    = optional(string)
      memory = optional(string)
    }))
  })
  default = null
}
