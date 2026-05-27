# Shared: Agent Teams Execution Protocol

> **Single source of truth** for Agent Teams behavior across all skills.
> Every team-aware skill `@`-references this file instead of duplicating the detection logic.
> Implementation: `~/.claude/scripts/team-mode-primitives.sh`
> Reference: <https://code.claude.com/docs/en/agent-teams>

---

## 1. Vocabulary (normative)

Two distinct taxonomies — do not mix them.

### Capability (persisted in `~/.claude/.team-capability`)

Cache written by `install.sh` at install time.

| Value | Meaning |
|---|---|
| `TMUX` | claude ≥ 2.1.32 + env flag + tmux binary + known-compatible terminal |
| `IN_PROCESS` | claude ≥ 2.1.32 + env flag, but tmux absent OR terminal incompatible/unknown |
| `NONE` | claude < 2.1.32 OR `--no-teams` flag OR env flag disabled |

### Runtime mode (computed live at every skill invocation)

Source of truth — overrides the cache on divergence.

| Value | Trigger |
|---|---|
| `TEAMS_TMUX` | Live probe PASS: teams usable AND tmux running AND known-compatible terminal |
| `TEAMS_INPROCESS` | Live probe PASS: teams usable but tmux absent or terminal incompatible/unknown |
| `SUBAGENTS` | Live probe FAIL on any team prerequisite — legacy `Task`-tool dispatching |

### Terminal classification (heuristic, maintained by tests)

| Class | Signals | Action |
|---|---|---|
| `known-compatible` | `$TMUX` set; `$TERM_PROGRAM` ∈ {iTerm.app, WezTerm, ghostty, kitty} | allow split-pane |
| `known-incompatible` | `$VSCODE_PID` set; `$TERM_PROGRAM=vscode`; `$WT_SESSION` set | downgrade to IN_PROCESS |
| `unknown` | none of the above | downgrade to IN_PROCESS (conservative) |

These lists are heuristics updated by regression tests in `.devcontainer/tests/unit/`, NOT platform invariants.

---

## 2. Compatibility matrix

| Claude ver | env flag | tmux | Terminal | Capability | Runtime mode |
|:-:|:-:|:-:|:-:|:-:|:-:|
| < 2.1.32 | — | — | — | NONE | SUBAGENTS |
| ≥ 2.1.32 | off | — | — | NONE | SUBAGENTS |
| ≥ 2.1.32 | on | no | — | IN_PROCESS | TEAMS_INPROCESS |
| ≥ 2.1.32 | on | yes | known-compatible | TMUX | TEAMS_TMUX |
| ≥ 2.1.32 | on | yes | known-incompatible | TMUX | TEAMS_INPROCESS |
| ≥ 2.1.32 | on | yes | unknown | TMUX | TEAMS_INPROCESS |

Notice: persistent `TMUX` capability can downgrade to `TEAMS_INPROCESS` runtime if the terminal changed since install.

---

## 3. Runtime detection (canonical block)

Source `~/.claude/scripts/team-mode-primitives.sh` and call `detect_runtime_mode`. Every team-aware skill starts with:

```bash
source "$HOME/.claude/scripts/team-mode-primitives.sh"
MODE=$(detect_runtime_mode)
case "$MODE" in
    TEAMS_TMUX|TEAMS_INPROCESS) : ;;   # go to TEAMS execution
    SUBAGENTS)                  : ;;   # go to SUBAGENTS fallback
esac
```

`detect_runtime_mode` does NOT trust `.team-capability` alone. It performs a live probe:
1. Read capability file (hint)
2. Check `$CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS` (must be `"1"`)
3. Parse `claude --version` (must be ≥ 2.1.32)
4. Check `command -v tmux`
5. Call `classify_terminal`
6. Emit the final runtime mode
7. If the result disagrees with the cache, the caller MAY rewrite `.team-capability` (optional optimization)

---

## 4. Task payload contract v1 (normative)

Claude Code hook stdin does NOT carry file paths natively (verified against code.claude.com docs). The only carrier is `task_description`. Every task created for a teammate MUST embed a machine-readable contract block inside `task_description`:

