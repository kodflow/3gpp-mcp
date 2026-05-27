# Ansible Roles Structure Pattern

## Overview

A standardized Ansible role structure that emphasizes validation-first execution, clear task organization, and maintainable configurations. Based on enterprise infrastructure automation patterns.

## Directory Structure

```
ansible/
├── env/                           # Environment-specific variables
│   └── production/
│       ├── group_vars/
│       │   ├── all/
│       │   │   ├── config.yml
│       │   │   ├── vault.yml
│       │   │   └── version.yml
│       │   ├── k8s_controller/
│       │   │   ├── config.yml
│       │   │   └── vault.yml
│       │   └── k8s_worker/
│       │       └── config.yml
│       └── host_vars/
│           ├── node1.yml
│           └── node2.yml
├── playbook/
│   ├── .ansible.cfg
│   ├── play.yml                   # Main playbook
│   ├── play_controller.yml
│   └── play_worker.yml
├── roles/
│   └── <role_name>/
│       ├── defaults/main.yml      # Default variables
│       ├── files/main.yml         # Static files
│       ├── handlers/main.yml      # Event handlers
│       ├── meta/argument_specs.yml # Variable validation
│       ├── tasks/
│       │   ├── main.yml           # Entry point
│       │   ├── validate.yml       # ALWAYS FIRST
│       │   └── configure.yml      # Configuration tasks
│       ├── templates/             # Jinja2 templates
│       │   ├── conf/
│       │   └── containers/
│       └── vars/main.yml          # Internal variables
└── requirements.yml               # Role dependencies
```

## Role Template

### defaults/main.yml

```yaml
# Copyright (C) - Organization
# Contact: contact@organization.com

---
################################################################################
# Default Variables for role: <role_name>
################################################################################
# These values can be overridden in group_vars, host_vars, or playbook

# ==============================================================================
# Service Configuration
# ==============================================================================
service_name: "myservice"
service_enabled: true
service_port: 8080

# ==============================================================================
# Paths Configuration
# ==============================================================================
service_config_path: "/etc/{{ service_name }}"
service_data_path: "/var/lib/{{ service_name }}"
service_log_path: "/var/log/{{ service_name }}"

# ==============================================================================
# Security Configuration
# ==============================================================================
service_user: "{{ service_name }}"
service_group: "{{ service_name }}"
service_uid: 1000
service_gid: 1000
```

### meta/argument_specs.yml

```yaml
# Copyright (C) - Organization
# Contact: contact@organization.com

---
argument_specs:
  main:
    short_description: Configure <service_name>
    description:
      - This role configures and deploys <service_name>
      - It validates all inputs before making changes
    author:
      - Your Name <your.email@organization.com>
    options:
      service_name:
        type: str
        required: false
        default: "myservice"
        description: Name of the service

      service_enabled:
        type: bool
        required: false
        default: true
        description: Whether the service should be enabled

      service_port:
        type: int
        required: false
        default: 8080
        description: Port the service listens on
        choices:
          - range: [1, 65535]

      service_config_path:
        type: path
        required: false
        description: Path to service configuration directory
```

### tasks/main.yml

```yaml
# Copyright (C) - Organization
# Contact: contact@organization.com

---
################################################################################
# Main entrypoint for role: <role_name>
################################################################################
# Description:
#   This file orchestrates the execution of all subtasks in the role.
#   It validates input variables, then proceeds to configuration.
################################################################################

- name: Validate <role_name> variables constraints
  # ALWAYS validate before any action - fail fast principle
  ansible.builtin.include_tasks: validate.yml

- name: Configure <role_name>
  # Apply configuration only after validation passes
  ansible.builtin.include_tasks: configure.yml
```

### tasks/validate.yml (CRITICAL)

```yaml
# Copyright (C) - Organization
# Contact: contact@organization.com

---
################################################################################
# Validation tasks for role: <role_name>
################################################################################
# Description:
#   Validates all required variables and preconditions before any changes.
#   Fails fast if validation fails to prevent partial configurations.
################################################################################

# ==============================================================================
# Required Variables Check
# ==============================================================================
- name: Validate required variables are defined
  ansible.builtin.assert:
    that:
      - service_name is defined
      - service_name | length > 0
      - service_port is defined
      - service_port | int > 0
      - service_port | int < 65536
    fail_msg: "Required variables are missing or invalid"
    success_msg: "All required variables are defined"

# ==============================================================================
# Path Validation
# ==============================================================================
- name: Validate configuration paths
  ansible.builtin.assert:
    that:
      - service_config_path is defined
      - service_config_path | regex_search('^/')
    fail_msg: "Configuration path must be an absolute path"
    success_msg: "Configuration paths are valid"

# ==============================================================================
# Connectivity Check
# ==============================================================================
- name: Check if required ports are available
  ansible.builtin.wait_for:
    port: "{{ item }}"
    state: stopped
    timeout: 5
  loop:
    - "{{ service_port }}"
  ignore_errors: true
  register: port_check

- name: Fail if ports are already in use
  ansible.builtin.fail:
    msg: "Port {{ service_port }} is already in use"
  when: port_check.results | selectattr('failed', 'equalto', false) | list | length == 0

# ==============================================================================
# Dependency Check
# ==============================================================================
- name: Verify required packages are installed
  ansible.builtin.package:
    name: "{{ item }}"
    state: present
  loop:
    - python3
    - openssl
  check_mode: true
  register: package_check
  failed_when: false

- name: Report missing packages
  ansible.builtin.debug:
    msg: "Missing package: {{ item.item }}"
  loop: "{{ package_check.results }}"
  when: item.changed | default(false)
```

