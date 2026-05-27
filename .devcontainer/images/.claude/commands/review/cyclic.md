# Cyclic Workflow & Guardrails (Phase 14-15)

## Path Resolution (MANDATORY)

All `.claude/` paths MUST be absolute: `${WORKSPACE_ROOT}/.claude/plans/` (resolve via `git rev-parse --show-toplevel || echo /workspace`).

---

## Phase 14.0: Language-Specialist Dispatch

**Route fixes to language-specialist agent via /do:**

```yaml
language_specialist_dispatch:
  goal: "Delegate fixes to language specialist"

  routing:
    ".go":    "developer-specialist-go"
    ".py":    "developer-specialist-python"
    ".java":  "developer-specialist-java"
    ".kt":    "developer-specialist-kotlin"
    ".kts":   "developer-specialist-kotlin"
    ".ts":    "developer-specialist-nodejs"
    ".js":    "developer-specialist-nodejs"
    ".rs":    "developer-specialist-rust"
    ".rb":    "developer-specialist-ruby"
    ".ex":    "developer-specialist-elixir"
    ".exs":   "developer-specialist-elixir"
    ".php":   "developer-specialist-php"
    ".scala": "developer-specialist-scala"
    ".cpp":   "developer-specialist-cpp"
    ".cc":    "developer-specialist-cpp"
    ".hpp":   "developer-specialist-cpp"
    ".c":     "developer-specialist-c"
    ".h":     "developer-specialist-c"
    ".dart":  "developer-specialist-dart"
    ".cs":    "developer-specialist-csharp"
    ".swift": "developer-specialist-swift"
    ".r":     "developer-specialist-r"
    ".R":     "developer-specialist-r"
    ".pl":    "developer-specialist-perl"
    ".pm":    "developer-specialist-perl"
    ".lua":   "developer-specialist-lua"
    ".f90":   "developer-specialist-fortran"
    ".f95":   "developer-specialist-fortran"
    ".f03":   "developer-specialist-fortran"
    ".adb":   "developer-specialist-ada"
    ".ads":   "developer-specialist-ada"
    ".cob":   "developer-specialist-cobol"
    ".cbl":   "developer-specialist-cobol"
    ".pas":   "developer-specialist-pascal"
    ".dpr":   "developer-specialist-pascal"
    ".vb":    "developer-specialist-vbnet"
    ".m":     "developer-specialist-matlab"
    ".asm":   "developer-specialist-assembly"
    ".s":     "developer-specialist-assembly"

  dispatch:
    # PR1 — Skills Architecture v1.3: replace magic-string with Skill recursion.
    # `Skill` makes the chain visible to TaskCreated hooks and gives the
    # cycle-detection guard (max depth 5) something to actually count.
    primitive: |
      Skill(skill="do", args="--plan .claude/plans/review-fixes-{timestamp}.md")
    fallback_when_skill_absent: "/do --plan .claude/plans/review-fixes-{timestamp}.md"
    executor: "developer-specialist-{lang}"

  integration_with_do:
    workflow:
      1: "/review generates plan with findings + fix_patch"
      2: "/do loads plan"
      3: "/do groups by language"
      4: "/do dispatches to language-specialist agents"
      5: "Language-specialists apply fixes"
      6: "/do returns control to /review"
      7: "If --loop, re-run /review"
```

---

## Phase 15.0: Cyclic Validation

**Loop until perfect OR --loop limit:**

```yaml
cyclic_workflow:
  trigger: "/review --loop [N]"

  modes:
    no_flag: "Single review, no fix, no loop"
    loop_only: "--loop → Infinite until perfect"
    loop_N: "--loop 5 → Max 5 iterations"

  flow:
    iteration_1:
      1_review: "Full analysis (15 phases, 5 agents)"
      2_generate_plan: ".claude/plans/review-fixes-{timestamp}.md"
      3_dispatch_to_do: "/do --plan {plan_file}"

    iteration_2_to_N:
      1_review_validation: "/review (re-scan post-fix)"
      2_check_remaining:
        if: "findings.CRITICAL + findings.HIGH > 0"
        then: "Generate new plan, continue loop"
        else: "Exit loop (success)"
      3_check_loop_limit:
        if: "iteration >= N"
        then: "Exit loop (limit reached)"

  exit_conditions:
    - "No CRITICAL/HIGH findings remaining"
    - "--loop limit reached"
    - "User interrupt (Ctrl+C)"

  output_per_iteration:
    format: |
      ═══════════════════════════════════════════════════════════════
        Iteration {X}/{N}
        Findings: CRIT={a}, HIGH={b}, MED={c}, LOW={d}
        Fixes applied: {n} files modified
        Status: {CONTINUE|SUCCESS|LIMIT_REACHED}
      ═══════════════════════════════════════════════════════════════
```

