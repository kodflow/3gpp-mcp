<!-- updated: 2026-04-24T10:50:00Z -->
# DevContainer Images

## Purpose

Two-tier Docker images with all development tools pre-installed.
Claude Code and MCP servers are included; languages added via features.

**Base image** (`Dockerfile.base`): Stable deps (apt, Cloud CLIs) — rebuilt weekly.
**Main image** (`Dockerfile`): Dynamic tools (Claude, rtk) — rebuilt daily.

## Structure

```text
.devcontainer/images/
├── Dockerfile.base     # Stable layer (~1.1GB, weekly rebuild)
├── Dockerfile          # Dynamic layer (~120MB, daily rebuild)
├── .dockerignore       # Build context exclusions
├── mcp.json.tpl        # MCP server template
├── rtk.config.toml     # RTK PreToolUse rewrite config
├── .p10k.zsh           # Powerlevel10k config
├── scripts/vpn/        # VPN helper scripts
├── hooks/              # Image-embedded lifecycle hooks
│   ├── shared/utils.sh # Shared utilities
│   └── lifecycle/      # onCreate, postCreate, postStart, etc.
└── .claude/            # Claude Code configuration
    ├── commands/       # Slash commands (17 skills)
    ├── scripts/        # Hook scripts (31 scripts)
    ├── agents/         # Agent definitions (79 agents)
    ├── docs/           # Design Patterns Knowledge Base (170+ patterns)
    ├── templates/      # Project/docs/terraform templates
    └── settings.json   # Claude settings
```

## Container Paths (Runtime)

| Source (Build) | Container Path | Backup Location |
|----------------|----------------|-----------------|
| `.claude/` | `/home/vscode/.claude/` | `/etc/claude-defaults/` |
| `.claude/commands/` | `~/.claude/commands/` | `/etc/claude-defaults/commands/` |
| `.claude/scripts/` | `~/.claude/scripts/` | `/etc/claude-defaults/scripts/` |
| `.claude/agents/` | `~/.claude/agents/` | `/etc/claude-defaults/agents/` |
| `.claude/docs/` | `~/.claude/docs/` | `/etc/claude-defaults/docs/` |
| `mcp.json.tpl` | `/etc/mcp/mcp.json.tpl` | - |
| `hooks/` | `/etc/devcontainer-hooks/` | - |
| `features/` (CI-staged) | `/etc/devcontainer-template/features/` | 3-way safe-synced to `.devcontainer/features/` |
| `image-template-files.json` (CI-built) | `/etc/devcontainer-template/.template-files.json` | sha256 manifest powering the 3-way sync |

