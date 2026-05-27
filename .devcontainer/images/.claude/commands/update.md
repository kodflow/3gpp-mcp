---
name: update
description: |
  DevContainer Environment Update from official template.
  Profile-aware: auto-detects infrastructure projects and syncs from both templates.
  Uses git tarball (1 API call per source) instead of per-file curl.
  Use when: syncing local devcontainer with latest template improvements.
allowed-tools:
  - "Bash(curl:*)"
  - "Bash(git:*)"
  - "Bash(jq:*)"
  - "Read(**/*)"
  - "Write(.devcontainer/**/*)"
  - "Write(modules/**/*)"
  - "Write(stacks/**/*)"
  - "Write(ansible/**/*)"
  - "Write(packer/**/*)"
  - "Write(ci/**/*)"
  - "Write(tests/**/*)"
  - "WebFetch(*)"
  - "Glob(**/*)"
  - "Grep(**/*)"
  - "mcp__context7__*"
  - "Task(*)"
---

# Update - DevContainer Environment Update

$ARGUMENTS

## Description

Updates the DevContainer environment from the official template.

**GIT-TARBALL approach**: Downloads each template as a single tarball
(1 API call per source) instead of per-file curl. Extracts locally.

**Profile-aware**: Auto-detects infrastructure projects when `modules/`,
`stacks/`, or `ansible/` directories exist. No flags needed.

**Updated components (devcontainer - always):**

- **Hooks** - Claude scripts (format, lint, security, etc.)
- **Commands** - Slash commands (/git, /search, etc.)
- **Agents** - Agent definitions (specialists, executors)
- **Image-hooks** - Lifecycle hooks embedded in Docker image
- **Shared-utils** - Shared utilities (utils.sh)
- **Config** - p10k, settings.json
- **Compose** - docker-compose.yml (update devcontainer, preserve custom)
- **VSCode** - `.vscode/settings.json` (file nesting + editor defaults)

**Updated components (infrastructure - if profile detected):**

- **Modules** - Terraform modules (cloud, services, base)
- **Stacks** - Terragrunt stacks (management, edge, compute, vpn)
- **Ansible** - Roles and playbooks
- **Packer** - Machine images per provider
- **CI** - GitHub Actions + GitLab CI pipelines
- **Tests** - Terratest + Molecule tests

**Sources:**
- `github.com/kodflow/devcontainer-template` (always)
- `github.com/kodflow/infrastructure-template` (infrastructure profile only)

**OCI feature staleness handling (auto, no flags needed):**

`/update` now reconciles the GHCR publish state with the local pin, so a
single `/update` is enough whether you are working on the template repo or a
downstream project:

- **Template mode** (`origin` = `kodflow/devcontainer-template`): detects any
  feature whose `install.sh` or non-docs content changed since the last
  `devcontainer-feature.json` version bump, bumps it, opens a PR. The
  existing `publish-features.yml` then republishes fresh tags on merge.
- **Downstream mode**: force-refreshes GHCR manifests for every pinned
  `ghcr.io/kodflow/devcontainer-features/*` ref, prunes the BuildKit layer
  cache, wipes the devcontainer CLI feature cache, writes a
  `/tmp/claude-rebuild-request.json` CTA, and tells you to run
  **"Dev Containers: Rebuild Without Cache"** (plain Rebuild reuses the
  stale BuildKit layer).