---

## Guardrails

| Action | Status |
|--------|--------|
| Auto-approve/merge | FORBIDDEN |
| Skip security issues | FORBIDDEN |
| Modify code directly | FORBIDDEN |
| Post comment without user validation | FORBIDDEN |
| Mention AI in PR responses | **ABSOLUTE FORBIDDEN** |
| Skip Phase 0-1 (context/intent) | FORBIDDEN |
| Challenge without context (skip Phase 3-4) | FORBIDDEN |
| Expose secrets in evidence/output | FORBIDDEN |
| Ignore budget limits | FORBIDDEN |
| Pattern analysis on docs-only PR | SKIP (not forbidden) |

---

## No-Regression Checklist

```yaml
no_regression:
  check_in_pr:
    - "Tests added/adjusted for changes?"
    - "Migration/rollback needed?"
    - "Backward compatibility maintained?"
    - "Config changes documented?"
    - "Observability (logs/metrics) added?"
```

---

## Error Handling

```yaml
error_handling:
  mcp_rate_limit:
    action: "Exponential backoff (1s, 2s, 4s)"
    fallback: "git diff local"
    max_retries: 3

  agent_timeout:
    max_wait: 60s
    action: "Continue without this agent, report"

  large_diff:
    threshold: 5000 lines
    action: "Force TRIAGE mode, warn user"
```

---

## Shell Safety Checks (if *.sh present)

```yaml
shell_safety_axes:
  1_download_safety:
    checks:
      - "mktemp for temporary files?"
      - "curl --retry --proto '=https'?"
      - "install -m instead of chmod?"
      - "rm -f cleanup?"

  2_download_robustness:
    checks:
      - "Track download failures?"
      - "Exit if critical?"
      - "Avoid silent failures?"

  3_path_determinism:
    checks:
      - "Absolute paths in configs?"
      - "No implicit PATH dependency?"

  4_fallback_completeness:
    checks:
      - "Fallback copies binary to correct location?"

  5_input_resilience:
    checks:
      - "Handles empty input?"
      - "set -e with graceful handling?"

  6_url_validation:
    checks:
      - "Release URL exists?"
      - "Official script if available?"
```

---

## DTO Convention Check (Go files)

**Verify Go DTOs use `dto:"direction,context,security"`:**

```yaml
dto_validation:
  trigger: "*.go files in diff"
  severity: MEDIUM

  detection:
    suffixes: [Request, Response, DTO, Input, Output, Payload, Message, Event, Command, Query]
    serialization_tags: ["json:", "yaml:", "xml:"]

  check: |
    Struct name matches *Request/*Response/*DTO/etc.
    AND has serialization tags
    → MUST have dto:"dir,ctx,sec" on each PUBLIC field

  valid_format: 'dto:"<direction>,<context>,<security>"'
  valid_values:
    direction: [in, out, inout]
    context: [api, cmd, query, event, msg, priv]
    security: [pub, priv, pii, secret]

  report_format: |
    ### DTO Convention
    | File | Struct | Status | Issue |
    |------|--------|--------|-------|
    | user_dto.go | CreateUserRequest | OK | - |
    | order.go | OrderResponse | FAIL | Missing dto:"..." tags |

  reference: "~/.claude/docs/conventions/dto-tags.md"
```

---

## Review Iteration Loop

```yaml
iteration_loop:
  description: |
    Continuous improvement based on bot feedback.

  process:
    1: "Collect bot suggestions (Phase 2.6)"
    2: "Extract BEHAVIOR (not the fix)"
    3: "Categorize (shell/security/quality)"
    4: "Add to workflow (user approves)"
    5: "Commit the improvement"

  example:
    input: "Use mktemp to prevent partial writes"
    output:
      behavior: "Downloads should use temp files"
      category: "shell_safety"
      axis: "1_download_safety"
```
