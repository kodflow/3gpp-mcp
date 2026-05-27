# Commit Workflow (Phases 2-7)

Phases for the `--commit` action after identity validation (Phase 1.0).

---

## Phase 2.0: Peek (RLM Pattern)

**Collect ALL git context in a single script call:**

```yaml
peek_workflow:
  1_collect_all:
    action: "Run git-peek.sh to get identity, branch, status, diff, remote in ONE call"
    command: "bash ~/.claude/scripts/git-peek.sh"
    returns: "JSON with identity, branch, status, head, diff_stats, remote"
    critical_rule: |
      This script replaces 13 sequential git commands.
      READ the JSON output and make decisions based on it.
      DO NOT re-run individual git commands — all data is in the JSON.

  2_decisions:
    action: "Based on git-peek.sh JSON output, decide:"
    decision:
      - "branch.is_protected == true → MUST create new branch"
      - "status.has_lock == true → Auto-remove lock (rm -f .git/index.lock) and continue. Common with GitKraken/parallel tools."
      - "status.conflicts is not empty → BLOCK commit. Show conflicts list. Suggest: resolve conflicts first, then retry"
      - "identity.name is empty → Ask user for identity via AskUserQuestion"
      - "worktree.active_count > 0 → WARN: active worktrees exist, verify no conflicts"

  2.5_conflict_gate:
    rule: "ABSOLUTE BLOCK if conflicts detected"
    check: "status.conflicts array from git-peek.sh"
    if_not_empty: |
      BLOCK the commit immediately. Display:
        "Merge conflicts detected in N files: [list].
         Resolve conflicts before committing.
         Run: git mergetool or manually edit conflicted files."
      DO NOT proceed to Phase 3.0.
```

**Output Phase 1:**

```text
═══════════════════════════════════════════════════════════════
  /git --commit - Peek Analysis
═══════════════════════════════════════════════════════════════

  Branch: main (protected)
  Status: 5 files modified, 2 untracked

  Changes detected:
    ├─ src/auth/login.ts (+45, -12)
    ├─ src/auth/logout.ts (+23, -5)
    ├─ tests/auth.test.ts (+80, -0) [new]
    ├─ package.json (+2, -1)
    └─ README.md (+15, -3)

  Decision: CREATE new branch (on protected main)

═══════════════════════════════════════════════════════════════
```

---

## Phase 3.0: Decompose (RLM Pattern)

**Categorize modified files:**

```yaml
decompose_workflow:
  categories:
    features:
      patterns: ["src/**/*.ts", "src/**/*.js", "src/**/*.go", "src/**/*.rs", "src/**/*.py"]
      prefix: "feat"

    fixes:
      patterns: ["*fix*", "*bug*"]
      prefix: "fix"

    tests:
      patterns: ["tests/**", "**/*.test.*", "**/*_test.go"]
      prefix: "test"

    docs:
      patterns: ["*.md", "docs/**", "**/CLAUDE.md", ".claude/commands/*.md"]
      prefix: "docs"

    config:
      patterns: ["*.json", "*.yaml", "*.toml", ".devcontainer/**"]
      prefix: "chore"

    hooks:
      patterns: [".devcontainer/hooks/**", ".claude/scripts/**", ".githooks/**"]
      prefix: "fix"

  auto_detect:
    action: "Infer the dominant type"
    output: "commit_type, scope, branch_name"

  gitignore_awareness:
    rule: |
      BEFORE categorizing, check the gitignore status of each file.
      Use `git status --porcelain` to list ALL modified files.
      Gitignored files do not appear in git status → no risk.
      Tracked modified files MUST be included, even if they are in .claude/ or CLAUDE.md.
    check: |
      # List ALL modifications (staged + unstaged + untracked non-ignored)
      git status --porcelain
      # Verify that nothing tracked is forgotten after staging
      git diff --name-only  # Must be empty after git add -A
```

---

## Phase 4.0: Incremental Quality Gate (Lint + Test in Parallel)

**Runs lint + test in PARALLEL, scoped to changed files/packages only.**

