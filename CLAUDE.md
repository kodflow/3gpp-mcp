<!-- updated: 2026-04-24T10:50:00Z -->
# devcontainer-template

## Purpose

Universal DevContainer shell providing cutting-edge AI agents, skills, and workflows to bootstrap any project. Reliability first: agents reason deeply, cross-reference sources, and self-correct until the output meets quality standards.

## Project Structure

```
/workspace
├── .devcontainer/   # Container config, features, hooks, images
├── .github/         # GitHub Actions workflows
├── .githooks/       # Git hooks (pre-commit: regenerate assets)
├── .claude/         # Workspace Claude overrides (settings.local.json, features.json)
├── docs/            # Documentation (plain markdown: vision, architecture, guides)
├── src/             # All source code (created per project via /init)
├── tests/           # Unit tests (created per project via /init)
├── AGENTS.md        # Specialist agents specification (81 agents)
└── CLAUDE.md        # This file
```

## Tech Stack

- **Languages**: Python, C, C++, Java, C#, JavaScript/Node.js, Visual Basic, R, Pascal, Perl, Fortran, PHP, Rust, Go, Ada, MATLAB, Assembly, Kotlin, Swift, COBOL, Ruby, Dart, Lua, Scala, Elixir, SQL
- **Cloud CLIs**: AWS v2, GCP SDK, Azure CLI
- **IaC**: Terraform, Vault, Consul, Nomad, Packer, Ansible
- **Containers**: Docker, kubectl, Helm
- **AI**: Claude Code, RTK (token savings via PreToolUse hook), MCP servers (GitHub, GitLab, context7 + feature-based: Playwright, ktn-linter)

## How to Work

1. **New project**: `/init` → conversational discovery → doc generation
2. **New feature**: `/plan "description"` → planning mode → `/do` → `/git --commit`
3. **Bug fix**: `/plan "description"` → planning mode → `/do` → `/git --commit`
4. **Code review**: `/review` → 3-tier review (agents + Qodo + CodeRabbit)

Branch conventions: `feat/<desc>` or `fix/<desc>`, commit prefix matches.

## Key Principles

**Reliability first**: Verify before generating. Agents consult context7 and official docs before producing non-trivial code.

**MCP-first**: Use MCP tools (`mcp__github__*`, `mcp__gitlab__*`) before CLI fallbacks. Auth is pre-configured.

**Self-correction**: When linting or tests fail, agents fix and retry automatically.

**Token efficiency**: RTK (`rtk hook claude` PreToolUse hook) auto-compresses Bash output for 60–90 % token savings. **Default is rtk-prefix every Bash call** — runtime is never blocking, but `session-init.sh` surfaces `[rtk] mode=… reason=…` when degraded so you see it. Bypass via `RTK_BYPASS=1` is first-class (advisory `session-bypass`, never conflated with degradation); single-call bypass = type without prefix and the miss surfaces in `rtk discover`. Use `rtk gain` for analytics. **No semantic-embedding tooling** (`grepai`/`ollama` were dropped in 2026-04 — high CPU/RAM cost, marginal benefit). Search with targeted `Grep` + `Read`.

**Specialist agents**: Language conventions enforced by agents that know current stable versions.

**Deep reasoning**: For complex tasks — Peek, Decompose, Parallelize, Synthesize.

