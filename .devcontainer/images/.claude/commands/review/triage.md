# Finding Classification (Phases 4-5, 10-11)

## Phase 4.0: Auto-Describe (PR-Agent inspired)

**Generate description if PR/MR is empty or insufficient:**

```yaml
auto_describe:
  trigger:
    - "pr_mr_body is empty OR pr_mr_body.length < 50"
    - "pr_mr_body contains only template placeholders"
    - "--describe flag passed"

  skip_if:
    - "pr_mr_body.length >= 200 AND contains_summary_section"
    - "mode == 'local' (no PR/MR)"

  workflow:
    1_analyze_diff:
      inputs:
        github: "mcp__github__pull_request_read (method: get_files)"
        gitlab: "mcp__gitlab__get_merge_request_changes"
        common:
          - "git diff --stat"
          - "git log --oneline {base}..HEAD"
      extract:
        main_changes: [string]  # Max 5 key changes
        breaking_changes: [string]
        new_features: [string]
        bug_fixes: [string]
        refactors: [string]

    2_generate_description:
      format: |
        ## Summary
        {1-2 sentence overview based on commit messages + diff}

        ## Changes
        {bulleted list of main_changes, max 5}

        ## Type
        - [ ] Feature
        - [ ] Bug fix
        - [ ] Refactor
        - [ ] Documentation
        - [ ] Configuration

        ## Checklist
        - [ ] Tests added/updated
        - [ ] Documentation updated (if needed)
        - [ ] No breaking changes (or documented below)

      constraints:
        max_length: 1000
        no_code_blocks_in_summary: true
        no_file_by_file_description: true  # Avoid verbose output

    3_user_validation:
      tool: AskUserQuestion
      prompt: |
        Description generated for {PR|MR} #{pr_mr_number}:

        {generated_description}

        Action?
      options:
        - label: "Post"
          description: "Update the description"
        - label: "Edit"
          description: "Modify before posting"
        - label: "Ignore"
          description: "Do not modify"

    4_update_pr_mr:
      condition: "user_choice in ['Post', 'Edit']"
      tools:
        github: "gh pr edit {pr_number} --body '{final_description}'"
        gitlab: "glab mr update {mr_number} --description '{final_description}'"
      fallback:
        github: "mcp__github__update_pull_request (body: final_description)"
        gitlab: "mcp__gitlab__update_merge_request (description: final_description)"
```

**Output Phase 1.5:**

```
═══════════════════════════════════════════════════════════════
  /review - Auto-Describe
═══════════════════════════════════════════════════════════════

  PR Description: EMPTY (0 chars)
  Action: Generate description

  Generated:
    ## Summary
    Add SessionStart hook to restore context after compaction.

    ## Changes
    - Add post-compact.sh script for context restoration
    - Update settings.json with SessionStart hook config
    - Add hook documentation in CLAUDE.md

    ## Type: Feature

  Status: Waiting for user validation...

═══════════════════════════════════════════════════════════════
```

---

## Phase 5.0: Feedback Collection

**Collect feedback with budget and prioritization:**

```yaml
feedback_collection:
  1_fetch:
    tools:
      github:
        - "mcp__github__pull_request_read (method: get_reviews)"
        - "mcp__github__pull_request_read (method: get_comments)"
      gitlab:
        - "mcp__gitlab__list_merge_request_notes"
        - "mcp__gitlab__list_merge_request_discussions"

  2_budget_filter:
    rule: |
      IF count(all_feedback) > 80:
        filter = "unresolved + modified_lines_only"
      ELSE:
        filter = "all"

  3_classify:
    method: |
      FOR each feedback:
        IF author.type == "Bot" OR author.login ends with "[bot]":
          category = "ai_review"
        ELSE:
          category = "human_review"

        IF body contains "?" AND NOT suggestion_code:
          type = "question"
        ELSE IF suggestion_code != null:
          type = "suggestion"
        ELSE:
          type = "comment"

  4_prioritize:
    order:
      1: "unresolved human reviews"
      2: "questions (need response)"
      3: "suggestions on modified lines"
      4: "ai reviews (behavior extraction)"
      5: "resolved/outdated"
```

