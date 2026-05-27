# Configuration

## Environment Variables

The `.devcontainer/.env` file configures the container. It is created automatically on first launch from `.devcontainer/.env.tpl`.

### Required

| Variable | Value | Example |
|----------|-------|---------|
| `GIT_USER` | Name for commits | `John Doe` |
| `GIT_EMAIL` | Email for commits | `john@example.com` |

These values are read by `postCreate.sh` to configure `git config`.

### MCP Tokens (optional)

| Variable | Service | How to Obtain |
|----------|---------|---------------|
| `GITHUB_TOKEN` | GitHub MCP (PRs, issues) | [Settings → Developer settings → Fine-grained tokens](https://github.com/settings/tokens?type=beta) |
| `GITLAB_TOKEN` | GitLab MCP (MRs, pipelines) | [Preferences → Access Tokens](https://gitlab.com/-/user_settings/personal_access_tokens) |

Without a token, the corresponding MCP server does not start. Commands (`/review`, `/git`) fall back to CLIs (`gh`, `glab`).

### Secrets and VPN (optional)

| Variable | Usage |
|----------|-------|
| `OP_SERVICE_ACCOUNT_TOKEN` | 1Password CLI auth for `/secret` |
| `VPN_CONFIG_REF` | 1Password reference to VPN profile (e.g., `op://VPN/MyVPN/config`) |

### Full Example

```env
# .devcontainer/.env
GIT_USER=John Doe
GIT_EMAIL=john@example.com

# MCP Tokens
GITHUB_TOKEN=ghp_xxxxxxxxxxxxxxxxxxxx
GITLAB_TOKEN=glpat-xxxxxxxxxxxxxxxxxxxx

# Secrets
OP_SERVICE_ACCOUNT_TOKEN=ops_xxxxxxxxxxxx

# VPN (auto-connect on startup)
VPN_CONFIG_REF=op://VPN/Office/config
```

## Persistent Volumes

8 Docker volumes preserve data between container rebuilds:

| Volume | Path | What Persists |
|--------|------|---------------|
| `package-cache` | `~/.cache` | npm, pip, cargo, maven, gradle, go-build |
| `npm-global` | `~/.local/share/npm-global` | Global npm packages |
| `claude-config` | `~/.claude` | Claude sessions, settings, history |
| `op-config` | `~/.config/op` | 1Password config |
| `op-cache` | `~/.op` | 1Password cache |
| `zsh-history` | `~/.zsh_history_dir` | Shell history |
| `gnupg` | `~/.gnupg` | GPG keys (from host) |
| `docker-socket` | `/var/run/docker.sock` | Docker-from-Docker access |

!!! warning "Home is not a volume"
    Only these subdirectories persist. The rest of `~` is recreated on each rebuild from the image. Claude files are restored from `/etc/claude-defaults/` by `postStart.sh`.

## Enabling Optional Features

In `devcontainer.json`, uncomment features as needed:

```jsonc
"features": {
    // Always enabled (required for MCP)
    "ghcr.io/devcontainers/features/node:1": {},

    // Uncomment for local Kubernetes
    // "ghcr.io/kodflow/devcontainer-features/kubernetes:latest": {
    //     "kindVersion": "0.31.0",
    //     "kubectlVersion": "1.35.0"
    // },

    // Uncomment for Docker-in-Docker
    // "ghcr.io/devcontainers/features/docker-outside-of-docker:1": {
    //     "moby": false,
    //     "installDockerBuildx": true
    // }
}
```
