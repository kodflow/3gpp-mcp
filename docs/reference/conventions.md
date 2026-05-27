# Conventions

## Commits

Format: `type(scope): message`

| Type | Usage |
|------|-------|
| `feat` | New feature |
| `fix` | Bug fix |
| `docs` | Documentation |
| `refactor` | Restructuring without functional change |
| `test` | Adding or modifying tests |
| `chore` | Maintenance (CI, deps, config) |
| `perf` | Performance optimization |

The scope is inferred from the main modified directory. Examples: `feat(auth): add JWT login`, `fix(api): handle timeout error`.

## Branches

| Prefix | Usage |
|--------|-------|
| `feat/` | New feature |
| `fix/` | Bug fix |
| `docs/` | Documentation |
| `refactor/` | Restructuring |

Never commit directly to `main`. Always go through a branch + PR.

## Merge Strategy

Squash merge by default. GitHub automatically deletes the remote branch after merge.

## Code Structure

| Directory | Contents |
|-----------|----------|
| `src/` | All source code (required) |
| `tests/` | Unit tests (Go: alongside code in `src/`) |
| `docs/` | Documentation |
| `.devcontainer/` | Container configuration |

## Makefile

Quality hooks look for a Makefile target first before using tools directly:

| Target | Usage |
|--------|-------|
| `make fmt` / `make format` | Code formatting |
| `make lint` | Linting |
| `make typecheck` | Type checking |
| `make test` | Tests |

If your project has a Makefile with these targets, the hooks use it. Otherwise, they detect the language and run the corresponding tool.

## Protected Files

Claude hooks prevent accidental modification of:

- `.devcontainer/` — container configuration
- `.claude/scripts/` — hook scripts
- `.env` — environment variables
- `node_modules/`, `vendor/` — dependencies
- `*.lock` — lock files


