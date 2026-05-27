# Merge Workflow (--merge)

**Final verification and merge. If not immediately mergeable, enable auto-merge and enter watch loop.**

**Flow:** `/git --merge` → if ready: merge now | if blocked: enable auto-merge + enter --watch loop until merged

**CRITICAL RULE: --merge NEVER stops and asks the user to retry.**
If not mergeable now → enable auto-merge → enter --watch loop → keep going until merged or Ctrl+C.

---

## MCP-ONLY Policy (STRICT - Issue #142)

**NEVER use CLI for pipeline status. Always use MCP tools:**

```yaml
mcp_only_policy:
  MANDATORY:
    github:
      pipeline_status: "mcp__github__pull_request_read"
      check_runs: "mcp__github__list_check_runs (via pull_request_read)"
    gitlab:
      pipeline_status: "mcp__gitlab__list_pipelines"
      pipeline_jobs: "mcp__gitlab__list_pipeline_jobs"

  FORBIDDEN:
    - "gh pr checks"
    - "gh run view"
    - "gh pr merge"             # PR1 — Skills Architecture v1.3 (use mcp__github__merge_pull_request)
    - "glab ci status"
    - "glab ci view"
    - "glab mr merge"           # PR1 — use mcp__gitlab__merge_merge_request when GitLab remote
    - "curl api.github.com"
    - "curl gitlab.com/api"
```

### Merge call (PR1 — MCP-only, fix per v1.3 plan §4)

```yaml
merge_call:
  github:
    tool: "mcp__github__merge_pull_request"
    args:
      owner: "<from remote>"
      repo: "<from remote>"
      pullNumber: "<resolved>"
      merge_method: "squash"     # or merge / rebase per project policy
    on_failure: "raise — do NOT silently fall back to gh pr merge"
  gitlab:
    tool: "mcp__gitlab__merge_merge_request"
    on_failure: "raise — no glab fallback"
```

On `PushNotification` primitive `present` (per PR0 probe), emit one
`Merged <repo>#<pr> via <merge_method>` notification post-merge.

---

## Phase 1.0: Peek + Commit-Pinned Tracking

**CRITICAL: Track pipeline for SPECIFIC commit SHA**

```yaml
peek_workflow:
  0_get_pushed_commit:
    action: "Get SHA of just-pushed commit"
    command: "git rev-parse HEAD"
    store: "pushed_commit_sha"

  1_pr_mr_info:
    action: "Retrieve PR/MR info"
    tools:
      github: mcp__github__pull_request_read
      gitlab: mcp__gitlab__get_merge_request
    verify: "head_sha == pushed_commit_sha"

  2_find_pipeline:
    action: "Find pipeline triggered by THIS commit"
    github: "mcp__github__pull_request_read(method='get') → verify head_sha"
    gitlab: "mcp__gitlab__list_pipelines(sha=pushed_commit_sha)"

  3_validate_pipeline:
    action: "Abort if pipeline not found within 60s"
    timeout: 60s
```

---

## Phase 2.0: Job-Level Status Parsing (CRITICAL)

**Parse EACH job individually, not overall status:**

```yaml
status_parsing:
  github:
    statuses:
      success: ["success", "neutral"]
      pending: ["queued", "in_progress", "waiting", "pending"]
      failure: ["failure", "action_required", "timed_out"]
      cancelled: ["cancelled", "stale"]
      skipped: ["skipped"]

    aggregation_rule: |
      pipeline_success = ALL jobs in [success, skipped, neutral]
      pipeline_failure = ANY job in [failure, cancelled, timed_out]
      pipeline_pending = ANY job in [pending, queued, in_progress]

  gitlab:
    statuses:
      success: ["success", "manual"]
      pending: ["created", "waiting_for_resource", "preparing", "pending", "running"]
      failure: ["failed"]
      cancelled: ["canceled"]
      skipped: ["skipped"]
```

---

## Phase 3.0: CI Monitoring with Exponential Backoff and Hard Timeout

**ABSOLUTE LIMIT: 10 minutes / 30 polls**

```yaml
ci_monitoring:
  config:
    initial_interval: 10s
    max_interval: 120s
    backoff_multiplier: 1.5
    jitter_percent: 20
    timeout: 600s
    max_poll_attempts: 30

  on_timeout:
    action: "ABORT immediately"
    message: "Pipeline timeout after 10 minutes — check manually"
```

