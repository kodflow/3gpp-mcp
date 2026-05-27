<!-- updated: 2026-04-21T15:27:23Z -->
# GitHub Actions Workflows

## Purpose

CI/CD automation for the devcontainer template.

## Workflows

| File | Description |
|------|-------------|
| `docker-images.yml` | Build and push devcontainer images |
| `publish-features.yml` | Publish Dev Container Features as OCI artifacts to GHCR |
| `release.yml` | Create GitHub Release with claude-assets.tar.gz |
| `tests.yml` | Run unit tests for hooks and scripts |

## docker-images.yml (Two-Tier Build)

**Base image** (`devcontainer-base`):
- **Trigger**: Weekly (Sunday 3AM UTC), `[base]` in commit message, manual dispatch
- **Content**: apt, PPA tools, Cloud CLIs, Oh My Zsh (~1.1GB, stable)

**Main image** (`devcontainer-template`):
- **Trigger**: Push to main, PRs, daily (4AM UTC), ktn-linter-release
- **Content**: kubectl, rtk, Claude Code, CodeRabbit, Qodo (~100MB delta)

- **Registry**: ghcr.io
- **Tags**: latest, date, commit SHA
- **Platforms**: linux/amd64, linux/arm64
- **Cache busting**: Scheduled builds pass `CACHE_BUST_DYNAMIC=YYYY-MM-DD`

## publish-features.yml

- **Trigger**: Push to main (features changed), workflow_dispatch
- **Action**: Flattens features, embeds shared utils, publishes as OCI artifacts
- **Registry**: `ghcr.io/kodflow/devcontainer-features/<feature>:v<version>`
- **Uses**: `devcontainers/action@v1`

## release.yml

- **Trigger**: Push to main, workflow_dispatch
- **Action**: Generates `claude-assets.tar.gz` and creates a GitHub Release
- **Tag format**: `vYYYY.MM.DD-<sha7>`
- **Latest**: Always marks as latest release (used by `install.sh`)

## tests.yml

- **Trigger**: Push to main, PRs
- **Action**: Runs unit tests for hooks and scripts
- **Uses**: `bats-core/bats-action@4.0.0`

## Conventions

- Use `ubuntu-latest` runners
- Cache Docker layers for speed
- Action SHAs pinned with version comments
- Use GITHUB_TOKEN for authentication
