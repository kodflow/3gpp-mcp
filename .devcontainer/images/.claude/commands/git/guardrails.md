# Guardrails & Safety Rules

Reference document for all safety rules, conventions, and forbidden actions across /git workflows.

---

## Conventional Commits

| Type | Usage |
|------|-------|
| `feat` | New feature |
| `fix` | Bug fix |
| `refactor` | Refactoring |
| `docs` | Documentation |
| `test` | Tests |
| `chore` | Maintenance |
| `ci` | CI/CD |

---

## Guardrails (ABSOLUTE)

| Action | Status | Reason |
|--------|--------|--------|
| Skip Phase 0.5 (Identity) without flag | **FORBIDDEN** | Git identity required |
| Skip Phase 1 (Peek) | **FORBIDDEN** | git status before action |
| Skip Phase 6.0 (Context) | **FORBIDDEN** | CLAUDE.md must reflect changes |
| Skip Phase 5.5 (PR Regeneration) | **FORBIDDEN** | PR must reflect final changes |
| Skip Phase 2 (CI Polling) | **FORBIDDEN** | CI validation mandatory |
| Automatic merge without CI | **FORBIDDEN** | Code quality |
| Push to main/master | **FORBIDDEN** | Protected branch |
| Force merge if CI fails x3 | **FORBIDDEN** | Attempt limit |
| Push without --force-with-lease | **FORBIDDEN** | Safety |
| AI mentions in commits | **FORBIDDEN** | Discretion |
| Commit without validated identity | **FORBIDDEN** | Traceability |
| CLI for CI status | **FORBIDDEN** | MCP-ONLY policy |
| Report success if ANY job failed | **FORBIDDEN** | Job-level parsing |
| Wait > 10 min for pipeline (--merge) | **FORBIDDEN** | Hard timeout (--merge only; --watch uses stall detection instead) |
| Monitor wrong commit's pipeline | **FORBIDDEN** | Commit-pinned tracking |

---

## Review Triage Safeguards (Phase 3.5)

| Action | Status | Reason |
|--------|--------|--------|
| Auto-resolve human comments | **FORBIDDEN** | Only humans resolve their own |
| Skip Phase 3.5 entirely | **ALLOWED** | If 0 review comments exist or --skip-review |
| More than 3 fix iterations | **FORBIDDEN** | Escalate to user |
| Auto-dismiss CodeRabbit without fixing | **FORBIDDEN** | Must fix or justify |
| Auto-dismiss Qodo P0/P1 without fixing | **FORBIDDEN** | Must address blockers |
| Post `@coderabbitai resolve` before fixes applied | **FORBIDDEN** | Resolve only after fixing |

---

## Auto-fix Safeguards

| Action | Status | Reason |
|--------|--------|--------|
| Auto-fix security vulnerabilities | **FORBIDDEN** | Human review required |
| Merge with CRITICAL issues | **FORBIDDEN** | Security first |
| Circular fix (same error 3x) | **FORBIDDEN** | Prevents infinite loop |
| Modify .claude/ via auto-fix | **FORBIDDEN** | Protected config |
| Modify .devcontainer/ via auto-fix | **FORBIDDEN** | Protected config |
| Auto-fix without commit message | **FORBIDDEN** | Traceability |

---

## Commit message guardrails — workflow-internal leak protection (#369)

PR1 — Skills Architecture v1.3 documents the patterns blocked by
`git-guard.sh` (PreToolUse hook) and `.githooks/commit-msg` (git-native).
Both layers reject commits whose message body or subject matches any of:

| Pattern (case-insensitive) | Why it's blocked |
|---|---|
| `.claude/` path reference | Workflow internals leak into commit history |
| `^\s*plan:` footer | `Plan:` convention reveals internal planning workflow |
| `see/ref/tracked-in .claude…` | Reverse leak through cross-references |
| AI attribution lines (#358) | Co-Authored-By: Claude/AI/GPT/etc — see CLAUDE.md §4 |
| `🤖` emoji | Robot emoji signals AI authorship |
| `claude code` / `claude-code` literal | Same as above, literal form |

False positives are avoided by anchored regexes: the body keyword
"plan ahead" or "the plan is" remains legal because the patterns require
`plan:` to appear at start-of-line or `plan` to be followed by literal
`.claude` path.

Bypassing via `--no-verify` or `-n` is also blocked (per CLAUDE.md §4).
If a legitimate use case surfaces (e.g. backfilling history with
upstream-published artifacts), open an issue rather than weakening the
regex.

---

## Auto-fix Timeouts

| Element | Value | Reason |
|---------|-------|--------|
| CI Polling total | 600s (10min) | Prevent infinite wait |
| Per fix attempt | 120s (2min) | Prevent blocking |
| Cooldown between attempts | 30s | Allow CI to start |
| Polling jitter | ±20% | Prevent thundering herd |

---

## Watch Safeguards

| Action | Status | Reason |
|--------|--------|--------|
| Auto-merge when all green | **FORBIDDEN** | Watch is monitor+fix only, merge is explicit |
| Skip dashboard display | **FORBIDDEN** | User must see progress |
| Auto-resolve human reviews | **FORBIDDEN** | Only humans resolve their own |
| Watch without PR/MR | **FORBIDDEN** | Must have a target to monitor |
| Fix without commit message | **FORBIDDEN** | Traceability |
| Ignore stalled check >10min | **FORBIDDEN** | Must investigate root cause |

---

## CLI Commands FORBIDDEN for CI Monitoring

```yaml
forbidden_cli:
  github:
    - "gh pr checks"
    - "gh run view"
    - "gh run list"
    - "gh api repos/.../check-runs"
  gitlab:
    - "glab ci status"
    - "glab ci view"
    - "glab pipeline status"
  generic:
    - "curl *api.github.com*"
    - "curl *gitlab.com/api*"

required_mcp:
  github: "mcp__github__pull_request_read"
  gitlab: "mcp__gitlab__list_pipelines, mcp__gitlab__list_pipeline_jobs"
```

---

## Legitimate Parallelization

| Element | Parallel? | Reason |
|---------|-----------|--------|
| Pre-commit checks (lint+test+build) | Parallel | Independent |
| Language checks (Go+Rust+Node) | Parallel | Independent |
| CI polling + conflict check | Parallel | Independent |
| Git operations (branch→commit→push→PR) | Sequential | Dependency chain |
| Auto-fix attempts | Sequential | Depends on CI result |
| CI checks waiting | Sequential | Wait for result |
| Pipeline polling | Sequential | State changes between polls |