**Classification output:**

| Category | Type | Count | Priority |
|----------|------|-------|----------|
| Human | Question | 2 | HIGH |
| Human | Comment | 3 | MEDIUM |
| AI (qodo) | Suggestion | 3 | EXTRACT |

---

## Phase 6.0: CI Diagnostics (Conditional)

**Extract actionable signal from CI failures:**

```yaml
ci_diagnostics:
  trigger: "on_pr_mr == true AND ci_status in ['failing', 'pending']"

  goal: "Extract exploitable signal from CI failures without noise"

  tools:
    github:
      - "mcp__github__get_workflow_run_logs"
      - "gh run view --log-failed"
    gitlab:
      - "mcp__gitlab__get_pipeline_jobs"
      - "glab ci trace"

  extract:
    failing_jobs: [{name, conclusion, url}]
    top_errors: [string]  # max 5 representative lines
    affected_files: [string]
    error_categories:
      - "build_error"
      - "test_failure"
      - "lint_error"
      - "security_scan"
      - "timeout"

  output:
    ci_first_section: |
      IF failing:
        Prepend review with CI-First section
        Focus analysis on affected_files first

    rule: |
      IF ci_status == "failing":
        priority = ["fix CI errors", "then review rest"]
        inject_ci_context = true
      IF ci_status == "pending":
        warning = "CI still running, results may change"
```

---

## Phase 7.0: Question Handling

**Prepare answers for human questions:**

```yaml
question_handling:
  rule_absolute: "NEVER mention AI/Claude/LLM in answers"

  forbidden_phrases:
    - "Claude", "AI", "assistant", "LLM"
    - "I was generated", "automatically generated"
    - "artificial intelligence suggests"

  workflow:
    1_collect: "Extract questions from human reviews"
    2_prepare: |
      FOR each question:
        answer = generate_answer(question, context)
        validate: no_forbidden_phrases(answer)

    3_present:
      format: |
        ## Question by {author}
        > {question_text}

        **Proposed answer:**
        {answer}

        [Post / Edit / Skip]

    4_user_validates: "AskUserQuestion before posting"
    5_post:
      github_inline:
        preferred: "gh api repos/{owner}/{repo}/pulls/{pr_number}/comments/{comment_id}/replies -f body='{answer}'"
        fallback: "mcp__github__add_issue_comment"
      gitlab: "mcp__gitlab__create_merge_request_note (if validated)"
```

---

## Phase 10.0: Parallel Analysis (5 AGENTS)

@.devcontainer/images/.claude/commands/shared/team-mode.md

**Branch on runtime mode** (from review.md Execution Mode Detection):

```bash
source "$HOME/.claude/scripts/team-mode-primitives.sh"
MODE=$(detect_runtime_mode)
```

- `TEAMS_TMUX` or `TEAMS_INPROCESS` → run **Phase 10.0 TEAMS execution** below
- `SUBAGENTS` → run **Phase 10.0 SUBAGENTS execution (fallback)** further down

---

### Phase 10.0 TEAMS execution (Agent Teams mode)

**Step 1 — Create 5 tasks via `TaskCreate`**, each with a task-contract v1 block embedded in `task_description`. All 5 are read-only (no file writes) so `access_mode: "read-only"` and `owned_paths: []` is legitimate:

