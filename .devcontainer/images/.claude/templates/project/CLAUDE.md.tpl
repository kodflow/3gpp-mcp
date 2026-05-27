# {{PROJECT_NAME}}

## Purpose

{{PROJECT_DESCRIPTION}}

## Tech Stack

{{#LANGUAGES}}
- {{.}}
{{/LANGUAGES}}
{{#DATABASES}}
- {{.}}
{{/DATABASES}}

## How to Work

1. `/init` - Verify environment setup
2. `/feature <description>` - Start new feature branch
3. `/fix <description>` - Start bug fix branch

## Key Principles

- MCP-first for integrations
- RTK auto-rewrites Bash output for token savings
- Specialist agents for {{PRIMARY_LANGUAGE}}

## Verification

Changes complete when:
- Tests pass (`{{TEST_COMMAND}}`)
- Lint passes (auto via hooks)
- Security scan clean (Codacy)

## Documentation

- [Vision](docs/vision.md) - Goals & success criteria
- [Architecture](docs/architecture.md) - System design
- [Workflows](docs/workflows.md) - Development processes
