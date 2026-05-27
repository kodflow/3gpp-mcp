---
name: secret
description: |
  Secure secret management with 1Password CLI (op).
  Share secrets between projects via Vault-like path structure.
  Auto-detects project path from git remote origin.
  Use when: storing, retrieving, or listing project secrets.
allowed-tools:
  - "Bash(op:*)"
  - "Bash(git:*)"
  - "Read(**/*)"
  - "Glob(**/*)"
  - "Grep(**/*)"
  - "AskUserQuestion(*)"
---

# /secret - Secure Secret Management (1Password + Vault-like Paths)

$ARGUMENTS

## Overview

Secure secret management via **1Password CLI** (`op`) with a path hierarchy inspired by HashiCorp Vault:

- **Peek** - Verify 1Password connectivity + resolve project path
- **Execute** - Call `op` CLI for push/get/list
- **Synthesize** - Display formatted result

**Backend:** 1Password (via `OP_SERVICE_ACCOUNT_TOKEN`)
**CLI:** `op` (installed in the devcontainer)
**No MCP:** 1Password has no official MCP (deliberate policy)

---

## Arguments

| Pattern | Action |
|---------|--------|
| `--push <key>=<value>` | Write a secret to 1Password |
| `--get <key>` | Read a secret from 1Password |
| `--list` | List project secrets |
| `--path <path>` | Override the project path (optional) |
| `--help` | Show help |

### Examples

```bash
# Push a secret (auto path = kodflow/devcontainer-template)
/secret --push DB_PASSWORD=mypass

# Push to a different path (cross-project)
/secret --push SHARED_TOKEN=abc123 --path kodflow/shared-infra

# Get a secret
/secret --get DB_PASSWORD

# Get from another path
/secret --get API_KEY --path kodflow/other-project

# List secrets for the current project
/secret --list

# List secrets from another path
/secret --list --path kodflow/shared-infra
```

---

## --help

```
═══════════════════════════════════════════════════════════════
  /secret - Secure Secret Management (1Password)
═══════════════════════════════════════════════════════════════

Usage: /secret <action> [options]

Actions:
  --push <key>=<value>    Store a secret in 1Password
  --get <key>             Retrieve a secret from 1Password
  --list                  List secrets for current project

Options:
  --path <org/repo>       Override project path (default: auto)
  --help                  Show this help

Path Convention (Vault-like):
  Items are named: <org>/<repo>/<key>
  Default path is auto-detected from git remote origin.
  Example: kodflow/devcontainer-template/DB_PASSWORD

  Without --path: scoped to current project ONLY
  With --path: access any project's secrets

Backend:
  1Password CLI (op) with OP_SERVICE_ACCOUNT_TOKEN
  Items stored as API_CREDENTIAL in configured vault
  Field: "credential" (matches existing MCP token pattern)

Examples:
  /secret --push DB_PASSWORD=s3cret
  /secret --get DB_PASSWORD
  /secret --list
  /secret --push TOKEN=abc --path kodflow/shared
  /secret --get TOKEN --path kodflow/shared

═══════════════════════════════════════════════════════════════
```

---

## Module Reference

| Action | Module |
|--------|--------|
| Push, get, list operations | Read ~/.claude/commands/secret/operations.md |
| 1Password CLI, paths, peek | Read ~/.claude/commands/secret/integration.md |
| Cross-skill integration & guardrails | Read ~/.claude/commands/secret/workflow.md |

---

## Routing

1. **Always start** with Phase 1.0 Peek from `integration.md`
2. **--push**: Execute push workflow from `operations.md`
3. **--get**: Execute get workflow from `operations.md`
4. **--list**: Execute list workflow from `operations.md`
5. **Path conventions**: Refer to `integration.md`
6. **Cross-skill usage**: Refer to `workflow.md`
