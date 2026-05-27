<!-- updated: 2026-04-24T10:50:00Z -->
# DevContainer Hooks

## Purpose

Host-side initialization and optional project extensions.
All lifecycle hooks are now embedded in the Docker image at `/etc/devcontainer-hooks/`.

## Structure

```text
hooks/
├── lifecycle/          # Host-side only
│   └── initialize.sh   # .env setup + feature validation (runs on HOST before build)
├── shared/             # Shared utilities
│   ├── utils.sh        # Common functions (used by initialize.sh)
│   └── .env.example    # Environment variable template
└── project/            # Project-specific extensions (optional)
    └── .gitkeep
```

## Architecture

`devcontainer.json` lifecycle commands point directly to image-embedded hooks:
```json
"onCreateCommand": "/etc/devcontainer-hooks/lifecycle/onCreate.sh",
"postStartCommand": "/etc/devcontainer-hooks/lifecycle/postStart.sh"
```

Hooks auto-update when the Docker image is rebuilt. No workspace stubs needed.

**Exception:** `initialize.sh` runs on the host machine (before container build).

## postStart Services

| Service | Function | Description |
|---------|----------|-------------|
| Legacy cleanup | `step_cleanup_legacy_stubs` | Remove old workspace stubs |
| Shell env repair | `step_shell_env_repair` | v1→v3 upgrade, duplicate cleanup |
| Completion cache | `step_cache_completions` | Pre-generate `~/.zsh_completions/` |
| p10k segments | `step_generate_p10k_segments` | Dynamic `~/.p10k-segments.zsh` |
| Legacy cleanup | `cleanup_legacy_grepai` | One-shot kill of grepai/ollama leftovers (transitive after 2026-04 migration) |
| VPN | `init_vpn` | 1Password profile detection |
| Claude Code update | `step_update_claude_code` | Auto-update to latest version |
| RTK init | `init_rtk` | Token savings proxy initialization |

## Conventions

- All hook logic lives in `images/hooks/lifecycle/*.sh` (baked into image)
- `initialize.sh` is the only workspace-side hook (host machine)
- Use `run_step` pattern from `shared/utils.sh` in image hooks
