# Agent Dispatch (Phases 0-3, 8-9)

## Phase 1.0: Context Detection

**Run `review-context.sh` to collect ALL context in ONE call:**

```bash
bash ~/.claude/scripts/review-context.sh
```

Returns JSON with: `git{branch, platform, org, repo}`, `diff{files, stats}`, `repo_profile{lint_configs}`, `pr{exists, number}`.

Use this output for ALL decisions. DO NOT re-run individual git commands.

**Then route based on platform:**

```yaml
context_detection:
  1_collect:
    command: "bash ~/.claude/scripts/review-context.sh"
    output: "JSON with git context, diff, repo profile, PR detection"

  1.5_platform_detection:
    rule: |
      From review-context.sh JSON:
      IF git.platform == "github":
        platform = "github"
        mcp_prefix = "mcp__github__"
      ELSE IF git.platform == "gitlab":
        platform = "gitlab"
        mcp_prefix = "mcp__gitlab__"
      ELSE:
        platform = "local"
        mcp_prefix = null
    output:
      platform: "github" | "gitlab" | "local"
      mcp_prefix: string | null

  2_pr_mr_detection:
    tools:
      github: "mcp__github__list_pull_requests(head: current_branch)"
      gitlab: "mcp__gitlab__list_merge_requests(source_branch: current_branch)"
    output:
      on_pr_mr: boolean
      pr_mr_number: number | null
      pr_mr_url: string | null
      target_branch: string  # base branch

  3_diff_source:
    rule: |
      IF on_pr_mr == true:
        source = "PR/MR diff via MCP"
        base = target_branch
        head = current_branch
      ELSE:
        source = "local diff"
        base = "git merge-base origin/main HEAD"
        head = "HEAD"
    output:
      diff_source: "pr" | "mr" | "local"
      merge_base: string
      display: "Reviewing: {base}...{head}"

  4_ci_status:
    condition: "on_pr_mr == true"
    tools:
      github: "mcp__github__pull_request_read (method: get_status)"
      gitlab: "mcp__gitlab__list_pipelines(ref: current_branch)"
    strategy:
      max_polls: 2
      poll_interval: 30s
      on_pending: "Continue with warning 'CI pending'"
      on_failure: "Report in review, do not block"
    output:
      ci_status: "passing|pending|failing|unknown"
      ci_jobs: [{name, status, conclusion}]
```

**Output Phase 0 (GitHub):**

```
═══════════════════════════════════════════════════════════════
  /review - Context Detection
═══════════════════════════════════════════════════════════════

  Platform: GitHub
  Branch: feat/post-compact-hook
  PR: #97 (open) → main
  Diff source: PR (mcp__github)
  Merge base: a60a896...847d6db

  CI Status: passing (3/3 jobs)
    ├─ build: passed (1m 23s)
    ├─ test: passed (2m 45s)
    └─ lint: passed (45s)

  Mode: NORMAL (18 files, 375 lines)

═══════════════════════════════════════════════════════════════
```

**Output Phase 0 (GitLab):**

```
═══════════════════════════════════════════════════════════════
  /review - Context Detection
═══════════════════════════════════════════════════════════════

  Platform: GitLab
  Branch: feat/post-compact-hook
  MR: !42 (open) → main
  Diff source: MR (mcp__gitlab)
  Merge base: a60a896...847d6db

  CI Status: passed (pipeline #12345)
    ├─ build: passed (1m 23s)
    ├─ test: passed (2m 45s)
    └─ lint: passed (45s)

  Mode: NORMAL (18 files, 375 lines)

═══════════════════════════════════════════════════════════════
```

---

## Phase 2.0: Repo Profile (Cacheable)

**Build stable repo understanding BEFORE analysis:**

```yaml
repo_profile:
  cache:
    location: ".claude/.cache/repo_profile.json"
    key: "repo_profile@{default_branch}"
    ttl: "7 days"

  inputs:
    priority_files:
      - "README.md"
      - "CONTRIBUTING.md"
      - "ARCHITECTURE.md"
      - ".editorconfig"
      - ".golangci*"
      - ".eslintrc*"
      - "pyproject.toml"
      - "CODEOWNERS"
      - "~/.claude/docs/**"

  extract:
    languages: [string]
    build_tools: [string]
    test_frameworks: [string]
    lint_tools: [string]
    architecture_style: "hexagonal|layered|cqrs|microservices|monolith"
    error_conventions: [string]  # "wrap with fmt.Errorf", etc.
    naming_conventions: [string]
    ownership:
      codeowners_present: boolean
      owners_by_path: [{path, owners}]

  output:
    repo_profile_summary: "max 50 lines, JSON"

  usage: |
    Injected into EVERY agent so they:
    - Adapt checks to repo conventions
    - Avoid false positives on intentional patterns
    - Respect established style
```

---

## Phase 3.0: Intent Analysis

**Understand the PR/MR intent BEFORE heavy analysis:**

