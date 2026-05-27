# T2 Qodo + T3 CodeRabbit Integration (External Tiers)

## External Tiers (T2 + T3) — Parallel with T1

**Launch external review tools in parallel with the 5 internal agents:**

```yaml
external_tiers:
  dispatch:
    mode: "parallel with T1 (2 Bash calls alongside 5 Task calls)"
    tier_filter: "--tier flag controls which tiers run (default: all)"

    qodo:
      tier: "T2"
      trigger: "command -v qodo && QODO_API_KEY is set"
      command: |
        qodo run review "Review the diff from {base_branch}. Focus on P0/P1 only. Structured output." \
          --ci --yes --silent --log=/tmp/qodo-review-{timestamp}.md \
          --tools=git,filesystem,ripgrep
      timeout: 120000  # 2 minutes max
      fallback: "skip (T2 unavailable)"

    coderabbit:
      tier: "T3"
      trigger: "command -v coderabbit && ~/.coderabbit/auth.json exists"
      command: |
        coderabbit review --plain --no-color --type committed \
          --base "{base_branch}" --cwd /workspace \
          > /tmp/coderabbit-review-{timestamp}.md 2>&1
      timeout: 120000  # 2 minutes max
      fallback: "skip (T3 unavailable)"
```

**Tier Selection:**

| `--tier` Value | T1 (Agents) | T2 (Qodo) | T3 (CodeRabbit) |
|----------------|-------------|-----------|-----------------|
| `all` (default) | Yes | Yes | Yes |
| `internal` | Yes | No | No |
| `external` | No | Yes | Yes |
| `qodo` | No | Yes | No |
| `coderabbit` | No | No | Yes |

---

## Agents Architecture

```
/review (15 phases, 5 agents)
    │
    ├── Phase 0-2.5: Context + Feedback (sequential)
    │     ├── 0: Context Detection (GitHub/GitLab auto)
    │     ├── 0.5: Repo Profile (cached 7d)
    │     ├── 1: Intent + Risk Model
    │     ├── 1.5: Auto-Describe (drift detection)
    │     ├── 2: Feedback Collection
    │     ├── 2.3: CI Diagnostics (conditional)
    │     └── 2.5: Question Handling
    │
    ├── Phase 3-4.7: Parallel Analysis
    │       │
    │       ├── 3: Peek & Route (categorize files)
    │       │
    │       ├── 4: PARALLEL (5 agents)
    │       │       │
    │       │       ├── developer-executor-correctness (sonnet)
    │       │       │     Focus: Invariants, bounds, state, concurrency
    │       │       │     Output: oracle, failure_mode, repro
    │       │       │
    │       │       ├── developer-executor-security (opus)
    │       │       │     Focus: OWASP, taint analysis, supply chain
    │       │       │     Output: source, sink, taint_path, CWE refs
    │       │       │
    │       │       ├── developer-executor-design (sonnet)
    │       │       │     Focus: Antipatterns, DDD, layering, SOLID
    │       │       │     Output: pattern_reference, official_reference
    │       │       │
    │       │       ├── developer-executor-quality (haiku)
    │       │       │     Focus: Complexity, duplication, style, DTOs
    │       │       │
    │       │       └── developer-executor-shell (haiku)
    │       │             Condition: *.sh OR Dockerfile exists
    │       │             Focus: 6 shell safety axes
    │       │
    │       └── 4.7: Merge & Dedupe (normalize, evidence-required)
    │
    ├── Phase 5: Challenge (with full context)
    │
    ├── Phase 6-6.5: Output (LOCAL ONLY)
    │       ├── 6: Generate report + /plan file
    │       └── 6.5: Dispatch to language-specialist via /do
    │
    └── Phase 7: Cyclic Validation (--loop)
          Loop: review → fix → review until perfect
```
