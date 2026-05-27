---
name: infra
description: |
  Infrastructure automation with Terraform/Terragrunt.
  Dispatches to DevOps specialist agents for cloud-specific analysis.
allowed-tools:
  - "Read(**/*)"
  - "Glob(**/*)"
  - "mcp__context7__*"
  - "Grep(**/*)"
  - "Write(**/*)"
  - "Edit(**/*)"
  - "Bash(*)"
  - "Task(*)"
  - "AskUserQuestion(*)"
---

# /infra - Infrastructure Automation (Terraform/Terragrunt)

## CONTEXT7 (RECOMMENDED)

Use `mcp__context7__resolve-library-id` + `mcp__context7__query-docs` to:
- Fetch Terraform provider documentation for resource configuration
- Verify cloud service API references (AWS, GCP, Azure)
- Check HashiCorp tool documentation (Vault, Consul, Nomad)

---

## Overview

Automates Terraform and Terragrunt workflows with RLM patterns:

- **Peek** - Analyze infrastructure state before changes
- **Validate** - Run tflint, tfsec, and terraform validate
- **Plan** - Generate and review execution plan
- **Apply** - Apply changes with safety checks
- **Docs** - Generate documentation with terraform-docs

---

## Arguments

| Pattern | Action |
|---------|--------|
| `--init` | Initialize Terraform/Terragrunt |
| `--plan` | Generate execution plan |
| `--apply` | Apply changes (requires plan) |
| `--destroy` | Destroy infrastructure (with confirmation) |
| `--validate` | Run all validations (tflint, tfsec, validate) |
| `--docs` | Generate documentation |
| `--help` | Show help |

### Options

| Option | Description |
|--------|-------------|
| `--module <path>` | Target specific module |
| `--all` | Run on all modules (Terragrunt run-all) |
| `--auto-approve` | Skip interactive approval |
| `--dry-run` | Show what would be done |

---

## --help

```
═══════════════════════════════════════════════════════════════
  /infra - Infrastructure Automation (Terraform/Terragrunt)
═══════════════════════════════════════════════════════════════

Usage: /infra <action> [options]

Actions:
  --init          Initialize providers and modules
  --plan          Generate and review execution plan
  --apply         Apply infrastructure changes
  --destroy       Destroy infrastructure (with confirmation)
  --validate      Run validation suite (tflint, tfsec, validate)
  --docs          Generate documentation with terraform-docs

Options:
  --module <path> Target specific module directory
  --all           Apply to all modules (Terragrunt run-all)
  --auto-approve  Skip interactive approval (use with caution)
  --dry-run       Show what would be done without executing

RLM Patterns:
  1. Peek      - Check current state
  2. Validate  - Run all checks
  3. Plan      - Generate plan
  4. Apply     - Execute changes
  5. Docs      - Update documentation

Examples:
  /infra --validate                    # Validate all
  /infra --plan --module terraform/k8s # Plan specific module
  /infra --apply --all                 # Apply all with Terragrunt
  /infra --docs                        # Generate all docs

Safety:
  - Never auto-approve destroy
  - Always validate before apply
  - Review plan output before proceeding

═══════════════════════════════════════════════════════════════
```

---

## Module Reference

| Action | Module |
|--------|--------|
| Detection, plan, apply, docs, Terragrunt | Read ~/.claude/commands/infra/terraform.md |
| Agent dispatch & skill integration | Read ~/.claude/commands/infra/agents.md |
| Validation suite & safety guards | Read ~/.claude/commands/infra/validate.md |

---

## Execution Mode Detection (Agent Teams)

@.devcontainer/images/.claude/commands/shared/team-mode.md

Before Phase 1.5 (agent dispatch), determine runtime mode:

```bash
source "$HOME/.claude/scripts/team-mode-primitives.sh"
MODE=$(detect_runtime_mode)
```

Branch:
- `TEAMS_TMUX` / `TEAMS_INPROCESS` → **TEAMS cloud dispatch** (below)
- `SUBAGENTS` → legacy specialist dispatch in `infra/agents.md` (unchanged)

### TEAMS cloud dispatch

Lead: `devops-orchestrator`. Spawn cloud specialists only for clouds detected in the repo (via `.tf` provider scan). Up to 4 teammates:

```text
TaskCreate × N (where N ≤ 5, only for present clouds):
  cloud-aws         → using devops-specialist-aws         (if aws provider detected)
  cloud-gcp         → using devops-specialist-gcp         (if google provider detected)
  cloud-azure       → using devops-specialist-azure       (if azurerm provider detected)
  cloud-cloudflare  → using devops-specialist-cloudflare  # PR7 + PR2b — wrangler.toml or cloudflare provider
  cloud-hashicorp   → using devops-specialist-hashicorp   (if vault/consul/nomad detected)
```

### Cloud routing (PR7 — Skills Architecture v1.3)

```bash
# Cloud dispatch via route-agent.sh phase=apply, expand_from=.cloud[]
ROUTER=~/.claude/scripts/route-agent.sh
DISPATCH=$(bash "$ROUTER" --skill /infra --phase apply \
  --profile .claude/state/profile.json)
# Returns one dispatch per cloud in the .cloud[] facet.
```

Each task embeds a task-contract v1 block:
- `access_mode: "read-only"` for plan/validate operations (default)
- `access_mode: "write"` with explicit `owned_paths` for apply operations (user confirmation required)

Wait for all teammates (timeout: 300s per teammate, 600s total) → aggregate plan outputs → Phase 4.0 validation. Token ceiling ≤ 2x (cloud ops are IO-bound). If a teammate fails to report within timeout, proceed with partial results and log a warning.

---

## Routing

1. **Phase 1.0** Detection: Refer to `terraform.md` for tool/backend detection
2. **Phase 1.5** Agent Dispatch: Refer to `agents.md` for parallel specialist dispatch
3. **Phase 2.0** Secret Discovery: Refer to `terraform.md` for 1Password integration
4. **--validate**: Refer to `validate.md` for validation suite
5. **--plan / --apply / --docs**: Refer to `terraform.md` for workflows
6. **Safety guards**: Refer to `validate.md` for blocked commands
