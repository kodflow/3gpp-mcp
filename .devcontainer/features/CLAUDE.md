<!-- updated: 2026-04-21T15:27:23Z -->
# DevContainer Features

## Purpose

Modular features for languages, tools, and architectures.

## Structure

```text
features/
├── languages/        # 25 languages + shared utility library
│   ├── shared/       # feature-utils.sh (colors, logging, arch, GitHub API)
│   └── <lang>/       # install.sh + devcontainer-feature.json
├── architectures/    # Architecture patterns (14 patterns)
├── browser/          # Playwright browser testing
├── claude/           # Claude Code standalone integration
├── infrastructure/   # Terragrunt, TFLint, Infracost, cfssl, Ansible tools
└── kubernetes/       # Local K8s via kind
```

## Key Components

- Each language has `install.sh` + `devcontainer-feature.json`
- All install.sh source `shared/feature-utils.sh` (with inline fallback)
- Downloads parallelized with `&` + `wait` for faster builds
- Conventions enforced by specialist agents (e.g., `developer-specialist-go`)

## MCP Integration

Features can contribute MCP server configs via fragment files:

1. Add `mcp.json` in the feature directory (e.g., `languages/go/mcp.json`)
2. Call `install_mcp_fragment "$FEATURE_DIR"` at end of `install.sh`
3. Fragment is copied to `/etc/mcp/features/<name>.mcp.json` at build time
4. `postStart.sh` merges fragments into `/workspace/mcp.json` at runtime
5. `requires_binary` field gates inclusion (skipped if binary not found)

Fragment format:
```json
{
  "servers": {
    "server-name": {
      "command": "binary",
      "args": ["arg1"],
      "env": {},
      "requires_binary": "binary"
    }
  }
}
```

## Adding a Language

1. Create `languages/<name>/`
2. Add `devcontainer-feature.json` for metadata
3. Add `install.sh` sourcing `shared/feature-utils.sh`
4. (Optional) Add `mcp.json` if the language provides an MCP server

## Failure Modes — Fail Loud, Not Silent

Features have ONE job: install the toolchain. A silently-half-installed
feature produces downstream breakage that is hard to diagnose (issue #324).

Rules for every `install.sh`:

1. **Strict mode** at top of script:
   ```bash
   set -Eeuo pipefail
   trap 'on_error $? $LINENO "$BASH_COMMAND"' ERR
   ```
2. **Per-PID tracking** for parallel installs — `wait <pid>` is the only
   reliable way to surface a subshell failure. A bare `wait` returns 0.
3. **Critical vs optional** — declare which tools are load-bearing
   (hooks / MCP depend on them) and `exit 1` when a critical one fails
   to install.
4. **Write MCP fragments early** — call `install_mcp_fragment` immediately
   after the trigger binary is verified, NOT at the end of the script.
   Otherwise any later failure silently drops the fragment.
5. **Structured step markers** (e.g. `[INSTALL-GO] step=X status=ok|fail`)
   so users can grep the devcontainer build log.
6. **Bump `version`** in `devcontainer-feature.json` on every non-docs change.
   `devcontainers/cli` skips republish when the version string already exists
   on GHCR ([devcontainers/cli#814](https://github.com/devcontainers/cli/issues/814)) —
   forgetting the bump silently ships stale code to every downstream consumer.
   Enforced by `.github/workflows/version-gate.yml`.

Reference implementation: `.devcontainer/features/languages/go/install.sh`.
Static regression guards: `tests/scripts/go-install.bats`.