Runs silently when everything is fresh. Background: `devcontainers/cli` skips
republish on an existing version string ([#814](https://github.com/devcontainers/cli/issues/814))
— forgetting to bump silently ships stale code.

---

## Arguments

| Pattern | Action |
|---------|--------|
| (none) | Full update |
| `--check` | Check for available updates |
| `--component <name>` | Update a specific component |
| `--help` | Show help |

### Available components

| Component | Path | Description |
|-----------|------|-------------|
| `hooks` | `.devcontainer/images/.claude/scripts/` | Claude scripts |
| `commands` | `.devcontainer/images/.claude/commands/` | Slash commands |
| `agents` | `.devcontainer/images/.claude/agents/` | Agent definitions |
| `lifecycle` | `.devcontainer/hooks/lifecycle/` | Lifecycle hooks (stubs) |
| `image-hooks` | `.devcontainer/images/hooks/` | Image-embedded lifecycle hooks |
| `shared-utils` | `.devcontainer/hooks/shared/utils.sh` | Shared hook utilities |
| `p10k` | `.devcontainer/images/.p10k.zsh` | Powerlevel10k config |
| `settings` | `.../images/.claude/settings.json` | Claude config |
| `compose` | `.devcontainer/docker-compose.yml` | Update devcontainer service |
| `mcp-template` | `.devcontainer/images/mcp.json.tpl` | MCP server template |
| `mcp-fragments` | `.devcontainer/images/mcp-fragments/` | MCP server fragments |
| `docs` | `.../images/.claude/docs/` | Design patterns KB (170+) |
| `templates` | `.../images/.claude/templates/` | Project/docs templates |
| `devcontainer` | `.devcontainer/devcontainer.json` | Feature refs (GHCR) |
| `dockerfile` | `.devcontainer/Dockerfile` | Image FROM reference |
| `vscode` | `.vscode/settings.json` | File nesting, editor defaults |

### Available components (infrastructure - auto-detected)

| Component | Path | Description |
|-----------|------|-------------|
| `modules` | `modules/` | Terraform modules (cloud, services, base) |
| `stacks` | `stacks/` | Terragrunt stacks |
| `ansible` | `ansible/` | Roles and playbooks |
| `packer` | `packer/` | Machine images per provider |
| `ci` | `ci/` | GitHub Actions + GitLab CI pipelines |
| `tests` | `tests/` | Terratest + Molecule tests |

---

## --help

```
═══════════════════════════════════════════════
  /update - DevContainer Environment Update
═══════════════════════════════════════════════

Usage: /update [options]

Options:
  (none)              Full update
  --check             Check for updates
  --component <name>  Update a component
  --help              Show this help

Components:
  hooks        Claude scripts (format, lint...)
  commands     Slash commands (/git, /search)
  agents       Agent definitions (specialists)
  image-hooks  Lifecycle hooks (image-embedded)
  shared-utils Shared hook utilities (utils.sh)
  p10k         Powerlevel10k config
  settings     Claude settings.json
  compose      docker-compose.yml (devcontainer service)
  mcp-template MCP server template (mcp.json.tpl)
  mcp-fragments MCP server fragments (context7, ktn-linter)
  docs         Design patterns knowledge base (170+)
  templates    Project/docs/terraform templates
  devcontainer devcontainer.json (feature refs)
  dockerfile   Dockerfile (image FROM)
  vscode       .vscode/settings.json (file nesting)

Infrastructure (auto-detected):
  modules      Terraform modules (cloud, services)
  stacks       Terragrunt stacks
  ansible      Roles and playbooks
  packer       Machine images per provider
  ci           CI/CD pipelines
  tests        Terratest + Molecule tests

Profile auto-detection:
  modules/ OR stacks/ OR ansible/ exists -> infrastructure
  Otherwise -> devcontainer (single source)

Method: git tarball (1 API call per source)

Examples:
  /update                       Update everything
  /update --check               Check for updates
  /update --component hooks     Hooks only

Sources:
  kodflow/devcontainer-template (main) - always
  kodflow/infrastructure-template (main) - if infra detected
═══════════════════════════════════════════════
```

---

## Overview

DevContainer environment update using **RLM** patterns:

- **Peek** - Verify connectivity and versions
- **Profile** - Auto-detect project profile (devcontainer or infrastructure)
- **Download** - Download tarballs (1 API call per source)
- **Extract** - Extract files to correct paths
- **Synthesize** - Apply updates and consolidated report

---

## Quick Reference (Phase Dispatch)

| Phase | Action | Module |
|-------|--------|--------|
| 1.0 | Environment detection (container vs host) | Read ~/.claude/commands/update/detect.md |
| 1.5 | Profile detection (devcontainer vs infrastructure) | Read ~/.claude/commands/update/detect.md |
| 2.0 | Version check (peek) | Read ~/.claude/commands/update/diff.md |
| 3.0 | Download tarballs (git tarball) | Read ~/.claude/commands/update/diff.md |
| 4.0 | Extract & apply components | Read ~/.claude/commands/update/apply.md |
| 5.0 | Orchestration & compose merge | Read ~/.claude/commands/update/apply.md |
| 6.0 | Hook synchronization | Read ~/.claude/commands/update/validate.md |
| 7.0 | Script validation | Read ~/.claude/commands/update/validate.md |

**IF `$ARGUMENTS` contains `--help`**: Display the help above and STOP.

**To execute a phase**, read the corresponding module file for full instructions.
