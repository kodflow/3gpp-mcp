# Guides

## Quick Start

```bash
# 1. Clone and open in VS Code
git clone https://github.com/kodflow/devcontainer-template
code devcontainer-template

# 2. Reopen in container
# Ctrl+Shift+P → "Dev Containers: Reopen in Container"

# 3. Start working
/warmup              # Load project context
/docs                # Serve documentation
```

## Common Workflows

### Starting a Feature

```bash
/warmup                      # Load context
/plan                        # Design approach
# ... implement ...
/review                      # Self-review
/git --commit                # Commit with conventional format
```

### Code Review

```bash
/review                      # Review current changes
/review --pr 123             # Review specific PR
/review --loop               # Iterative review cycle
```

### Documentation

```bash
/docs                        # Serve docs locally
/docs --update               # Regenerate from codebase
/docs --stop                 # Stop server
```

## Available Commands

| Command | Description |
|---------|-------------|
| `/warmup` | Load CLAUDE.md hierarchy |
| `/init` | Validate environment |
| `/plan` | Enter planning mode |
| `/do` | Execute approved plan |
| `/docs` | Documentation server |
| `/review` | Code review |
| `/git` | Git automation |
| `/lint` | Intelligent linting |
| `/test` | E2E testing with Playwright |