### Format (JSON embedded, delimiter-tolerant)

```
<!-- task-contract v1
{
  "contract_version": 1,
  "scope": "src/auth/",
  "access_mode": "write",
  "owned_paths": ["src/auth/jwt.go", "src/auth/jwt_test.go"],
  "forbidden_paths": ["src/auth/session.go"],
  "acceptance_criteria": ["all tests pass", "no clippy warnings"],
  "output_format": "diff",
  "assignee": "reviewer-security",
  "depends_on": [],
  "idempotency_key": "review-jwt-2026-04-09-a1b2c3"
}
-->

<free-form instructions for the teammate continue here>
```

### Field rules

| Field | Type | Required | Constraint |
|---|---|---|---|
| `contract_version` | integer | ✓ | Currently `1`. Unknown versions → advisory fallback. |
| `scope` | string | ✓ | Non-empty |
| `access_mode` | enum | ✓ | `"read-only"` or `"write"` |
| `owned_paths` | array\<string\> | ✓ if `write` | When `write`, ≥ 1 entry. **Exact-match collision only** — `src/auth/` and `src/auth/jwt.go` do NOT collide. |
| `forbidden_paths` | array\<string\> | Optional | Advisory |
| `acceptance_criteria` | array\<string\> | ✓ | ≥ 1 entry |
| `output_format` | string | ✓ | Skill-defined enum (`diff`, `report`, `patch`, `summary`, …) |
| `assignee` | string | ✓ | Must match a teammate name known to the lead |
| `depends_on` | array\<string\> | Optional | Task IDs this task depends on |
| `idempotency_key` | string | Optional | Dedup key for retries |

### Parser

Single implementation in `team-mode-primitives.sh`:

```bash
extract_task_contract "$task_description" | jq -c .
```

Tolerates:
- Extra whitespace in opening marker (`<!--  task-contract  v1`)
- Any version number (`v2`, `v3` → future-proof)
- Closing `-->` on any line

Failure modes (all → advisory allow + stderr warning):
- Missing block
- Malformed JSON
- `contract_version != 1`

### `/review` contract compliance (example)

Read-only tasks are legal with empty `owned_paths`:

```json
{"contract_version":1,"scope":"PR #142","access_mode":"read-only","owned_paths":[],"acceptance_criteria":["report all security findings"],"output_format":"report","assignee":"reviewer-security","depends_on":[]}
```

The collision check skips entries with `access_mode == "read-only"`.

---

## 5. Registry lifecycle (hook-owned, local, best-effort)

**File:** `~/.claude/logs/<team-name>/task-registry.jsonl` (append-only, garbage-collected)

Written by `task-created.sh`, updated by `task-completed.sh`, read by `task-created.sh` (collision check) and `teammate-idle.sh` (pending check).

**Stability:** this file is hook-owned and 100% under our control. We do NOT read Claude Code internal paths like `~/.claude/tasks/` or `~/.claude/teams/` (documented as "auto-managed, should not be edited").

### Entry schema

```json
{
  "id": "task-001",
  "team": "my-project",
  "assignee": "reviewer-security",
  "subject": "Review JWT handling",
  "contract": true,
  "access_mode": "write",
  "owned_paths": ["src/auth/jwt.go"],
  "status": "active",
  "created_at": "2026-04-09T14:30:00Z",
  "completed_at": null,
  "idempotency_key": null
}
```

### State transitions

```
(none) ──[task-created.sh]──▶ active
active ──[task-completed.sh, exit 0]──▶ completed
active ──[task-completed.sh with {"continue":false}]──▶ abandoned
active ──[teammate-idle.sh on failure signal]──▶ failed (advisory, no block)
```

### Collision rule

A new task is rejected only when ALL of the following are true:
1. New task `access_mode == "write"`
2. Existing entry has `status == "active"` AND `access_mode == "write"`
3. Overlapping `owned_paths` (exact string match)
4. Different `assignee` (self-overlap allowed — same teammate refining)

