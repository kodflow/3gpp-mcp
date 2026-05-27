# Report Generation & Plan Output (Phases 12-13)

## Phase 12.0: Challenge & Synthesize

**Evaluate relevance with OUR context:**

```yaml
challenge_feedback:
  timing: "AFTER phases 3-4 (we have full context)"

  for_each_suggestion:
    evaluate:
      - "Within the PR scope?"
      - "Applicable to our stack/language?"
      - "Pattern already implemented elsewhere?"
      - "Conscious trade-off?"
      - "Generic suggestion vs specific case?"
      - "If suggestion = 'missing X' → Grep codebase for X. If never called → REJECT (YAGNI)"

  classify:
    KEEP:
      action: "Integrate into findings"
      confidence: "HIGH"
    PARTIAL:
      action: "Report with nuance"
      confidence: "MEDIUM"
    REJECT:
      action: "Ignore with reason"
      confidence: "LOW"
    DEFER:
      action: "Create separate issue"
      reason: "Out of PR scope"

  output_format:
    table:
      - suggestion: string
      - source: string (bot name)
      - verdict: "KEEP|PARTIAL|REJECT|DEFER"
      - rationale: string (1-2 lines)
      - action: "apply|issue|ignore"

  ask_user_if:
    - "Ambiguity on relevance"
    - "Undocumented trade-off"
    - "Suggestion impacts architecture"
```

**Challenge table:**

| Situation | Verdict | Action |
|-----------|---------|--------|
| Valid suggestion, applicable | KEEP | Apply now |
| Valid suggestion, out of scope | DEFER | Create issue |
| Generic suggestion, not applicable | REJECT | Ignore + rationale |
| Conscious trade-off | REJECT | Document trade-off |
| Ambiguity | ASK | User decision |

---

## Phase 13.0: Output Generation (LOCAL ONLY)

**Generate LOCAL report + /plan file (NO GitHub/GitLab posting):**

```yaml
output_generation:
  mode: "LOCAL ONLY - No PR/MR comments"

  inputs:
    - findings_normalized: "Phase 4.7 output"
    - validated_suggestions: "Phase 5 KEEP items"
    - repo_profile: "Phase 0.5 output"

  tone_directive:
    rule: "Prefer question-based framing for HIGH/MEDIUM findings"
    examples:
      bad: "This will crash on null input"
      good: "What happens when input is null here?"
      bad: "Missing error handling"
      good: "How does this behave if the API returns an error?"
    exceptions: "CRITICAL findings remain declarative (urgency)"

  outputs:
    1_terminal_report:
      format: "Markdown to terminal"
      sections:
        - summary
        - critical_issues
        - high_priority
        - medium (max 5)
        - low (max 3)
        - commendations
        - metrics

    2_plan_file:
      location: ".claude/plans/review-fixes-{timestamp}.md"
      content:
        header: |
          # Review Fixes Plan
          Generated: {timestamp}
          Branch: {branch}
          Files: {files_count}
          Findings: CRIT={n}, HIGH={n}, MED={n}

        sections:
          critical:
            title: "## Critical (MUST FIX)"
            items: |
              ### {title}
              - **File:** {file}:{line}
              - **Impact:** {impact}
              - **Evidence:** {evidence}
              - **Fix:** {fix_patch}
              - **Language:** {language}
              - **Specialist:** developer-specialist-{lang}

          high:
            title: "## High Priority"
            items: "Same format as critical"

          medium:
            title: "## Medium"
            items: "Same format, max 5"

  no_github_gitlab:
    rule: "NEVER post comments to PR/MR"
    reason: "Reviews are local, fixes via /do"

  post_review_action:
    trigger: "NOT --loop mode AND findings with HIGH+ severity exist"
    skip_conditions:
      - "--loop mode (auto-fix cycle handles this)"
      - "No findings (APPROVE verdict)"
      - "Only LOW/MEDIUM severity findings"

    workflow:
      tool: AskUserQuestion
      questions:
        - question: "How do you want to proceed with the review findings?"
          header: "Post-Review Action"
          multiSelect: false
          options:
            - label: "Fix all issues"
              description: "Generate a plan for all findings and run /do"
              action: "Generate .claude/plans/review-fixes-{timestamp}.md with ALL findings"
            - label: "Fix critical/high only"
              description: "Focus on HIGH+ severity findings"
              action: "Filter findings to HIGH+, generate plan"
            - label: "Investigate unclear findings"
              description: "Deep-dive into findings with < 85% confidence"
              action: "Re-analyze low-confidence findings with extended context"
            - label: "Done"
              description: "End review without action"
              action: "Exit review"
```

---

## Output Format

```markdown
# Code Review: PR #{number}

## Summary
{1-2 sentences assessment}
Mode: {NORMAL|TRIAGE}
CI: {status}

## Critical Issues
> Must fix before merge

### [CRITICAL] `file:line` - Title
**Problem:** {description}
**Evidence:** {code snippet, REDACTED if secret}
**Fix:** {actionable recommendation}
**Confidence:** HIGH

## High Priority
> From our analysis + validated bot suggestions

## Medium
> Quality improvements (max 5)

## Low
> Style/polish (max 3)

## Shell Safety (if *.sh present)

### Download Safety
| Check | Status | File:Line |
|-------|--------|-----------|

### Path Determinism
| Config | Issue | Fix |
|--------|-------|-----|

## Pattern Analysis (CONDITIONAL)
> Triggered only if: complexity increase, duplication, or core/ touched

### Patterns Identified
| Pattern | Location | Status |

### Suggestions
| Problem | Pattern | Reference |

## Challenged Feedback
| Suggestion | Source | Verdict | Rationale |
|------------|--------|---------|-----------|

## Questions (pending)
| Author | Question | Proposed Answer |

## Commendations
> What's done well

## Metrics
| Metric | Value |
|--------|-------|
| Mode | NORMAL |
| Files reviewed | 18 |
| Lines | +375/-12 |
| Critical | 0 |
| High | 3 |
| Medium | 2 |
| Suggestions kept | 3/8 |
```

---

## Pattern Consultation (CONDITIONAL)

**Source:** `~/.claude/docs/` (Design Patterns Knowledge Base)

**Trigger ONLY if:**

```yaml
pattern_triggers:
  source: "~/.claude/docs/"
  index: "~/.claude/docs/README.md"

  conditions:
    - "complexity_increase > 20%"
    - "duplication_detected"
    - "directories: core/, domain/, pkg/, internal/"
    - "new_classes > 3"
    - "file size > 500 lines"

  skip_if:
    - "only docs/config changes"
    - "test files only"
    - "mode == TRIAGE"

  workflow:
    1_identify: "Read ~/.claude/docs/README.md to identify category"
    2_consult: "Read(~/.claude/docs/<category>/README.md)"
    3_analyze: "Verify used patterns vs recommended patterns"
    4_report: "Include in 'Pattern Analysis' section"

  language_aware:
    go: "No 'class' keyword, check interfaces/structs"
    ts_js: "Factory, Singleton, Observer patterns"
    python: "Metaclass, decorator patterns"
    shell: "N/A - skip pattern analysis"
```
