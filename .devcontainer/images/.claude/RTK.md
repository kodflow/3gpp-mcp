# RTK - Rust Token Killer

**Usage**: Token-optimized CLI proxy (60-90% savings on dev operations)

## Meta Commands (always use rtk directly)

```bash
rtk gain              # Show token savings analytics
rtk gain --history    # Show command usage history with savings
rtk gain --graph      # ASCII graph (last 30 days)
rtk gain --daily      # Day-by-day breakdown
rtk discover          # Analyze Claude Code history for missed opportunities
rtk proxy <cmd>       # Execute raw command without filtering (for debugging)
```

## How It Works

RTK installs as a PreToolUse hook in Claude Code. It transparently rewrites
Bash commands before execution to compress their output:

```text
git status    → rtk git status     (75% savings)
cargo test    → rtk cargo test     (90% savings)
docker ps     → rtk docker ps      (75% savings)
cat file.rs   → rtk read file.rs   (70% savings)
```

The agent never sees the rewrite — only the compressed output.

## Commands Rewritten by Hook

| Category | Commands | Avg Savings |
|----------|----------|-------------|
| Git | status, diff, log, add, commit, push, pull | 75-94% |
| GitHub CLI | pr list/view, issue list, run list | ~80% |
| Tests | cargo test, pytest, vitest, go test, playwright | ~90% |
| Build/Lint | cargo build/clippy, tsc, eslint, ruff, golangci-lint | ~80% |
| Files | ls, cat/head/tail, find, grep | ~70% |
| Containers | docker ps/images/logs, kubectl pods/logs | ~75% |
| Packages | pnpm list, pip list/outdated | ~70% |

## Commands NOT Rewritten (pass-through)

- `echo`, `make`, `terraform`, `helm`
- Commands already prefixed with `rtk`
- Heredocs (`<<`)
- Unrecognized commands

## Migration from grepai (2026-04)

The previous `grepai` + `ollama` semantic-search stack was removed: bge-m3
embeddings on the host, daemon + watchdog inside the container, and an
indexed `.grepai/` directory together cost more CPU/RAM than they saved
in agent quality. RTK alone now covers the token-cost problem; semantic
understanding is delegated to targeted `Grep`/`Read` inside specialist
agents (which have access to file context anyway).

If you previously relied on `mcp__grepai__*` tools, switch to:
- `Grep` for exact strings / regex
- `Read` for full-file inspection
- `mcp__context7__*` for up-to-date library/framework documentation
- `rtk discover` to find new commands worth rewriting

## Configuration

File: `~/.config/rtk/config.toml`

```toml
[hooks]
exclude_commands = []  # Skip rewrite for specific commands

[tee]
enabled = true         # Save raw output on failure
mode = "failures"      # "failures", "always", "never"
max_files = 20         # Rotation limit
```

## Installation Verification

```bash
rtk --version         # Should show: rtk X.Y.Z
rtk gain              # Should work (not "command not found")
which rtk             # Verify correct binary (/usr/local/bin/rtk)
```