```text
Task 1 (correctness):
  subject: Review diff for correctness
  description:
    <!-- task-contract v1
    {"contract_version":1,"scope":"review-diff","access_mode":"read-only","owned_paths":[],"acceptance_criteria":["return JSON findings array","apply Correctness Oracle Framework","include file:line and severity"],"output_format":"report","assignee":"reviewer-correctness","depends_on":[]}
    -->
    Review the diff for correctness: algorithmic errors, invariants,
    state machines, concurrency, silent failures, ordering.

Task 2 (security):
  subject: Review diff for security
  description:
    <!-- task-contract v1
    {"contract_version":1,"scope":"review-diff","access_mode":"read-only","owned_paths":[],"acceptance_criteria":["OWASP Top 10","taint analysis source→sink","CWE references"],"output_format":"report","assignee":"reviewer-security","depends_on":[]}
    -->
    Security review: OWASP, injection, secrets, crypto, supply chain.

Task 3 (design):
  subject: Review diff for design
  description:
    <!-- task-contract v1
    {"contract_version":1,"scope":"review-diff","access_mode":"read-only","owned_paths":[],"acceptance_criteria":["antipatterns","DDD/SOLID violations","layering"],"output_format":"report","assignee":"reviewer-design","depends_on":[]}
    -->
    Design review: patterns, DDD, SOLID, layering, architecture drift.

Task 4 (quality):
  subject: Review diff for quality
  description:
    <!-- task-contract v1
    {"contract_version":1,"scope":"review-diff","access_mode":"read-only","owned_paths":[],"acceptance_criteria":["complexity","smells","style","maintainability"],"output_format":"report","assignee":"reviewer-quality","depends_on":[]}
    -->
    Quality review: complexity, duplication, style, DTO conventions.

Task 5 (shell):
  subject: Review diff for shell/CI safety
  description:
    <!-- task-contract v1
    {"contract_version":1,"scope":"review-diff","access_mode":"read-only","owned_paths":[],"acceptance_criteria":["shell pitfalls","Dockerfile","CI/CD risks"],"output_format":"report","assignee":"reviewer-shell","depends_on":[]}
    -->
    Shell/Dockerfile/CI review: dangerous patterns, missing safeguards.
```

**Step 2 — Spawn 5 teammates** (natural language to Claude Code):

```
Create an agent team for this review. Spawn 5 teammates and have each claim its task:
- use developer-executor-correctness, name reviewer-correctness
- use developer-executor-security, name reviewer-security
- use developer-executor-design, name reviewer-design
- use developer-executor-quality, name reviewer-quality
- use developer-executor-shell, name reviewer-shell
Wait for all 5 teammates to stop before synthesizing. Do NOT do the review work yourself.
```

**Step 3 — Wait + collect + cleanup** (bounded):
- Wait for TeammateIdle events (timeout: 120s per teammate, 300s total)
- If a teammate times out: proceed with partial results, annotate missing agents in report
- Collect teammate reports from shared task list
- Pass findings to Phase 11.0 (Merge & Dedupe) — same schema as SUBAGENTS path
- If 0 reports received (all timed out): fall back to SUBAGENTS path
- Call team cleanup when done

After Phase 10.0 TEAMS completes, **skip directly to Phase 11.0** (the SUBAGENTS block below is the fallback, not additive).

---

### Phase 10.0 SUBAGENTS execution (fallback, legacy)

**Launch 5 sub-agents with strict JSON contract via the Task tool:**

