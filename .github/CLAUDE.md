<!-- updated: 2026-04-10T12:00:00Z -->
# GitHub Configuration

## Purpose

GitHub-specific configurations: workflows, templates, and instructions.

## Structure

```
.github/
├── workflows/          # GitHub Actions
│   ├── docker-images.yml
│   ├── publish-features.yml
│   ├── release.yml
│   ├── tests.yml
│   └── CLAUDE.md
├── instructions/       # AI coding instructions
├── dependabot.yml      # Dependency updates
└── CLAUDE.md           # This file
```

## Workflows

| Workflow | Trigger | Description |
|----------|---------|-------------|
| docker-images.yml | push/PR/schedule | Two-tier build (base weekly + main daily) |
| publish-features.yml | push to main (features) | Publish features as OCI artifacts to GHCR |
| release.yml | push to main | Create release with claude-assets.tar.gz |
| tests.yml | push/PR | Run unit tests (hooks, scripts) |

## Dependency Management

| File | Description |
|------|-------------|
| dependabot.yml | Automated dependency update configuration |

## Conventions

- Workflows use reusable actions where possible
- Secrets stored in GitHub repository settings
- Branch protection on main