---

## Phase 3.5: Auto-Merge Enablement (NEW)

**If not immediately mergeable, enable auto-merge and enter watch loop.**

```yaml
auto_merge:
  description: "Enable GitHub auto-merge so PR merges automatically when all checks pass"
  trigger: "Any readiness check fails during Phase 4.0"
  workflow:
    1_enable_auto_merge:
      github: "gh pr merge {{number}} --auto --squash"
      note: "This enables auto-merge — GitHub will merge when all status checks pass"
    2_enter_watch_loop:
      action: "Execute full --watch workflow (Phase 4.5 review triage included)"
      note: "Fix findings, wait for CI, dismiss stale reviews — NEVER stop"
    3_monitor_merge:
      action: "After watch reports all green, poll PR state every 60s"
      condition: "PR merged == true"
      on_success: "Proceed to Phase 5.0 cleanup"
      on_timeout: "Keep polling — auto-merge will handle it"

  # ABSOLUTE RULE: --merge never returns to user with "BLOCKED".
  # It enables auto-merge and enters watch loop until merge happens.
```

---

## Phase 4.0: Final Readiness Verification (MANDATORY)

**--merge does NOT fix anything. It verifies readiness. If blocked → Phase 3.5 auto-merge.**

```yaml
readiness_checks:
  description: "Verify everything is mergeable — abort if any check fails"

  # ── Check 1: All CI jobs passed ────────────────────────────
  ci_passed:
    github: "mcp__github__pull_request_read(method: get_status)"
    gitlab: "mcp__gitlab__list_pipelines + mcp__gitlab__list_pipeline_jobs"
    condition: "ALL jobs in [success, skipped, neutral]"
    on_fail: "ABORT — pipeline has failed jobs, run /git --watch to fix"

  # ── Check 2: No unresolved review findings ────────────────
  reviews_clear:
    parallel:
      - tool_github: "mcp__github__pull_request_read(method: get_reviews)"
        tool_gitlab: "mcp__gitlab__list_merge_request_notes — check for unresolved threads"
        condition: "No CHANGES_REQUESTED from bots (coderabbit, qodo on GitHub; unresolved threads on GitLab)"
    on_fail: "ABORT — unresolved review findings, run /git --watch to fix"

  # ── Check 3: No secrets in diff ────────────────────────────
  secrets_scan:
    action: "Scan full PR diff for secrets"
    patterns:
      - "(?i)(api[_-]?key|secret|token|password|credential)\\s*[:=]\\s*['\"][^'\"]{8,}"
      - "(?i)-----BEGIN (RSA |EC |DSA )?PRIVATE KEY-----"
      - "(?i)AWS[_-]?(ACCESS|SECRET)[_-]?KEY"
      - "ghp_[a-zA-Z0-9]{36}"
      - "glpat-[a-zA-Z0-9\\-]{20}"
      - "op_[a-zA-Z0-9]{43}"
    tool_github: "mcp__github__pull_request_read(method: get_diff)"
    tool_gitlab: "mcp__gitlab__get_merge_request_changes"
    on_match: "ABORT — potential secret detected in diff: {{match}}"

  # ── Check 4: PR/MR title/body matches actual changes ──────
  pr_conformity:
    action: "Verify PR/MR title follows conventional commit format and reflects the code"
    steps:
      1_get_pr:
        github: "mcp__github__pull_request_read(method: get)"
        gitlab: "mcp__gitlab__get_merge_request"
      2_get_files:
        github: "mcp__github__pull_request_read(method: get_files)"
        gitlab: "mcp__gitlab__get_merge_request_changes"
      3_get_commits: "git log $({ git symbolic-ref refs/remotes/origin/HEAD --short 2>/dev/null || echo origin/main; } | sed 's|origin/||')..HEAD --oneline"
      4_validate_title: |
        Title MUST match: <type>(<scope>): <summary>
        Type MUST match actual changes:
          - feat: if new functionality added
          - fix: if bug fixed
          - refactor: if code restructured
          - docs: if only docs changed
          - chore: if tooling/config changed
      5_validate_body: |
        Body MUST contain:
          - ## Summary (with bullet points)
          - ## Test plan (with checklist)
        Body MUST NOT be stale (reference files/features not in the diff)
      6_regenerate_if_needed: |
        IF title or body don't match:
          Regenerate from final branch state
          Update via mcp__github__update_pull_request (GitHub) or glab mr update (GitLab)
          Log: "PR/MR description regenerated to match final code"

  # ── Check 5: Mergeable state ──────────────────────────────
  mergeable:
    github: "mergeable_state != 'dirty' AND mergeable != false"
    gitlab: "detailed_merge_status == 'mergeable'"
    on_fail: "ABORT — merge conflicts or missing approvals"

  # ── Check 6: Branch up to date ────────────────────────────
  branch_fresh:
    action: "Verify branch is not behind main"
    command: "git fetch && git merge-base --is-ancestor origin/$({ git symbolic-ref refs/remotes/origin/HEAD --short 2>/dev/null || echo origin/main; } | sed 's|origin/||') HEAD"
    on_fail: "ABORT — branch behind main, run /git --watch to rebase"
```