```yaml
parallel_analysis:
  dispatch:
    mode: "parallel (single message, 5 Task calls)"
    agents:
      correctness:
        name: "developer-executor-correctness"
        model: sonnet
        trigger: "always (MANDATORY for code stability)"
        focus:
          - "Algorithmic errors (off-by-one, bounds, indexes)"
          - "Invariant violations"
          - "State machine correctness"
          - "Concurrency issues (races, deadlocks)"
          - "Error surfacing (silent failures)"
          - "Idempotence violations"
          - "Ordering/determinism issues"

      security:
        name: "developer-executor-security"
        model: opus
        trigger: "always"
        focus:
          - "OWASP Top 10"
          - "Taint analysis (source → sink)"
          - "Supply chain risks"
          - "AuthN/AuthZ issues"
          - "Crypto misuse"
          - "Secrets exposure"

      design:
        name: "developer-executor-design"
        model: sonnet
        trigger: "risk_tags contains architecture OR files in core/, domain/, pkg/"
        focus:
          - "Antipatterns (God object, Feature envy, etc.)"
          - "DDD violations"
          - "Layering violations"
          - "SOLID violations"
          - "Design pattern misuse"

      quality:
        name: "developer-executor-quality"
        model: haiku
        trigger: "always"
        focus:
          - "Complexity metrics"
          - "Code duplication"
          - "Style issues"
          - "DTO convention check"

      shell:
        name: "developer-executor-shell"
        model: haiku
        trigger: "shell_files > 0 OR Dockerfile exists OR ci_config exists"
        focus:
          - "Shell safety (6 axes)"
          - "Dockerfile best practices"
          - "CI/CD script safety"

  agent_contract:
    input:
      files: [string]
      diff: string
      mode: "normal|triage"
      repo_profile: object  # From Phase 0.5

    output_schema:
      agent: string
      summary: string (max 200 chars)
      findings:
        - severity: "CRITICAL|HIGH|MEDIUM|LOW"
          impact: "correctness|security|design|quality|shell"
          category: string (ex: "injection", "invariant", "antipattern")

          # Location
          file: string
          line: number
          in_modified_lines: boolean

          # Label (enriches severity)
          label: "blocking|important|nit|suggestion|learning|praise"  # optional

          # Description
          title: string (max 80 chars)
          evidence: string (MANDATORY, max 300 chars, NO SECRETS)

          # For correctness/security
          oracle: "invariant|counterexample|boundary|error-surfacing|taint"
          failure_mode: string (what can go wrong)
          repro: string (scenario: input → expected vs actual)

          # For security
          source: string (taint origin)
          sink: string (vulnerable point)
          taint_path_summary: string
          references: ["CWE-XX", "OWASP-AXX"]

          # Fix
          recommendation: string (MANDATORY)
          fix_patch: string (MANDATORY for HIGH+)
          effort: "XS|S|M|L"
          confidence: "HIGH|MEDIUM|LOW"
          confidence_pct: number  # 0-100, mandatory

      commendations: [string]
      metrics:
        files_scanned: number
        findings_count: number

  confidence_gate:
    directive: "MANDATORY for all 5 executor agents"
    scoring_rubric:
      0: "False positive or pre-existing issue (not introduced by this change)"
      25: "Might be real but unverified; stylistic without CLAUDE.md callout"
      50: "Real but nitpick/minor, not very important relative to rest of PR"
      75: "Verified, important, explicit in CLAUDE.md or clearly impacts functionality"
      100: "Absolutely certain, confirmed, frequent in practice"
    rules:
      CRITICAL_severity:
        min_confidence: 95
        on_below: "Downgrade to HIGH or omit if < 75%"
      HIGH_severity:
        min_confidence: 85
        on_below: "Downgrade to MEDIUM or omit if < 75%"
      MEDIUM_severity:
        min_confidence: 75
        on_below: "Omit finding entirely"
      below_75_percent:
        action: "DO NOT REPORT - insufficient confidence"
    claudemd_double_check: |
      For CLAUDE.md compliance issues: double-check that CLAUDE.md
      explicitly mentions this guideline before reporting. Do not infer rules.
    rationale: "Reduce false positives. Only report findings the agent is confident about."

  false_positive_exclusions:
    directive: "DO NOT REPORT findings matching any of these patterns"
    general:
      - "Pre-existing issues not introduced by this PR/MR"
      - "Issues on non-modified lines (unless directly caused by the change)"
      - "Linter/compiler-catchable issues (defer to automated tools)"
      - "Silenced violations (lint ignore comments with justification)"
      - "Intentional functionality changes (part of the PR purpose)"
      - "Pedantic nitpicks without explicit CLAUDE.md backing"
      - "Logic that looks buggy but is verified correct"
    security_specific:
      - "Denial of Service vulnerabilities (out of scope)"
      - "Rate limiting concerns (infrastructure responsibility)"
      - "Memory safety in Rust or other memory-safe languages"
      - "Issues found only in unit test files"
      - "SSRF with path-only control (no host control)"
      - "Regex injection/DoS (unless user-controlled pattern)"
      - "Purely theoretical race conditions without realistic trigger"
      - "Hardcoded secrets on disk (handled by git-guard hook)"
      - "Log spoofing concerns"
      - "Lack of hardening (not expected to implement all best practices)"

  severity_rubric:
    CRITICAL:
      - "Exploitable vulnerability (RCE, injection, auth bypass)"
      - "Exposed secret/token"
      - "Unverified supply chain"
      - "Certain data loss (invariant violation)"
      - "Infinite loop (pagination bug)"
    HIGH:
      - "Probable bug (null deref, race condition)"
      - "Silent failure (error swallowed)"
      - "Layering violation (domain → infra)"
      - "State machine corruption"
    MEDIUM:
      - "Technical debt"
      - "Design antipattern"
      - "SOLID violation"
      - "Missing validation"
    LOW:
      - "Style/polish"
      - "Maintainability antipattern"
      - "Naming conventions"
```