```yaml
incremental_quality:
  script: "~/.claude/scripts/pre-commit-quality.sh"
  trigger: "ALWAYS before commit (mandatory)"
  scope: "Only files changed vs base branch (not entire project)"
  parallelism: "lint and test run simultaneously in background"

  strategy:
    1_detect_changes: "git diff main...HEAD + unstaged + staged → changed files"
    2_detect_languages: "Map file extensions to languages (Go, Rust, TS, Python, Shell, etc.)"
    3_scoped_first: "Language-specific tools scoped to changed files/packages"

  supported_languages:
     go: { lint: "golangci-lint run <changed_pkgs>", test: "make test → bazel test //... → go test -race -timeout 180s <changed_pkgs>" }
    rust: { lint: "cargo clippy -- -D warnings", test: "cargo test" }
    node: { lint: "npx eslint <changed_files>", test: "npx vitest run" }
    python: { lint: "ruff check <changed_files>", test: "pytest" }
    shell: { lint: "shellcheck -x <changed_files>", test: "bats tests/**/*.bats" }
    ruby: { lint: "rubocop <changed_files>", test: "bundle exec rspec" }
    php: { lint: "phpstan analyse <changed_files>", test: "phpunit" }
    elixir: { lint: "mix credo --strict", test: "mix test" }
    dart: { lint: "dart analyze <changed_files>", test: "dart test" }

  execution:
    command: "bash ~/.claude/scripts/pre-commit-quality.sh main"
    parallel: true  # lint and test run as background jobs simultaneously
    timeout: 300s
    on_failure: "BLOCK commit — Claude must fix errors before retrying"
    on_success: "Continue to Phase 5.0 (Secret Scan)"
```

**Key difference from old on-stop-quality.sh:**
- Old: ran after EVERY Claude response (even Q&A), on ALL project files
- New: runs ONLY during `/git --commit`, scoped to changed files only

**Output:**

```text
═══════════════════════════════════════════════════════════════
  Pre-commit Quality Gate
═══════════════════════════════════════════════════════════════

  Changed: 5 files (Go: 3, Shell: 2)

  [PARALLEL]
    ├─ lint  : ✓ passed (golangci-lint + shellcheck)
    └─ test  : ✓ passed (go test + bats)

  Pre-commit Quality Gate PASSED

═══════════════════════════════════════════════════════════════
```

**IMPORTANT**: Run `.claude/scripts/pre-commit-quality.sh main` which auto-detects languages.

---

## Phase 5.0: Secret Scan (1Password Integration)

**ABSOLUTE RULE: No real secret/password must leak into a commit.**

**Secrets policy:**

| Type | Action | Example |
|------|--------|---------|
| Real secret (token, prod password) | **BLOCK the commit** | `ghp_abc123...`, `postgres://user:realpass@prod/db` |
| Test password | **OK if in `.example` file** | `.env.example`, `config.example.yaml` |
| Test password in code | **OK if explicitly commented** | `// TEST ONLY - not a real credential` |
| `.env` file with real secrets | **NEVER committed** | Must be in `.gitignore` |

**`.example` files:** Test passwords in `.example` files are accepted because they serve as documentation. They MUST have a comment explaining they are test values:

```bash
# .env.example - Test/default values only, NOT real credentials
DB_PASSWORD=test_password_change_me    # TEST ONLY
API_KEY=sk-test-fake-key-for-dev       # TEST ONLY
```

**Scan staged files for hardcoded secrets:**