All other cases: warning or silent pass.

### Garbage collection

On every `task-created.sh` invocation, entries older than 24h AND `status != "active"` are moved to `task-registry.archive.jsonl`. Keeps the active set small.

Uses portable epoch helpers (`epoch_now`, `epoch_from_iso`, `epoch_24h_ago`) from `team-mode-primitives.sh` — works on GNU, BSD, and busybox `date`.

---

## 6. TEAMS execution contract

When `MODE ∈ {TEAMS_TMUX, TEAMS_INPROCESS}`:

1. **Lead** creates all tasks via `TaskCreate` BEFORE spawning any teammate.
2. Every `task_description` MUST contain a valid `task-contract v1` block.
3. Lead spawns teammates by subagent type with explicit names:
   ```
   spawn teammate using developer-executor-security, name reviewer-security
   ```
4. Teammate naming is predictable and documented in the skill (see section 8).
5. Lead MUST wait for all teammates to stop (via `TeammateIdle`) before synthesizing.
6. Lead MUST call team cleanup at the end. Teammates MUST NOT call cleanup.
7. Team size hard cap: **5** teammates. Soft cap: 3 (warning beyond).
8. Tasks per teammate sweet spot: 5-6.

---

## 7. SUBAGENTS execution contract (fallback)

When `MODE == SUBAGENTS`:

1. Use the `Task` tool with `subagent_type` parameter.
2. Same agents, same prompts, same `task-contract v1` block in the prompt (even though no hook parses it here — future-compat).
3. No `SendMessage`, no shared task list, no cleanup call.
4. Output must be **functionally equivalent** to TEAMS mode:
   - Same report schema
   - Same severity semantics
   - No new user-visible sections introduced by the migration
5. The fallback path MUST be explicitly testable (`CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=0 /<skill>`).

---

## 8. Name convention (per skill)

Predictable names so users can reference them and `/audit` can track them.

| Skill | Teammate names |
|---|---|
| `/review` | `reviewer-correctness`, `reviewer-security`, `reviewer-design`, `reviewer-quality`, `reviewer-shell` |
| `/plan` | `explorer-backend`, `explorer-frontend`, `explorer-test`, `explorer-patterns` |
| `/docs` | `docs-architect`, `docs-agents`, `docs-commands`, `docs-hooks`, `docs-config`, `docs-structure`, `docs-mcp`, `docs-patterns`, `docs-languages` |
| `/do` | `impl-<language>` (picked from plan parallelization table) |
| `/infra` | `cloud-aws`, `cloud-gcp`, `cloud-azure`, `cloud-hashicorp` |
| `/test` | `test-e2e`, `test-unit`, `test-integration` |
| `/improve` | `improve-design`, `improve-quality`, `improve-security`, `improve-shell` |

Leads ask users to assign custom names via spawn prompts when the above don't fit.

---

## 9. Per-skill success criteria template

Every migrated skill MUST document its success criteria in its own file using this template:

```markdown
## Success criteria (vs legacy SUBAGENTS mode)

- **Functional equivalence**: <what must be identical>
- **Schema equivalence**: <report structure invariants>
- **Semantic equivalence**: <severity / priority semantics>
- **Fallback invariant**: <what must be unchanged in SUBAGENTS mode>
- **Performance floor** (TEAMS_TMUX): <target wall-clock multiplier>
- **Token cost ceiling**: <max multiplier vs legacy>  (skill-specific, NOT universal)
- **Stability**: <N consecutive runs without manual intervention>
- **Hook cleanliness**: <max false-positive rate>
```

Token ceilings are per-skill, not global. `/review` uses 2.5x as a pilot budget. Other skills set their own in their migration PRs.

---

## 10. Contracts & invariants

These are documented behaviors as of Claude Code 2.1.32+, verified by regression tests in `.devcontainer/tests/`. Treat them as TESTED, not as platform guarantees.