**Note:** Claude files restored from `/etc/claude-defaults/` at each start via `postStart.sh`.
Lifecycle hooks called directly from `devcontainer.json` → `/etc/devcontainer-hooks/` (no stubs).
`.devcontainer/features/` is **3-way merged** from `/etc/devcontainer-template/features/` at every
`postStart` (step `step_sync_features`, helper `shared/sync-features.sh`). Per-file protection
(issue #334 + #367):

1. byte-identical → noop;
2. tracked + git-dirty → preserved (`[WARNING]`);
3. previous shipped sha256 (from `.template-files.json::files`) matches current dst → safe overwrite;
4. dst hash appears in `.template-files.json::previous_hashes[rel]` → fast-forward, silent overwrite (stale-but-clean);
5. otherwise → preserved with an improved `[WARNING]` pointing at `git diff` (real consumer fork).

`--delete` only removes dst files whose sha256 still matches the previous manifest entry, so
consumer-added files are never deleted.

Manifest schema v2 (CI-built by `build-features-manifest.py`, see `scripts/`): adds an optional
`previous_hashes[rel] = [sha256, …]` list capped at 8 generations. The CI workflow
(`docker-images.yml`) fetches the previous image's manifest via `docker pull + docker cp` and
passes it as `--prev-manifest` to the builder; failure is non-fatal (one-build degradation,
self-healing).

Template repo self-skip: see `step_sync_features` in `postStart.sh` — primary path matches
`origin` URL against `kodflow/devcontainer-template` (#367); legacy `.template-version`
marker-file path stays as secondary opt-in.

### Local script overrides (`*.local.sh`)

Mirror of the `devcontainer.local.json` pattern, scoped to `~/.claude/scripts/`. To customise a
hook script without forking the upstream copy, drop a `<name>.local.sh` next to it:

| Upstream | Override seam |
|----------|---------------|
| `~/.claude/scripts/pre-commit-quality.sh` | `~/.claude/scripts/pre-commit-quality.local.sh` |
| `~/.claude/scripts/pre-commit-checks.sh` | `~/.claude/scripts/pre-commit-checks.local.sh` |
| `~/.claude/scripts/test.sh` | `~/.claude/scripts/test.local.sh` |

The upstream script sources its `.local.sh` companion **after** every upstream function is
defined and immediately before the entrypoint, so `.local.sh` can redefine any function (shell
uses last-definition-wins). `/update` and `safe_glob_copy` skip every `*.local.sh` file, so the
override survives template syncs. Issue ref: kodflow/devcontainer-template#352.

Example (`~/.claude/scripts/pre-commit-quality.local.sh`):

```bash
# Force Bazel for this project; can be deleted once #350 lands.
run_test() {
    local out="$1"
    (cd "$PROJECT_ROOT" && bazel test --test_output=errors //...) >> "$out" 2>&1 || return 1
}
```

## Design Patterns Knowledge Base

**Container Location:** `~/.claude/docs/` (restored at startup)

170+ pattern files across 19 categories, consulted by `/plan` and `/review`.

| Category | Files | Examples |
|----------|-------|----------|
| GoF (25 files) | creational, structural, behavioral | Factory, Observer, Strategy |
| Architectural (9) | architectural/ | MVC, Hexagonal, CQRS |
| Cloud + Resilience (27) | cloud/, resilience/ | Circuit Breaker, Saga, Retry |
| Concurrency (8) | concurrency/ | Thread Pool, Actor, Mutex |
| DDD (8) | ddd/ | Aggregate, Repository, Entity |
| DevOps (14) | devops/ | Feature Toggles, Blue-Green |
| Enterprise (12) | enterprise/ | PoEAA (Martin Fowler) |
| Functional (5) | functional/ | Monad, Either, Lens |
| Integration + Messaging (15) | integration/, messaging/ | API Gateway, EIP |
| Performance (8) | performance/ | Cache, Lazy Load, Pool |
| Principles (7) | principles/, conventions/ | SOLID, DRY, KISS |
| Security (8) | security/ | OAuth, JWT, RBAC |
| Testing (8) | testing/ | Mock, Stub, Fixture |

**Agent usage:** See `.claude/docs/CLAUDE.md`

## Installed Tools

| Category | Tools |
|----------|-------|
| Cloud CLIs | AWS, GCP, Azure, 1Password |
| IaC | Terraform, Vault, Consul, Nomad, Packer, Ansible |
| Container | Docker (via feature), kubectl, Helm |
| Network | ping, dig, nmap, traceroute, mtr, tcpdump, netcat, whois, iperf3, net-tools |
| VPN | OpenVPN, WireGuard, StrongSwan (IPsec), PPTP |
| Code Quality | ShellCheck, CodeRabbit, Qodo, RTK |
| Shell | Zsh (default `$SHELL`) + Oh My Zsh + Powerlevel10k |

## Shell Startup Optimization (v3)

`~/.devcontainer-env.sh` uses a three-pillar architecture for fast shell startup:

| Pillar | Mechanism | Effect |
|--------|-----------|--------|
| Lazy wrappers | `nvm`, `pyenv`, `rbenv`, `sdk` load on first use | ~1.4s saved |
| Cached completions | `~/.zsh_completions/` via fpath (pre-generated by postStart) | ~750ms saved |
| Dynamic p10k segments | `~/.p10k-segments.zsh` based on installed tools | ~70ms saved |

**Phase 1** (always): PATH, env vars, fpath — no subprocesses.
**Phase 2** (terminal only): lazy wrappers, aliases, fast `complete -C` for HashiCorp/AWS.

Tool binaries (`node`, `python`, `ruby`, `java`) work immediately via Phase 1 PATH/shims.
Management commands (`nvm use`, `pyenv install`) trigger lazy-load on first call.

## MCP Servers (Runtime)

Core servers in `mcp.json.tpl` (GitHub, GitLab). Additional servers added via MCP fragments:
- Image-level fragments (`/etc/mcp/fragments/`): context7 — always merged
- Feature-level fragments (`/etc/mcp/features/`): ktn-linter (Go), Playwright (browser), rust-analyzer, etc.

| Server | Package | Type | Auth |
|--------|---------|------|------|
| **GitHub** | `ghcr.io/github/github-mcp-server` (Docker) | Core (template) | `GITHUB_TOKEN` |
| **GitLab** | `@zereight/mcp-gitlab` | Core (template) | `GITLAB_TOKEN` |
| **context7** | `@upstash/context7-mcp` | Fragment (image) | None |
| **ktn-linter** | `ktn-linter` (binary) | Fragment (Go feature) | None |
| **Playwright** | `@playwright/mcp` | Fragment (browser feature) | None |

**Removed in 2026-04:** `grepai` MCP server (semantic search via Ollama embeddings)
— high CPU/RAM cost for marginal benefit. Use `Grep`/`Read` for searches and
`mcp__context7__*` for library documentation.

**GitLab tools (when GITLAB_TOKEN configured):**

| Tool | Description | Use Case |
|------|-------------|----------|
| `gitlab_list_projects` | List accessible projects | Project discovery |
| `gitlab_get_project` | Get project details | Project info |
| `gitlab_list_merge_requests` | List MRs | Code review |
| `gitlab_get_merge_request` | Get MR details | Review analysis |
| `gitlab_list_issues` | List project issues | Issue tracking |
| `gitlab_list_pipelines` | List CI pipelines | CI/CD status |

**GitLab env vars:** `GITLAB_TOKEN`, `GITLAB_API_URL` (default: gitlab.com)

**Context7 usage:** Add "use context7" in prompts to fetch up-to-date documentation.

**Playwright capabilities** (when browser feature enabled): `core`, `pdf`, `testing`, `tracing` (headless mode)

## Skills (Slash Commands)

| Skill | Description |
|-------|-------------|
| `/init` | Project initialization check |
| `/plan` | Planning mode for implementation strategy |
| `/do` | Iterative task execution loop (RLM) |
| `/review` | AI-powered code review (3-tier: agents + Qodo + CodeRabbit) |
| `/git` | Workflow Git automation (commit, push, PR, merge) |
| `/search` | Documentation research with official sources |
| `/docs` | Deep project documentation generation (multi-agent) |
| `/test` | E2E testing with Playwright MCP |
| `/lint` | Intelligent linting with ktn-linter (148 rules) |
| `/ktn` | ktn-linter MCP lifecycle: install/upgrade binary, wire hooks, heal daemon, phase config (5 parallel agents) |
| `/infra` | Infrastructure automation (Terraform/Terragrunt) |
| `/secret` | Secure secret management (1Password + Vault-like paths) |
| `/vpn` | Multi-protocol VPN management (OpenVPN, WireGuard, IPsec, PPTP) |
| `/warmup` | Context pre-loading and CLAUDE.md update |
| `/update` | DevContainer update from template |
| `/improve` | Continuous docs enhancement & anti-pattern detection |
| `/feature` | Feature tracking (RTM) with --add, --edit, --del, --list, --checkup |
| `/prompt` | Generate ideal prompt structure for /plan |

## Hooks (31 scripts, 17 event types)

Core hooks (always active):

| Hook | Trigger | Action |
|------|---------|--------|
| `git-guard.sh` | PreToolUse (Bash) | Block AI commits + secret scan + force-with-lease |
| `rtk hook claude` | PreToolUse (Bash) | Rewrite commands via RTK for token savings (native binary, replaces legacy `rtk-rewrite.sh`) |
| `pre-validate.sh` | PreToolUse (Write/Edit) | Protect sensitive files |
| `post-edit.sh` | PostToolUse (Write/Edit) | **Format only** (fast, ~100-500ms) |
| `on-stop-quality.sh` | Stop (*) | **Batch lint + typecheck + test** (deduplicated) |
| `on-stop.sh` | Stop (*) | Terminal bell + session summary |
| `session-init.sh` | SessionStart (all) | Cache git metadata as env vars |
| `post-compact.sh` | SessionStart (compact) | Restore RLM context rules |
| `notification.sh` | Notification (*) | Terminal bell + notification log

Full inventory: See `.devcontainer/hooks/CLAUDE.md` and `CLAUDE.md` (root).

**ktn-linter integration (embedded in existing hook scripts):**

| Script | Endpoint | Timeout | Phase scope (default) | Env override | Purpose |
|--------|----------|---------|-----------------------|--------------|---------|
| `pre-validate.sh` | `/hooks/pre-tool-use` | 4s | `structural,signatures` (1–2) | `KTN_PRE_PHASES` | Pre-edit fast check — only naming/signature breaks block before the edit |
| `on-stop.sh` | `/hooks/stop` | 28s | `structural,…,comment,tests` (1–8) | `KTN_STOP_PHASES` | Session-end report — includes test-quality phase |

The `phases` field is injected into the JSON request body via `jq` (per-request override; server YAML config is preserved for any consumer that doesn't pass `phases`). Servers pre-ktn-linter-#190 ignore the unknown field and fall back to YAML — back-compat safe. Override per-project via the `KTN_*_PHASES` env vars (comma-separated, no spaces). Graceful degradation: calls exit silently if ktn-linter is not running or `jq` is missing (raw `${INPUT:-{}}` is sent unchanged). See [docs/ktn-linter-integration.md](/workspace/docs/ktn-linter-integration.md).

**Per-edit lint (PostToolUse) — project-level recipe.** `post-edit.sh` deliberately does NOT curl `/hooks/post-tool-use`: Claude Code keeps only the *last* JSON emitted by a hook chain, so a script-level call would race with the native HTTP hook and silently drop `decision: "block"` payloads (issue #344). Consumers needing per-edit lint wire the HTTP hook directly in their project's `.claude/settings.json`:

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Edit|Write|MultiEdit",
        "hooks": [
          {
            "type": "http",
            "url": "http://localhost:7717/hooks/post-tool-use",
            "timeout": 15
          }
        ]
      }
    ]
  }
}
```

The native hook speaks the Claude Code hook payload protocol directly (decision + hookSpecificOutput) — no re-parse, no re-wrap, no payload mangling.

**Makefile-first pattern:** Scripts check `make fmt/lint/typecheck/test FILE=<path>` first, then fall back to direct tool invocation.

## Build

```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  -t ghcr.io/kodflow/devcontainer-template:latest .
```