```yaml
secret_scan:
  trigger: "ALWAYS run in parallel with language checks"
  blocking: true  # BLOCKS the commit if real secret detected

  0_policy:
    real_secrets: "BLOCK - never commit real tokens, passwords, API keys"
    test_passwords_in_example_files: "ALLOW - .example files are documentation"
    test_passwords_in_code: "ALLOW if commented with '// TEST ONLY' or '# TEST ONLY'"
    env_files: "BLOCK - .env must be in .gitignore, use .env.example instead"

  1_get_staged_files:
    command: "git diff --cached --name-only"
    exclude: [".env", ".env.*", "*.lock", "*.sum"]

  1b_check_env_not_staged:
    command: "git diff --cached --name-only | grep -E '^\.env$' || true"
    action: |
      IF .env is staged:
        BLOCK the commit
        Message: ".env potentially contains real secrets. Use .env.example for default values."

  2_scan_patterns:
    patterns:
      tokens:
        - 'ghp_[a-zA-Z0-9]{36}'           # GitHub PAT
        - 'glpat-[a-zA-Z0-9\-]{20}'       # GitLab PAT
        - 'sk-[a-zA-Z0-9]{48}'            # OpenAI/Stripe secret key
        - 'pk_[a-zA-Z0-9]{24,}'           # Stripe publishable key
        - 'ops_[a-zA-Z0-9]{50,}'          # 1Password service account
        - 'AKIA[0-9A-Z]{16}'             # AWS access key
      connection_strings:
        - 'postgres://[^\s]+'
        - 'mysql://[^\s]+'
        - 'mongodb(\+srv)?://[^\s]+'
      generic:
        - '[a-zA-Z0-9+/]{40,}={0,2}'     # Long base64 (potential secrets)

    exceptions:
      - file_pattern: "*.example*"         # .env.example, config.example.yaml
      - file_pattern: "*_example.*"
      - file_pattern: "*.sample*"
      - comment_marker: "TEST ONLY"        # Inline comment marks test value
      - comment_marker: "FAKE"
      - comment_marker: "PLACEHOLDER"
      - value_pattern: "test_*"            # test_password, test_token
      - value_pattern: "fake_*"
      - value_pattern: "dummy_*"
      - value_pattern: "changeme"
      - value_pattern: "TODO:*"

  3_if_secrets_found:
    action: "BLOCK commit + suggestion"
    output: |
      ═══════════════════════════════════════════════════════════════
        ⛔ REAL SECRETS DETECTED - COMMIT BLOCKED
      ═══════════════════════════════════════════════════════════════

        Found {count} potential secret(s) in staged files:

        File: src/config.go
          Line 42: ghp_xxxx... (GitHub PAT)
          Suggestion: /secret --push GITHUB_TOKEN=<value>
                      Replace with: os.Getenv("GITHUB_TOKEN")

        File: .env.production
          Line 5: postgres://user:pass@host/db
          Suggestion: /secret --push DATABASE_URL=<value>

        Action: Use /secret --push to store in 1Password
                Then replace with env var reference

        Test passwords? Put them in .env.example with comment:
          DB_PASSWORD=test_pass  # TEST ONLY

      ═══════════════════════════════════════════════════════════════

  4_if_no_secrets:
    output: "[PASS] No hardcoded secrets detected"
```

---

## Phase 6.0: Context Update (MANDATORY before commit)

**Updates CLAUDE.md files to reflect the branch modifications.**

**IMPORTANT**: This phase runs AFTER lint/test/build (Phase 3) to avoid
re-running `/warmup --update` if checks fail and require corrections.

```yaml
context_update_workflow:
  trigger: "ALWAYS (mandatory before commit)"
  position: "After Phase 4 + 5 (all checks pass), before Phase 7 (commit)"
  tool: "/warmup --update"

  1_collect_branch_diff:
    action: "Identify ALL files modified on the branch"
    command: |
      # Files modified in the entire branch (vs main)
      git diff main...HEAD --name-only 2>/dev/null || git diff HEAD --name-only
      # + unstaged/uncommitted files (in progress)
      git diff --name-only
      git diff --cached --name-only
      # Deduplicate
    output: "changed_files[] (unique list)"

  2_resolve_claude_files:
    action: "Find CLAUDE.md files affected by modified files"
    algorithm: |
      FOR each modified file:
        dir = dirname(file)
        WHILE dir != /workspace:
          IF exists(dir/CLAUDE.md):
            add(dir/CLAUDE.md) to set
          dir = parent(dir)
      # Always include /workspace/CLAUDE.md (root)
    output: "claude_files_to_update[] (unique set)"

  3_run_warmup_update:
    action: "Run /warmup --update on ALL resolved CLAUDE.md files (unconditional)"
    tool: "Skill(warmup, --update)"
    scope: "All directories from claude_files_to_update"
    note: |
      /warmup --update will automatically add the ISO timestamp
      as the first line of each updated CLAUDE.md:
        <!-- updated: 2026-02-11T14:30:00Z -->
      No staleness check — ALWAYS update to ensure accuracy.

  4_stage_updated_docs:
    action: "Add updated CLAUDE.md files to staging"
    command: "git add **/CLAUDE.md"
    note: "Included in the same commit as code modifications"

  timestamp_format:
    format: "<!-- updated: YYYY-MM-DDTHH:MM:SSZ -->"
    example: "<!-- updated: 2026-02-11T14:30:00Z -->"
    position: "First line of CLAUDE.md file"
    purpose: "Track last update time (unconditional update on every commit)"
    parse: "ISO 8601 - easiest format to parse programmatically"
```

**Output Phase 6.0:**

```text
═══════════════════════════════════════════════════════════════
  /git --commit - Context Update (Phase 6.0)
═══════════════════════════════════════════════════════════════

  Branch diff: 12 files changed

  CLAUDE.md resolution:
    ├─ /workspace/CLAUDE.md (updated)
    ├─ .devcontainer/CLAUDE.md (updated)
    ├─ .devcontainer/images/CLAUDE.md (updated)
    └─ .devcontainer/hooks/CLAUDE.md (updated)

  /warmup --update:
    ✓ 4 CLAUDE.md files updated (unconditional)
    ✓ Timestamps refreshed
    ✓ Staged for commit

═══════════════════════════════════════════════════════════════
```