```yaml
intent_analysis:
  inputs:
    github:
      - "mcp__github__pull_request_read (method: get → title, body, labels)"
      - "mcp__github__pull_request_read (method: get_files → file list)"
    gitlab:
      - "mcp__gitlab__get_merge_request (title, description, labels)"
      - "mcp__gitlab__get_merge_request_changes (file list)"
    common:
      - "git diff --stat"

  extract:
    title: string
    description: string (first 500 chars)
    labels: [string]
    files_changed: number
    lines_added: number
    lines_deleted: number
    directories_touched: [string]
    file_categories:
      security: count
      shell: count
      config: count
      tests: count
      docs: count
      code: count

  calibration:
    rule: |
      IF files_changed <= 5 AND only docs/config:
        analysis_depth = "light"
        skip_patterns = true
      ELSE IF security_files > 0 OR shell_files > 0:
        analysis_depth = "deep"
        force_security_scan = true
      ELSE:
        analysis_depth = "normal"

    split_recommendation:
      threshold: 400
      action: |
        SI lines_added > 400:
          warning = "Consider splitting: this PR has {n} lines. PRs under 400 lines get better reviews."
          include_in_output = true

  risk_model:
    goal: "Identify critical zones BEFORE heavy analysis"

    risk_tags:
      - "authn_authz"      # auth, jwt, oauth, rbac, acl, session
      - "crypto"           # crypto, x509, tls, sign, encrypt, hash
      - "secrets"          # secret, token, key, vault, password
      - "network"          # http, grpc, tcp, udp, dns, socket
      - "db_migrations"    # migrate, schema, sql, gorm, prisma
      - "concurrency"      # goroutine, mutex, channel, lock, atomic
      - "supply_chain"     # Dockerfile, go.sum, package-lock
      - "state_machine"    # state, transition, fsm, workflow
      - "pagination"       # cursor, offset, limit, page
      - "caching"          # cache, ttl, invalidate, redis

    calibration:
      rule: |
        IF any(risk_tags):
          analysis_depth = "deep"
          prioritize_files = "risk-touched first"
          enable_agents = ["correctness", "security", "design"]
        IF risk_tags contains ["authn_authz", "crypto", "secrets"]:
          force_security_deep = true
        IF risk_tags contains ["concurrency", "state_machine"]:
          force_correctness_deep = true

    output:
      risk_tags: [string]
      risk_files: [{path, risk_tags}]
      review_priorities: ["correctness", "security", "design", "quality"]
```

**Output Phase 1:**

```
═══════════════════════════════════════════════════════════════
  /review - Intent Analysis
═══════════════════════════════════════════════════════════════

  Title: feat(hooks): add SessionStart hook
  Labels: [enhancement]

  Scope:
    ├─ Files: 18 (+375, -12)
    ├─ Dirs: .devcontainer/, .claude/
    └─ Categories: shell(2), config(3), code(13)

  Calibration:
    ├─ Depth: DEEP (shell files detected)
    ├─ Security scan: FORCED
    └─ Pattern analysis: CONDITIONAL

═══════════════════════════════════════════════════════════════
```

---

## Phase 8.0: Behavior Extraction (AI Reviews)

**Extract behavioral patterns from AI reviews:**

```yaml
behavior_extraction:
  filter:
    - "importance >= 6/10"
    - "not already in workflow"
    - "actionable pattern"

  extract:
    from: "{bot_suggestion_text}"
    to:
      behavior: "short pattern description"
      category: "shell_safety|security|quality|pattern"
      check: "question to add to workflow"

  action:
    auto: false
    prompt_user: |
      New pattern detected:
        Behavior: {behavior}
        Category: {category}

      Add to /review workflow? [Yes/No]
```

---

## Phase 9.0: Peek & Decompose

**Snapshot the diff and categorize:**

```yaml
peek_decompose:
  1_diff_snapshot:
    tool: |
      IF diff_source == "pr" (GitHub):
        mcp__github__pull_request_read (method: get_diff)
      ELSE IF diff_source == "mr" (GitLab):
        mcp__gitlab__get_merge_request_changes
      ELSE:
        git diff --merge-base {base}...HEAD

    extract:
      files: [{path, status, additions, deletions}]
      total_lines: number
      hunks_count: number

  2_categorize:
    rules:
      security:
        patterns: ["auth", "crypto", "password", "token", "secret", "jwt"]
        extensions: [".go", ".py", ".js", ".ts", ".java"]
      shell:
        extensions: [".sh"]
        files: ["Dockerfile", "Makefile"]
      config:
        extensions: [".json", ".yaml", ".yml", ".toml"]
        files: ["mcp.json", "settings.json", "*.config.*"]
      tests:
        patterns: ["*_test.*", "*.test.*", "*.spec.*", "test_*"]
      docs:
        extensions: [".md"]

  3_mode_decision:
    rule: |
      IF total_lines > 1500 OR files.count > 30:
        mode = "TRIAGE"
      ELSE:
        mode = "NORMAL"
```