**Bot reviews are signal, not orders**: CodeRabbit, Qodo, Codacy and similar AI review bots produce useful hints but their findings are **non-binding**. Triage with judgment — never blindly iterate on every comment. Reject (with a short rationale) any finding that is:
- A style nitpick, not a real bug
- Defensive hardening against a threat model that does not apply (e.g., `mktemp` in a root-only devcontainer build)
- A false positive (run the actual linter / test before accepting the bot's claim)
- An out-of-scope rewrite that would expand the PR beyond its original intent
- The third+ new demand on the same PR — after two rounds of fixes, stop, respond with rationale, and merge

If the CI bot says "no" and you have verified the code is correct, the CI is wrong. Post the rationale, move on, merge.

**Intended working directories are writeable without prompting**: `.claude/contexts/` (search outputs), `.claude/plans/` (planning mode), and other agent-managed working directories are configured as writeable in `~/.claude/settings.json`. Never treat them as "sensitive" — they are the agent's scratchpad. If a permission prompt fires for these paths, fix the settings, do not ask the user.

## Safeguards

Ask before:
- Deleting files in `.claude/` or `.devcontainer/`
- Removing features from `.claude/commands/*.md`
- Removing hooks from `.devcontainer/hooks/`
- Dropping database state, force-push, dependency downgrades

Investigate before deleting unfamiliar state (branches, lock files, unknown files) — it may be user work-in-progress.

Never ask for:
- Writing new files under `.claude/contexts/` or `.claude/plans/` (configured as free-write in `~/.claude/settings.json`)

When refactoring: move content to separate files, preserve logic.

## Pre-commit

Auto-detected by language marker (`go.mod`, `Cargo.toml`, `package.json`, etc.). Priority: Makefile targets, then language-specific commands.

## Hooks (17 event types)

| Hook | Purpose |
|------|---------|
| SessionStart | Cache project metadata + compact recovery |
| SessionEnd | Session cleanup |
| UserPromptSubmit | Prompt tracking |
| PreToolUse | Commit validate, security scan, RTK rewrite, logging |
| PostToolUse | Format + lint, security, test, feature update, logging |
| PostToolUseFailure | Failure diagnostics |
| PermissionRequest | Permission logging |
| SubagentStart/Stop | Agent lifecycle tracking |
| Stop | Session summary + terminal bell + quality gate (lint/typecheck/test) |
| TeammateIdle | Multi-agent coordination + pending-task enforcement |
| TaskCreated | Task payload contract validation + file conflict advisory |
| TaskCompleted | Async task completion + registry lifecycle transition |
| ConfigChange | Configuration change tracking |
| WorktreeCreate/Remove | Git worktree lifecycle |
| PreCompact | Context preservation before compaction |
| Notification | External monitoring notifications |

## Agent Teams (experimental)

Parallel multi-agent execution for 6 high-value skills (`/review`, `/plan`, `/do`, `/infra`, `/test`, `/improve`). Each skill detects its runtime mode at invocation and branches:

| Capability (persisted) | Runtime mode | Where |
|---|---|---|
| TMUX | TEAMS_TMUX | Split-pane teammates in tmux |
| IN_PROCESS | TEAMS_INPROCESS | Shift+Down to cycle teammates |
| NONE | SUBAGENTS | Legacy Task-tool dispatch |

**Single source of truth:** `.devcontainer/images/.claude/commands/shared/team-mode.md`
**Primitives:** `~/.claude/scripts/team-mode-primitives.sh` (`detect_runtime_mode`, `extract_task_contract`, `classify_terminal`, …)
**Capability file:** `~/.claude/.team-capability` (hint only — live probe is source of truth)
**Install:** automatic via `install.sh`; opt-out with `install.sh --no-teams`
**Runtime opt-out:** `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=0 /<skill>`
**Debug:** `TEAM_MODE_DEBUG=1` → stderr decision logs
**Kill switch:** `echo NONE > ~/.claude/.team-capability`

Every team task embeds a `<!-- task-contract v1 ... -->` JSON block (contract_version, access_mode, owned_paths, acceptance_criteria, …). Parsed and validated by `task-created.sh` in advisory mode — strict only on explicit contract violations.

## /secret - Secure Secret Management (1Password)

```
/secret --push DB_PASSWORD=mypass     # Store secret
/secret --get DB_PASSWORD             # Retrieve secret
/secret --list                        # List project secrets
/secret --push KEY=val --path org/other  # Cross-project
```

**Path convention:** `<org>/<repo>/<key>` (auto-resolved from git remote)
**Backend:** 1Password CLI (`op`) with `OP_SERVICE_ACCOUNT_TOKEN`
**Integration:** `/init` (check), `/git` (scan), `/do` (discover), `/infra` (TF_VAR_*)

## Documentation Hierarchy

```
CLAUDE.md                    # This overview
├── AGENTS.md                # Specialist agents (79 agents)
├── docs/vision.md           # Objectives, success criteria
├── docs/architecture.md     # System design, components
├── docs/workflows.md        # Detailed workflows
├── docs/ktn-linter-integration.md  # ktn-linter hook contract
├── .github/CLAUDE.md        # GitHub Actions, dependabot
├── .devcontainer/CLAUDE.md  # Container config details
│   ├── features/CLAUDE.md   # Language & tool features
│   ├── hooks/CLAUDE.md      # Host-side hooks (initialize.sh only)
│   └── images/CLAUDE.md     # Two-tier images (base + dynamic)
└── .claude/commands/        # Slash commands (20 skills)
```

Principle: More detail deeper in tree. Target < 200 lines each.

## Commands

| Command | Purpose |
|---------|---------|
| `/init` | Conversational project discovery + doc generation |
| `/plan` | Analyze codebase and design implementation approach |
| `/review` | Code review (3-tier: agents + Qodo + CodeRabbit) |
| `/git` | Conventional commits, branch management |
| `/search` | Documentation research with official sources |
| `/test` | E2E testing with Playwright MCP |
| `/lint` | Multi-language intelligent linting |
| `/ktn` | Autonomous ktn-linter MCP lifecycle (binary, mcp.json, hooks, daemon, phases) |
| `/infra` | Infrastructure automation (Terraform/Terragrunt) |
| `/secret` | Secure secret management (1Password) |
| `/vpn` | Multi-protocol VPN management |
| `/warmup` | Context pre-loading and CLAUDE.md update |
| `/update` | DevContainer update from template |
| `/improve` | Documentation QA for design patterns |
| `/learn` | Extract reusable patterns from the current session into `~/.claude/docs/learned/` |
| `/feature` | Feature tracking RTM (CRUD, audit, auto-learn) |
| `/refine` | Skills Architecture v1.3 — goal contract generator (10-lens analysis) |
| `/prompt` | **[DEPRECATED]** Use `/refine` instead — scheduled for deletion in PR6 |

### Canonical workflow (Skills Architecture v1.6)

```
/search → /plan → /refine → /goal <slug>
```

`/plan --goal` chains automatically into `/refine` once the plan is
written. `/refine` writes a contract at `.claude/goals/<slug>.md` and
prints a textual `Suggested next step: /goal <slug>` — there is no
auto-chain. **`/goal` is a harness builtin** (not a repository
command file): the user types `/goal <slug>` to enter goal iteration
against the persistent state in `.claude/state/goals/<slug>.json`.

## Collaboration Rules

**Response style**
- Terse. No trailing summary ("I did X, Y, Z"). The diff and output are enough.
- Lead with action or decision, not with preamble.
- French or English to match the user's language.

**Tool discipline**
- Dedicated tools over Bash equivalents: Read (not cat), Edit (not sed), Glob (not find), Grep (not grep).
- Read the full file before modifying it. No guessing.
- Parallelize independent tool calls in a single message when possible.

**Git discipline**
- Branch prefix matches commit prefix: `feat/*` → `feat:`, `fix/*` → `fix:`.
- Never `--no-verify`, never `git push --force` without explicit user approval.
- Always create NEW commits after hook failure, never `--amend`.

**Memory discipline**
- Propose feedback/user/project memories when the user corrects you, confirms an unusual choice, or shares a deadline/constraint.
- At session end with significant corrections, suggest running `/learn`.

**Destructive actions** — see the canonical [Safeguards](#safeguards) section above.

## Verification

Changes are complete when:
- Tests pass (`make test` or language equivalent)
- Linting passes (auto-run by hooks)
- No secrets in commits (checked by security hook)
- Commit follows conventional format