---

## Phase 7.0: Execute & Synthesize

```yaml
execute_workflow:
  1_branch:
    action: "Create or use branch"
    auto: true

  2_stage:
    action: "Stage ALL tracked modified files"
    steps:
      - command: "git add -A"
        note: "git add -A respects .gitignore automatically — no ignored file will be staged"
      - command: "git diff --name-only"
        verify: "MUST be empty — otherwise tracked files have been missed"
        on_failure: |
          If tracked files remain unstaged after git add -A:
          → Add them explicitly with git add <file>
          → NEVER ignore modifications to tracked files (CLAUDE.md, .claude/commands/, hooks/)
    rules:
      - "ALWAYS use git add -A (never selective staging by filename)"
      - "git add -A automatically includes: CLAUDE.md, .devcontainer/, .claude/commands/"
      - "git add -A automatically excludes: .env, mcp.json, .claude/* (except gitignore exceptions)"
      - "Check git diff --name-only after staging — if non-empty, there is a problem"
      - "If a tracked file should NOT be committed → git restore <file> BEFORE staging, not after"

  3_commit:
    action: "Create the commit"
    format: |
      <type>(<scope>): <description>

      [optional body]

  4_push:
    action: "Push to origin"
    command: "git push -u origin <branch>"

  5_pr_mr:
    action: "Create the PR/MR"
    tools:
      github: mcp__github__create_pull_request
      gitlab: mcp__gitlab__create_merge_request
    skip_if: "--no-pr"

    body_format:
      rule: "STRICT format. Clean markdown. No escaped newlines. No bot content."
      template: |
        ## Summary

        - First change description
        - Second change description

        ## Test plan

        - [ ] First test item
        - [ ] Second test item
      rules:
        - "Use REAL newlines in the body string, NEVER literal \\n"
        - "Keep it SHORT: max 10 bullet points in Summary"
        - "Test plan: concrete, checkable items"
        - "No AI disclaimers, no emojis, no verbose explanations"
        - "No duplicate of the commit message"
        - "Bots will append their own analysis — keep ours minimal"
```

**Final Output (GitHub):**

```text
═══════════════════════════════════════════════════════════════
  /git --commit - Completed (GitHub)
═══════════════════════════════════════════════════════════════

| Step    | Status                           |
|---------|----------------------------------|
| Peek    | ✓ 5 files analyzed               |
| Checks  | ✓ lint, test, build PASS         |
| Context | ✓ 3 CLAUDE.md updated            |
| Branch  | `feat/add-user-auth`             |
| Commit  | `feat(auth): add user auth`      |
| Push    | origin/feat/add-user-auth        |
| PR      | #42 - feat(auth): add user auth  |

URL: https://github.com/<owner>/<repo>/pull/42

═══════════════════════════════════════════════════════════════
```

**Final Output (GitLab):**

```text
═══════════════════════════════════════════════════════════════
  /git --commit - Completed (GitLab)
═══════════════════════════════════════════════════════════════

| Step    | Status                           |
|---------|----------------------------------|
| Peek    | ✓ 5 files analyzed               |
| Checks  | ✓ lint, test, build PASS         |
| Context | ✓ 3 CLAUDE.md updated            |
| Branch  | `feat/add-user-auth`             |
| Commit  | `feat(auth): add user auth`      |
| Push    | origin/feat/add-user-auth        |
| MR      | !42 - feat(auth): add user auth  |

URL: https://gitlab.com/<owner>/<repo>/-/merge_requests/42

═══════════════════════════════════════════════════════════════
```

---

## Phase 8.0: Post-commit Skill chain (PR1 — Skills Architecture v1.3)

After the PR/MR is created, if the commit touched > 5 files OR the
working tree still has staged changes, hand off to `/review --staged`
through `Skill` recursion (cycle-detection guard limits depth to 5):

```yaml
post_commit_chain:
  trigger: "PR/MR created AND (staged_files > 0 OR touched_files >= 5)"
  primitive: |
    Skill(skill="review", args="--staged")
  fallback_when_skill_absent: "advise user to run /review --staged manually"
```

Skipped for trivial fixes (single `.md` file, < 20 lines changed).
