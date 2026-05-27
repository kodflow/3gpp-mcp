<!-- updated: 2026-04-18T00:00:00Z -->
# DevContainer Configuration

## Purpose

Development container setup for consistent dev environments across languages.

## Structure

```text
.devcontainer/
├── devcontainer.json         # Main config (synced from template on /update)
├── devcontainer.local.json   # Optional per-project overrides (preserved across /update)
├── docker-compose.yml        # Multi-service setup
├── Dockerfile                # Extends images/ base
├── install.sh                # Standalone Claude installer
├── scripts/                  # Build utilities
│   └── generate-assets-archive.sh
├── features/            # Language & tool features
│   ├── languages/       # 25 languages + shared/
│   ├── architectures/   # 14 architecture patterns
│   ├── browser/         # Playwright browser feature
│   ├── claude/          # Standalone Claude feature
│   ├── infrastructure/  # Terragrunt, TFLint, Infracost
│   └── kubernetes/      # Local K8s via kind
├── hooks/               # Host-side only (initialize.sh) + project extensions
├── tests/               # Unit tests (BATS)
└── images/              # Two-tier Docker images + Claude config
    ├── Dockerfile.base  # Stable layer (apt, Cloud CLIs) — weekly
    └── Dockerfile       # Dynamic layer (Claude, tools) — daily
```

## Key Files

- `devcontainer.json`: VS Code devcontainer config
- `docker-compose.yml`: Services (app, MCP servers)
- `.env`: Environment variables (git-ignored)
- `scripts/generate-assets-archive.sh`: Generates
  `claude-assets.tar.gz` (published via GitHub Releases)
- `scripts/list-team-agents.sh`: Deterministic extraction
  of agents referenced by team-migrated skills
- `install.sh`: Installs Claude CLI + tmux + detects
  Agent Teams capability. `--no-teams` to force-disable.

## Usage

Features are enabled in `devcontainer.json` under `features`.
Language conventions are enforced by specialist agents (e.g., `developer-specialist-go`).

## Local Overrides (`devcontainer.local.json`)

`/update` re-syncs `devcontainer.json` from the template on every run. To persist
per-project adjustments (enabled features, extra extensions, custom env vars),
create `.devcontainer/devcontainer.local.json` — a strict JSON file with only the
keys you want to override or add. The update flow deep-merges template + override
(override wins) and writes the result to `devcontainer.json`.

- No override file → template copied verbatim (JSONC comments preserved)
- Override file present → merged JSON written (comments stripped)
- Arrays are replaced wholesale (not concatenated) — copy template defaults if you need them
- Commit `devcontainer.local.json` so teammates get the same toolchain

Example (`.devcontainer/devcontainer.local.json`):

```json
{
  "features": {
    "ghcr.io/kodflow/devcontainer-features/go:1": {},
    "ghcr.io/kodflow/devcontainer-features/python:1": { "version": "3.12" }
  },
  "customizations": {
    "vscode": { "extensions": ["golang.go", "ms-python.python"] }
  }
}
```

Merge logic lives in `images/scripts/merge-devcontainer-json.mjs`, wired from
`images/.claude/commands/update/apply.md` (`update_devcontainer_json_from_tarball`).

## Features sync semantics (postStart)

`step_sync_features` (helper: `images/hooks/shared/sync-features.sh`) performs a
per-file 3-way merge from the image into `.devcontainer/features/`. Five outcomes:

- **noop** — byte-identical;
- **copied** — new file from upstream;
- **fast-forwarded** — workspace lagged behind image build commit, no manual edit
  (matched a `previous_hashes` entry from manifest v2); silent overwrite;
- **preserved** — dirty or true committed fork; emits `[WARNING]` pointing at
  `git diff -- .devcontainer/features/<path>` for inspection;
- **removed** — upstream deleted + dst still matches previous manifest entry.

Template repo (origin matches `kodflow/devcontainer-template`) self-excludes — its
workspace is the canonical source. The legacy `.template-version` marker-file path
stays as a secondary opt-in.

## .env propagation into git config

`postCreate.sh::step_git_identity` reconciles `/workspace/.env` with `git config --global`:

| .env key | git config key | Notes |
|---|---|---|
| `GIT_USER` | `user.name` | `export GIT_USER=…` form supported |
| `GIT_EMAIL` | `user.email` | double-quoted values supported |
| `GPG_SIGNINGKEY` | `user.signingkey` | resolved by `step_gpg_signing` (mode 1, see §GPG signing) |

Per-repo overrides (`.git/config user.email`) still win — `--global` is the
fallback, not the override. Multi-repo developers keep per-project identities.

Supported `.env` value subset: `KEY="value"` or `export KEY="value"`. Multi-line
values, single quotes, and escaped chars are out of scope.
