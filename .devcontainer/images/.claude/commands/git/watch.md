# Watch Workflow (--watch)

**Active CI, review & code quality loop -- resolves ALL issues so `--merge` is a clean final action.**

**Flow:** `/git --commit` --> `/git --watch` --> `/git --merge`

```yaml
action_watch:
  trigger: "--watch"
  refresh_interval: 60s
  stop: "Ctrl+C ONLY (user interrupt)"

  # ─── ABSOLUTE RULE: NEVER STOP ─────────────────────────────
  # --watch runs in an infinite loop until ALL conditions are green.
  # Do NOT present "options" to the user. Do NOT suggest they retry.
  # The ONLY exit is: all green OR Ctrl+C.
  # ────────────────────────────────────────────────────────────

  # ─── PR5b — Skills Architecture v1.3: Monitor primitive ──────
  # When the Monitor primitive is `present` per PR0's primitives.json,
  # prefer event-driven streaming over polling. Polling sleep(60) is the
  # documented fallback when Monitor is `absent` or `unknown`.
  #
  # Monitor invocation:
  #   Monitor(
  #     description: "PR #{{number}} CI status",
  #     timeout_ms: 1800000,                # 30 min, restart if needed
  #     command: |
  #       prev=""
  #       while true; do
  #         cur=$(gh pr checks {{number}} --json name,bucket 2>/dev/null \
  #               || gh api repos/{{owner}}/{{repo}}/commits/{{sha}}/check-runs --jq .check_runs)
  #         comm -13 <(echo "$prev") <(echo "$cur")
  #         prev=$cur
  #         jq -e 'all(.bucket!="pending")' <<<"$cur" >/dev/null && break
  #         sleep 30
  #       done
  #   )
  #
  # Each new check transition emits one notification — no polling loop in
  # the agent's transcript, no 60s blind sleeps.

  # ─── Phase 1.0: Resolve PR/MR ───────────────────────────────
  phase_1_resolve:
    description: "Auto-detect PR/MR from current branch"
    steps:
      - detect_platform: "git remote get-url origin → github.com | gitlab.*"
      - resolve_target: "Auto-detect from current branch"
      - get_info:
          github: "mcp__github__pull_request_read(method: get)"
          gitlab: "mcp__gitlab__get_merge_request"
      - pin_commit: "Store HEAD commit SHA for tracking (re-pinned after each fix push)"
      - validate: "PR/MR must exist and be open"
      - fail_if: "No PR/MR found → error with guidance"

  # ─── Phase 2.0: Collect All Prerequisites ────────────────────
  phase_2_collect:
    description: "Gather status of all merge prerequisites"
    parallel:
      pipeline:
        description: "Get check runs / pipeline jobs (job-level, not overall)"
        github: "mcp__github__pull_request_read(method: get_status)"
        gitlab: "mcp__gitlab__list_pipelines + mcp__gitlab__list_pipeline_jobs"
        parse: "Extract individual job statuses, durations, conclusions"

      reviews:
        description: "Fetch bot review statuses"
        note: "CodeRabbit and Qodo are GitHub-only."
        sources:
          coderabbit:
            platform: "GitHub only"
            detect: "author.login == 'coderabbitai[bot]'"
            check: "APPROVED | CHANGES_REQUESTED | PENDING"
          qodo:
            platform: "GitHub only"
            detect: "author.login IN ['qodo-merge-pro[bot]', 'qodo-code-review[bot]']"
            check: "P0/P1 findings present?"

      prerequisites:
        description: "Merge readiness checks"
        github: "mergeable_state, required_reviews_count, branch_up_to_date"
        gitlab: "detailed_merge_status, approvals_left, has_conflicts"

      human_reviews:
        description: "Human reviewer status"
        action: "Flag but NEVER auto-handle"
        display: "Show status, never attempt to resolve"

  # ─── Phase 3.0: Dashboard Display ───────────────────────────
  phase_3_dashboard:
    description: "Render status dashboard after each collection"
    format: |
      ═══════════════════════════════════════════════════════════════
        /git --watch - PR #{{number}} ({{branch}})     [{{green}}/{{total}} green]
      ═══════════════════════════════════════════════════════════════

        PIPELINE      | {{job_count}} jobs | [{{progress_bar}}] {{passed}}/{{total_jobs}}
          ├─ lint     : ✓ passed (45s)
          ├─ build    : ✓ passed (1m 23s)
          ├─ test     : ⟳ running (2m 15s)
          └─ deploy   : ○ queued

        REVIEWS       | {{source_count}} sources
          ├─ CodeRabbit    : ✓ approved
          └─ Qodo          : ⚠ 1 P1 finding → fixing...

        PREREQUISITES | {{rule_count}} rules
          ├─ Required reviews (1/1) : ✓
          └─ Branch up-to-date      : ✓

        STATUS: {{green}}/{{total}} green — {{current_action}}
        Next refresh: {{countdown}}s
      ═══════════════════════════════════════════════════════════════
    symbols:
      passed: "✓"
      failed: "✗"
      running: "⟳"
      queued: "○"
      warning: "⚠"
      fixing: "→ fixing..."

  # ─── Phase 4.0: Pipeline Fix Loop (Circuit Breaker) ────────
  phase_4_pipeline_fix:
    description: "Detect pipeline failures and fix them automatically"
    circuit_breaker:
      closed:
        description: "Normal operation"
        flow: "monitor → detect issue → fix → push → wait → refresh"
        max_fix_iterations: 3
        re_pin_after_push: "After each fix push, refresh PR/MR info and update pin_commit"

      half_open:
        description: "Stall detected — no status change for >10min"
        detection:
          method: "Compare current check/review statuses with previous poll"
          trigger: "No status field changed across 10+ consecutive minutes"
          note: "NOT a hard timeout — watch CONTINUES and investigates the stall"
        actions:
          - investigate_pipeline: "Check if runner available"
          - investigate_coderabbit: "If >5min no review, post @coderabbitai review"
          - report: "Display investigation results, then CONTINUE POLLING (do NOT stop)"

      open:
        description: "Truly unresolvable issue (merge conflicts with semantic overlap)"
        action: "Escalate to user via AskUserQuestion, then CONTINUE based on answer"
        note: "NEVER exit watch silently. Ask, get answer, act on it, keep looping."

    fix_actions:
      pipeline_failure:
        steps:
          - "Analyze job logs"
          - "Identify failure type: lint | type | test | build | dependency"
          - "Apply auto-fix: lint→format, type→fix types, test→update assertions"
          - "Commit with conventional message: fix(ci): resolve {{job}} failure"
          - "Re-pin commit SHA after push"

      branch_behind:
        steps:
          - "Rebase from main/default branch"
          - "Resolve simple conflicts automatically"
          - "Escalate complex conflicts to user"

      merge_conflicts:
        steps:
          - "Attempt auto-resolve for non-overlapping changes"
          - "Escalate overlapping/semantic conflicts to user"

  # ─── Phase 4.5: Review Triage with Legitimacy Filter ────────
  #
  # THIS IS THE CORE OF --watch: fetch all review findings,
  # judge each one for legitimacy, fix legitimate issues,
  # reject illegitimate ones WITH justification, and reply
  # to each bot explaining the decision.
  #
  # --watch does ALL the review work so --merge has nothing to do.
  # ─────────────────────────────────────────────────────────────
  phase_4_5_review_triage:
    description: "Triage, judge, fix or reject ALL review findings"
    platform: "GitHub (CodeRabbit + Qodo)"
    max_iterations: 3

    # ── Step 1: Parallel Fetch (platform-conditional) ───────
    fetch:
      github_calls:
        - tool: "mcp__github__pull_request_read"
          params: { method: "get_review_comments" }
          captures: "inline_comments (CodeRabbit + Qodo + Human threads)"
          note: |
            get_review_comments returns thread metadata with isResolved,
            isOutdated, isCollapsed. These flags drive the relevance
            filter in step 2 — do not drop them on parse.
        - tool: "mcp__github__pull_request_read"
          params: { method: "get_comments" }
          captures: "issue_comments (CodeRabbit summary)"
      gitlab_calls:
        - tool: "mcp__gitlab__list_merge_request_notes"
          captures: "mr_notes (human + bot comments)"
        - tool: "mcp__gitlab__list_merge_request_discussions"
          captures: "mr_discussions (unresolved threads)"
    # ── Step 2: Classify by Source (hard filter on isResolved) ─
    # WHY (v1.5 patch): if the user resolved a thread manually in the
    # GitHub UI between two iterations, re-fetching MUST treat that
    # thread as out-of-scope. Otherwise we re-fix what the user just
    # marked done, churning the PR and confusing the bot.
    classify:
      coderabbit:
        detect: "author.login == 'coderabbitai[bot]'"
        relevant: |
          jq filter applied to inline_comments threads:
            select(.isResolved == false and .isOutdated == false)
          (Any thread where isResolved is true — by us or by the user
          in the UI — is dropped from the relevant set.)
      qodo:
        detect: "author.login IN ['qodo-merge-pro[bot]', 'qodo-code-review[bot]'] AND P0/P1"
        relevant: |
          P0 or P1 only (P2 ignored) AND
          select(.isResolved == false and .isOutdated == false)
      human:
        detect: "is_bot=false"
        action: "NEVER auto-handle — flag to user only"

    # ── Step 3: Legitimacy Filter (CRITICAL) ────────────────
    #
    # Before fixing ANY finding, judge whether it is LEGITIMATE
    # or ILLEGITIMATE. This prevents regressions from blindly
    # applying bot suggestions.
    #
    # ILLEGITIMATE findings (REJECT with justification):
    #   - Downgrade language/tool version (e.g., "use Go 1.21" when project uses 1.26)
    #   - Remove a feature or capability the project intentionally provides
    #   - Change architecture in a way that contradicts CLAUDE.md or project conventions
    #   - Suggest patterns incompatible with the project's stack
    #   - Style preferences that contradict existing codebase conventions
    #   - False positives (rule doesn't apply to this context)
    #
    # LEGITIMATE findings (FIX):
    #   - Real bugs (null pointer, off-by-one, race condition)
    #   - Security vulnerabilities (injection, XSS, hardcoded secrets)
    #   - Unused imports/variables
    #   - Missing error handling
    #   - Performance issues with clear fix
    #   - Documentation/typo fixes
    #   - Actual code quality improvements that align with project conventions
    #
    legitimacy_filter:
      for_each_finding:
        1_read_context: "Read the affected file + surrounding code"
        2_check_project_rules: "Consult CLAUDE.md, language RULES.md, and conventions"
        3_judge: |
          Classify as:
            LEGITIMATE   → real issue, should be fixed
            ILLEGITIMATE → contradicts project, would cause regression
            UNCLEAR      → needs more context → ask user via AskUserQuestion before acting
        4_record_decision: "Store verdict + justification for each finding"

    # ── Step 4: Fix Legitimate Findings ─────────────────────
    fix_loop:
      flow: |
        WHILE relevant_count > 0 AND iteration < max_iterations:
          1. Separate findings into LEGITIMATE vs ILLEGITIMATE
          2. Fix all LEGITIMATE findings (code changes)
          3. Commit: "fix(review): address {source} findings"
          4. Push to branch
          5. Respond to bots (see interaction below)
          6. Wait for re-reviews (max 120s)
          7. Re-fetch and re-classify
          8. Check: relevant_count == 0?

    # ── Step 5: Respond to Bots (MANDATORY) ─────────────────
    #
    # Every finding gets a response. No silent dismissals.
    #
    bot_interaction:
      coderabbit:
        legitimate_fixed:
          action: |
            1. Apply fixes + commit + push
            2. Post issue comment: "@coderabbitai review" (trigger re-review)
            3. Sleep 120s, then re-fetch threads
            4. CONTINUE POLLING — do NOT stop

        illegitimate_rejected:
          action: |
            1. Post justification as issue comment (NOT thread reply — thread replies fail with 422)
            2. Dismiss the CHANGES_REQUESTED state via:
               mcp__github__pull_request_review_write(method: create, event: COMMENT,
                 body: "Findings triaged: N fixed, M rejected with justification. See PR comments.")
               This creates a new COMMENT review that supersedes the CHANGES_REQUESTED state.
            3. Resolve each rejected thread at the thread level (v1.5 patch):
               for each rejected_thread_id in $rejected_threads; do
                 gh api graphql -F threadId="$rejected_thread_id" -f query='
                   mutation Resolve($threadId: ID!) {
                     resolveReviewThread(input: {threadId: $threadId}) {
                       thread { id isResolved }
                     }
                   }' \
                 || true  # don't abort the loop on a single thread failure
               done
               # WHY: dismissing the review at PR level (step 2) doesn't
               # change the per-thread isResolved flag in the GitHub UI.
               # Without GraphQL resolveReviewThread, the red dots stay
               # next to each rejected comment and the user has to click
               # through them manually. graphql does what the @coderabbitai
               # resolve magic-string can't.
            4. Post "@coderabbitai resolve" in same comment (belt + suspenders;
               the GraphQL mutation in step 3 is what actually clears the dots)
            5. CONTINUE POLLING — do NOT stop

        # CRITICAL: @coderabbitai resolve in issue comments does NOT resolve
        # individual review threads. It only works as a bot command.
        # graphql resolveReviewThread (step 3 above) is the authoritative
        # mechanism. The review-level dismiss in step 2 is a separate axis:
        # it overrides CHANGES_REQUESTED → COMMENT at the review object.
        stall_recovery:
          description: "CodeRabbit status stuck at 'pending' for >5min"
          action: |
            1. Post "@coderabbitai review" as issue comment
            2. Sleep 120s
            3. If still pending: the status is a CodeRabbit backend issue
            4. Check if ALL threads are resolved (is_resolved: true)
            5. If all resolved: proceed as if review passed (ignore stuck status)
            6. NEVER stop watching because of a stuck CodeRabbit status

      qodo:
        legitimate_fixed:
          action: "Fix + push (Qodo auto-re-reviews on push)"
        illegitimate_rejected:
          action: |
            1. Post a reply on the Qodo comment thread:
                 "P{level} finding acknowledged but rejected:
                  [REASON — e.g., this pattern is intentional for performance].
                  Not a regression — consistent with project design."
            2. Resolve the thread at the thread level (v1.5 patch):
               gh api graphql -F threadId="$qodo_thread_id" -f query='
                 mutation Resolve($threadId: ID!) {
                   resolveReviewThread(input: {threadId: $threadId}) {
                     thread { id isResolved }
                   }
                 }' || true
               # Same rationale as CodeRabbit — Qodo threads also need
               # explicit resolve to clear the red dots.

      human:
        action: "NEVER auto-handle. Display to user and wait."

    # ── Step 6: Escalation ──────────────────────────────────
    escalation:
      condition: "iteration >= max_iterations AND relevant_count > 0"
      action: |
        Present remaining findings to user:
          Option 1: "Continue fixing (raise iteration limit)"
          Option 2: "Force proceed (override remaining findings)"
          Option 3: "Abort watch"

  # ─── Phase 5.0: Exit Conditions ─────────────────────────────
  phase_5_exit:
    all_green:
      condition: "Pipeline passed + All reviews satisfied + Prerequisites met"
      action: "Display final dashboard"
      message: "All prerequisites green — ready for /git --merge"
      display_format: |
        ═══════════════════════════════════════════════════════════════
          ✓ All prerequisites green — PR #{{number}} ready to merge
        ═══════════════════════════════════════════════════════════════
          PIPELINE      : ✓ All {{job_count}} jobs passed
          REVIEWS       : ✓ All satisfied ({{fixed}} fixed, {{rejected}} rejected with justification)
          PREREQUISITES : ✓ All met
          Duration      : {{elapsed}}

          Next step: /git --merge
        ═══════════════════════════════════════════════════════════════

    user_interrupt:
      action: "Display current state, exit cleanly"
      message: "Watch interrupted — current state displayed above"
      note: "This is the ONLY valid exit besides all_green"

    # FORBIDDEN EXIT PATTERNS:
    #   - "You can either: 1. Wait 2. Proceed" → NEVER present options, just keep going
    #   - "Run /git --watch to fix" → you ARE --watch, keep looping
    #   - "CodeRabbit stalled, stopping" → sleep and retry, never stop
    #   - "Escalating to user" → use AskUserQuestion, then act on answer and CONTINUE
```