### tasks/configure.yml

```yaml
# Copyright (C) - Organization
# Contact: contact@organization.com

---
################################################################################
# Configuration tasks for role: <role_name>
################################################################################

# ==============================================================================
# Create directories
# ==============================================================================
- name: Create configuration directory
  ansible.builtin.file:
    path: "{{ service_config_path }}"
    state: directory
    owner: "{{ service_user }}"
    group: "{{ service_group }}"
    mode: '0750'

- name: Create data directory
  ansible.builtin.file:
    path: "{{ service_data_path }}"
    state: directory
    owner: "{{ service_user }}"
    group: "{{ service_group }}"
    mode: '0750'

# ==============================================================================
# Deploy configuration
# ==============================================================================
- name: Deploy service configuration
  ansible.builtin.template:
    src: "conf/{{ service_name }}.conf.j2"
    dest: "{{ service_config_path }}/{{ service_name }}.conf"
    owner: "{{ service_user }}"
    group: "{{ service_group }}"
    mode: '0640'
  notify:
    - Restart {{ service_name }}

# ==============================================================================
# Deploy systemd unit (Podman Quadlet)
# ==============================================================================
- name: Deploy Podman Quadlet unit
  ansible.builtin.template:
    src: "containers/{{ service_name }}.container"
    dest: "{{ quadlet_container_path }}/{{ service_name }}.container"
    mode: '0644'
  notify:
    - Reload systemd
    - Restart {{ service_name }}
```

### handlers/main.yml

```yaml
# Copyright (C) - Organization
# Contact: contact@organization.com

---
################################################################################
# Handlers for role: <role_name>
################################################################################

- name: Reload systemd
  ansible.builtin.systemd:
    daemon_reload: true

- name: Restart {{ service_name }}
  ansible.builtin.systemd:
    name: "{{ service_name }}"
    state: restarted
    enabled: true

- name: Reload {{ service_name }}
  ansible.builtin.systemd:
    name: "{{ service_name }}"
    state: reloaded
```

### templates/containers/service.container

```ini
# Copyright (C) - Organization
# Contact: contact@organization.com

# ==============================================================================
# Systemd unit file for running {{ service_name }} in a containerized environment
# ==============================================================================

[Unit]
Description={{ service_name }}
After=network.target

[Container]
Image={{ service_image }}:{{ service_version }}
ContainerName={{ service_name }}

# ==============================================================================
# Network Configuration
# ==============================================================================
Network=host
# Or for isolated networking:
# PublishPort={{ service_port }}:{{ service_port }}

# ==============================================================================
# Volumes
# ==============================================================================
Volume={{ service_config_path }}/:/etc/{{ service_name }}/:ro
Volume={{ service_data_path }}/:/var/lib/{{ service_name }}/:rw

# ==============================================================================
# Security
# ==============================================================================
{% if service_capabilities is defined %}
{% for cap in service_capabilities %}
AddCapability={{ cap }}
{% endfor %}
{% endif %}

# User mapping
User={{ service_uid }}
Group={{ service_gid }}

[Service]
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

## Group Variables Organization

### env/production/group_vars/all/config.yml

```yaml
# Copyright (C) - Organization
# Contact: contact@organization.com

---
################################################################################
# Global Configuration Variables
################################################################################

# ==============================================================================
# Environment
# ==============================================================================
environment: production
domain: internal

# ==============================================================================
# Paths
# ==============================================================================
quadlet_container_path: "/etc/containers/systemd"
```

### env/production/group_vars/all/vault.yml

```yaml
# Copyright (C) - Organization
# Contact: contact@organization.com

---
################################################################################
# Vault Configuration Variables
################################################################################

vault_addr: "https://vault.internal"
vault_agent_config_path: "/etc/vault-agent/conf"
vault_agent_templates_path: "/etc/vault-agent/templates"
vault_agent_certs_path: "/var/lib/vault-agent/certs/"
vault_agent_uid: 100
vault_agent_gid: 100
```

## Best Practices

### 1. Validation First Pattern

| Step | File | Purpose |
|------|------|---------|
| 1 | `validate.yml` | Check all preconditions |
| 2 | `configure.yml` | Apply configuration |
| 3 | `handlers/` | React to changes |

### 2. Idempotency

- All tasks should be idempotent
- Use `changed_when` and `failed_when` appropriately
- Test with `--check` mode

### 3. Error Handling

```yaml
- name: Task with error handling
  ansible.builtin.command: some_command
  register: result
  failed_when: false
  changed_when: result.rc == 0

- name: Handle failure
  ansible.builtin.fail:
    msg: "Command failed: {{ result.stderr }}"
  when: result.rc != 0 and not ignore_errors | default(false)
```

### 4. Documentation

- Every file starts with copyright and contact
- Section headers with `# ====` or `# ----`
- Clear variable descriptions in `argument_specs.yml`

## Related Patterns

- [Infrastructure as Code](./iac.md)
- [Immutable Infrastructure](./immutable-infrastructure.md)
- [Vault Patterns](./vault-patterns.md)