| Invariant | Verification |
|---|---|
| Hook stdin payload fields (session_id, task_id, task_subject, task_description, teammate_name, team_name) | `tests/unit/test-hook-payload.sh` |
| Runtime mode detection maps correctly for all 6 matrix rows | `tests/unit/test-capability-mapping.sh` |
| `extract_task_contract` handles valid, malformed, missing, version-mismatch cases | `tests/unit/test-parse-contract.sh` |
| Registry lifecycle transitions: active → completed / abandoned / failed | `tests/unit/test-registry-lifecycle.sh` |
| Registry GC moves old non-active entries to archive | `tests/unit/test-registry-gc.sh` |
| Terminal classification returns expected class for all known inputs | `tests/unit/test-terminal-classify.sh` |
| `super-claude` never nests tmux | `tests/unit/test-super-claude-wrap.sh` |
| `list-team-agents.sh` extracts deterministically from migrated skills | `tests/unit/test-list-team-agents.sh` |
| `version_at_least` handles prereleases correctly | `tests/unit/test-version-compare.sh` |
| Tools allowlist behavior (SendMessage/Task* available to teammates) | `tests/test-agent-teams.sh` scenario 8 (canary) |

The last one (tools allowlist) is the canary: if Claude Code changes the behavior in a future version, this test catches it before it reaches production skills.

---

## 11. Guardrails (ABSOLUTE)

- NEVER spawn more than 5 teammates without explicit user request
- NEVER spawn teammates before creating their tasks
- NEVER call cleanup from a teammate — only the lead
- NEVER rely on `~/.claude/tasks/` or `~/.claude/teams/` for enforcement (read-only best-effort)
- NEVER assume stdin payload carries file paths
- NEVER assume two teammates editing the same file is safe
- NEVER skip the live runtime probe (cache alone is insufficient)
- NEVER block a task because of an ADVISORY hook warning — only strict contract violations block
- NEVER nest `tmux` via `super-claude` (already inside `$TMUX` → run claude directly)

---

## 12. Bypass & debug flags

Flags users can set to alter behavior without editing files:

| Flag | Effect |
|---|---|
| `NO_TEAMS=true` (install.sh arg `--no-teams`) | Force `.team-capability=NONE` at install time, strip env var |
| `SUPER_CLAUDE_NO_TMUX=1` | `super-claude` never wraps with tmux for this shell session |
| `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=0` | Force SUBAGENTS mode for this shell session |
| `CLAUDE_CODE_TASK_LIST_ID=<name>` | Share a named task list across sessions (Claude Code feature) |
| `TEAM_MODE_DEBUG=1` | `team-mode-primitives.sh` logs every runtime decision to stderr |

Debug log example:
```
[team-mode] capability-read: TMUX
[team-mode] live-probe: claude=2.1.97 env=1 tmux=yes term=known-compatible
[team-mode] runtime-mode: TEAMS_TMUX
[team-mode] contract-parse: valid v1 access_mode=write paths=2
[team-mode] registry-transition: task-001 active→completed
```

---

## 13. Known platform limitations

Documented at <https://code.claude.com/docs/en/agent-teams#limitations>:

- `/resume` and `/rewind` don't restore in-process teammates; lead may message dead teammates
- Task status can lag (teammate fails to mark complete); blocks dependents
- Shutdown can be slow (teammates finish current tool call first)
- One team per session
- No nested teams
- Lead is fixed for session lifetime
- Permissions set at spawn (no per-teammate override post-spawn)
- Split-panes not supported in VS Code integrated terminal, Windows Terminal, Ghostty (as of 2026-04)

Mitigations in this template:
- `/audit` dimension surfaces capability + migrated skill count
- Conservative terminal downgrade (unknown → IN_PROCESS)
- Per-skill success criteria force reality checks before generalization

---

## 14. Reference implementation

The pilot `/review` skill is the reference implementation. When migrating a new skill, read `.devcontainer/images/.claude/commands/review/dispatch.md` first.

Implementation phases (V3.1):

```
Phase A → B1(/review pilot) → D(hooks) → C-minimal(agents) → B2..B7 → F(docs+tests)
```

Plan: `/home/vscode/.claude/plans/agent-teams-refactor-v3.md`