**Output Phase 4.0:**

```text
═══════════════════════════════════════════════════════════════
  /git --merge - Readiness Verification
═══════════════════════════════════════════════════════════════

  ✓ CI: All 4 jobs passed
  ✓ Reviews: No unresolved findings
  ✓ Secrets: No secrets in diff
  ✓ PR title: feat(build): add resilient GitHub API version resolution
  ✓ Mergeable: Yes
  ✓ Branch: Up to date with main

  All checks passed — proceeding to merge...

═══════════════════════════════════════════════════════════════
```

**Output Phase 4.0 (NOT YET READY → auto-merge + watch):**

```text
═══════════════════════════════════════════════════════════════
  /git --merge — Not immediately mergeable
═══════════════════════════════════════════════════════════════

  ✓ CI: All 4 jobs passed
  ✗ Reviews: 2 unresolved CodeRabbit findings
  ✓ Secrets: No secrets in diff
  ✓ PR title: feat(build): ...
  ✗ Mergeable: blocked

  → Enabling auto-merge (squash)...
  → Entering --watch loop to resolve remaining issues...
  → Will merge automatically when all checks pass.

═══════════════════════════════════════════════════════════════
```

**FORBIDDEN output — NEVER do this:**
```text
  BLOCKED: ...
  Run /git --watch to fix remaining issues.
  ← WRONG: --merge must handle this itself, not ask the user
```

---

## Phase 5.0: Merge & Cleanup

```yaml
merge_workflow:
  1_pre_merge_test:
    action: "Test merge result BEFORE actual merge"
    commands:
      - "DEFAULT_BRANCH=$({ git symbolic-ref refs/remotes/origin/HEAD --short 2>/dev/null || echo origin/main; } | sed 's|origin/||')"
      - "git fetch origin $DEFAULT_BRANCH"
      - "git merge origin/$DEFAULT_BRANCH --no-commit --no-ff"
      - "{test_command}"
      - "git merge --abort"
    on_failure: "ABORT merge, report test failures"

  2_merge:
    tools:
      github: mcp__github__merge_pull_request
      gitlab: mcp__gitlab__merge_merge_request
    method: "squash"

  3_cleanup:
    actions:
      - "git push origin --delete <branch>"
      - "git branch -D <branch>"
      - "git checkout $DEFAULT_BRANCH"
      - "git pull origin $DEFAULT_BRANCH"
```

**Final Output (GitHub):**

```text
═══════════════════════════════════════════════════════════════
  ✓ PR #42 merged successfully
═══════════════════════════════════════════════════════════════

  Branch  : feat/add-auth → main
  Method  : squash
  CI      : ✓ All jobs passed
  Reviews : ✓ All satisfied
  Secrets : ✓ Clean

  Cleanup:
    ✓ Remote branch deleted
    ✓ Local branch deleted
    ✓ Switched to main
    ✓ Pulled latest

═══════════════════════════════════════════════════════════════
```

**Final Output (GitLab):**

```text
═══════════════════════════════════════════════════════════════
  ✓ MR !42 merged successfully
═══════════════════════════════════════════════════════════════

  Branch  : feat/add-auth → main
  Method  : squash
  Pipeline: ✓ Passed
  Cleanup : ✓ Complete

═══════════════════════════════════════════════════════════════
```
