# Phase 5.5: Worktree Dispatch (Semi-Auto, Optional)

**Triggered ONLY when plan contains a Parallelization table with `worktree: yes` steps.**

```yaml
worktree_dispatch:
  condition: "Plan has steps with worktree=yes AND no dependency conflicts"

  1_detect:
    action: "Parse plan for Parallelization table"
    if_absent: "Skip to Phase 6.0 (normal sequential loop)"

  2_display:
    action: "Show parallelizable groups to user"
    format: |
      ═══════════════════════════════════════════════
        /do — Parallel Worktree Dispatch
      ═══════════════════════════════════════════════

        Group 1 (parallel):
          Step 1: src/auth/   → developer-specialist-go (sonnet)
          Step 2: src/api/    → developer-specialist-go (haiku)

        Group 2 (sequential, depends on Group 1):
          Step 3: docs/       → developer-specialist-review (haiku)

      ═══════════════════════════════════════════════

  3_confirm:
    tool: AskUserQuestion
    question: "Create N worktrees and dispatch agents in parallel?"
    options:
      - label: "Yes, dispatch"
        description: "Create worktrees and run agents in parallel"
      - label: "Sequential only"
        description: "Skip worktrees, run all steps sequentially"

  4_dispatch:
    condition: "User approved"
    actions:
      - "For each parallel step: Agent(subagent_type=<agent>, model=<model>, isolation='worktree', prompt=<step description>)"
      - "Launch ALL parallel agents in SINGLE message"
      - "Wait for all to complete"

  4.5_file_overlap_check:
    action: "Before merging, check if parallel agents modified same files"
    trigger: "ALWAYS after all agents complete, BEFORE any merge"
    algorithm: |
      For each pair of worktree branches (A, B):
        DEFAULT=$(git symbolic-ref refs/remotes/origin/HEAD --short || echo origin/main)
        files_A = git diff --name-only ${DEFAULT}..branch-A
        files_B = git diff --name-only ${DEFAULT}..branch-B
        overlap = intersection(files_A, files_B)

      IF overlap is not empty:
        Show overlapping files to user
        AskUserQuestion:
          - "Merge sequentially (resolve conflicts manually)"
          - "Abort conflicting worktree (keep the other)"
          - "Abort all worktrees"
    warning_files:
      - "package.json, go.mod, Cargo.toml → dependency conflicts likely"
      - "*.lock, go.sum → regenerate after merge"
      - "CLAUDE.md → merge both sections"

  5_pre_merge_test:
    action: "Test merge before committing (like /git --merge Phase 5.0)"
    trigger: "For EACH branch before actual merge"
    steps:
      - "git merge --no-commit --no-ff <worktree-branch>"
      - "IF merge conflicts → show diff, AskUserQuestion: resolve or abort?"
      - "IF clean merge → run tests (if available and < 60s)"
      - "git merge --abort (rollback test merge)"
    if_test_fails:
      - "Show test failures to user"
      - "AskUserQuestion: Fix and retry, or skip this worktree?"

  5.5_actual_merge:
    action: "Actual merge after pre-merge test passes"
    for_each_branch:
      - "git merge --no-ff <worktree-branch>"
      - "If conflicts: show diff, AskUserQuestion for resolution"
      - "After merge: run tests again to confirm"
      - "ExitWorktree(action='remove') to cleanup"
    commit: "Merge worktree results are committed as merge commits"

  6_continue:
    action: "Continue to next group or Phase 7.0 (Synthesis)"
```

**Guardrails:**
- NEVER create worktrees without user confirmation
- NEVER merge without pre-merge test
- ALWAYS check file overlap between parallel branches
- Max parallel agents = nproc (CPU count)
- If ANY worktree fails, pause and ask user before continuing
- Worktrees are cleaned up after merge (ExitWorktree action=remove)
- If merge conflicts detected, ALWAYS escalate to user (never auto-resolve silently)