**Secret Masking Policy (MANDATORY):**

```yaml
secret_masking:
  rule: "NEVER repost tokens/secrets/signed URLs"

  patterns_to_mask:
    - "AKIA[0-9A-Z]{16}"           # AWS Access Key
    - "ghp_[a-zA-Z0-9]{36}"        # GitHub PAT
    - "sk-[a-zA-Z0-9]{48}"         # OpenAI key
    - "eyJ[a-zA-Z0-9_-]+\\.[a-zA-Z0-9_-]+\\.[a-zA-Z0-9_-]+"  # JWT
    - "-----BEGIN.*PRIVATE KEY-----"
    - "Bearer [a-zA-Z0-9._-]+"

  action: "Replace with [REDACTED] in evidence/recommendation"
```

---

## Phase 11.0: Merge & Dedupe

**Normalize, deduplicate, require evidence:**

```yaml
merge_dedupe:
  goal: "Normalize findings, remove duplicates, enforce evidence"

  inputs:
    # T1 (Internal agents)
    - "correctness_agent.findings"
    - "security_agent.findings"
    - "design_agent.findings"
    - "quality_agent.findings"
    - "shell_agent.findings"
    # T2 (Qodo) — parsed from /tmp/qodo-review-{timestamp}.md
    - "qodo_findings (parsed)"
    # T3 (CodeRabbit) — parsed from /tmp/coderabbit-review-{timestamp}.md
    - "coderabbit_findings (parsed)"

  parse_external:
    qodo:
      source_file: "/tmp/qodo-review-{timestamp}.md"
      parser: |
        Extract findings matching pattern: [P0|P1|P2] <file>:<line> — <title>
        Map: P0→CRITICAL, P1→HIGH, P2→MEDIUM
        Set: impact="external-qodo", source="qodo"
      on_parse_failure: "log warning, skip T2 findings"

    coderabbit:
      source_file: "/tmp/coderabbit-review-{timestamp}.md"
      parser: |
        Extract CodeRabbit findings from plain text output
        Map severity from CodeRabbit format to our schema
        Set: impact="external-coderabbit", source="coderabbit"
      on_parse_failure: "log warning, skip T3 findings"

  normalize:
    required_fields:
      - severity
      - impact
      - category
      - file
      - line
      - title
      - evidence
      - recommendation
      - confidence

    optional_enriched:
      - oracle (correctness)
      - failure_mode (correctness)
      - repro (correctness)
      - source, sink (security)
      - taint_path_summary (security)
      - references (security/design)
      - fix_patch (all)
      - effort (all)

  drop_rules:
    - "evidence is missing OR evidence is empty"
    - "recommendation is missing OR recommendation is empty"
    - "impact == 'correctness' AND severity >= HIGH AND (repro is missing OR repro is empty) AND (failure_mode is missing OR failure_mode is empty)"
    - "impact == 'security' AND category == 'injection' AND severity >= HIGH AND (source is missing OR source is empty)"

  dedupe:
    key: "{impact}:{category}:{file}:{line}:{normalize(title)}"
    merge_strategy: "keep highest severity, merge evidence"

  cross_tier_dedupe:
    rule: |
      IF same file:line AND similar title (fuzzy match > 80%):
        Keep T1 finding (highest detail)
        Add "confirmed_by: [T2, T3]" field
        Boost confidence by 10% per confirmation (cap 100%)
    boost_label: "multi-tier-confirmed"

  promote:
    rule: |
      IF file has >= 3 MEDIUM findings in same impact:
        Create 1 HIGH umbrella finding
        Reference the 3 MEDIUM as sub-findings

  output:
    findings_normalized: [{...}]
    stats:
      total_before: number
      total_after: number
      dropped: number
      promoted: number
```
