# Improve - Continuous Enhancement (RLM Multi-Agent)

## Description

Automatic continuous improvement. Detects context and acts.

```
/improve
```

**No arguments.** The skill auto-detects the mode.

---

## Modes (Auto-detected)

```
┌─────────────────────────────────────────────────────────────────────┐
│                            /improve                                  │
├──────────────────────────────┬──────────────────────────────────────┤
│  kodflow/devcontainer-template│  Other project                      │
│  ══════════════════════════════│══════════════════════════════════  │
│                               │                                      │
│  → Improve ~/.claude/docs/    │  → Analyze the code                  │
│    ├─ Update best practices   │    ├─ Detect anti-patterns           │
│    ├─ Fix inconsistencies     │    ├─ Compare with ~/.claude/docs/   │
│    ├─ Refine examples         │    ├─ Find best practices            │
│    └─ WebSearch validations   │    └─ Create issues on template      │
│                               │                                      │
│  Output: Modified files       │  Output: GitHub issues created       │
└──────────────────────────────┴──────────────────────────────────────┘
```

---

## Execution Mode Detection (Agent Teams)

@.devcontainer/images/.claude/commands/shared/team-mode.md

Before Phase 3 (parallel violation detection), determine runtime mode:

```bash
source "$HOME/.claude/scripts/team-mode-primitives.sh"
MODE=$(detect_runtime_mode)
```

Branch:
- `TEAMS_TMUX` / `TEAMS_INPROCESS` → **TEAMS improvement dispatch** (4 parallel axes)
- `SUBAGENTS` → legacy sequential/Task dispatch (unchanged)

### TEAMS improvement dispatch

Lead: `developer-orchestrator`. Spawn 4 axis teammates:

```text
TaskCreate × 4:
  improve-design    → using developer-executor-design    (antipatterns, SOLID, DDD)
  improve-quality   → using developer-executor-quality   (complexity, smells, style)
  improve-security  → using developer-executor-security  (OWASP, taint analysis)
  improve-shell     → using developer-executor-shell     (shell/Dockerfile/CI safety)
```

Each task embeds a task-contract v1 block with `access_mode` matching the current mode (read-only for detection, write with explicit `owned_paths` from the scan for auto-fix). Token ceiling ≤ 2.5x legacy.

---

## RLM Workflow

### Phase 1: Context detection

```yaml
detection:
  command: git remote get-url origin 2>/dev/null

  rules:
    - if: "contains 'kodflow/devcontainer-template'"
      mode: "DOCS_IMPROVEMENT"
      scope: "~/.claude/docs/**/*.md"
      action: "Improve pattern documentation"

    - else:
      mode: "ANTI_PATTERN_DETECTION"
      scope: "**/*.{md,ts,js,py,go,rs,java,rb,php}"
      action: "Detect violations and create issues"
      target: "github.com/kodflow/devcontainer-template/issues"
```

---

### Phase 2: Inventory (Partition)

```yaml
inventory:
  mode_docs:
    action: Glob("~/.claude/docs/**/*.md")
    group_by: category (principles, creational, behavioral, etc.)

  mode_antipattern:
    action: |
      Glob("**/*.md")
      Glob("**/*.{ts,js,py,go,rs,java,rb,php}")
    group_by: file_type
```

---

### Phase 3: Parallel agents (Map)

**Launches 1 agent per file, max 20 in parallel.**

```yaml
parallel_execution:
  max_agents: 20
  model: haiku  # Fast

  mode_docs:
    prompt_per_file: |
      FILE: {path}
      CATEGORY: {category}

      TASKS:
      1. Read current content
      2. Identify possible improvements:
         - Outdated info
         - Missing examples
         - Inconsistencies
      3. WebSearch "{pattern} best practices {current_year}"
      4. Propose fixes

      OUTPUT JSON:
      {
        "file": "{path}",
        "status": "OK | UPDATE | OUTDATED",
        "improvements": [{
          "type": "content | example | fix",
          "current": "...",
          "proposed": "...",
          "source": "url"
        }]
      }

  mode_antipattern:
    prompt_per_file: |
      FILE: {path}
      REFERENCE: ~/.claude/docs/

      TASKS:
      1. Read the code
      2. Compare with documented patterns
      3. Detect:
         - Violations (anti-patterns)
         - Missing patterns
         - Best practices worth documenting

      OUTPUT JSON:
      {
        "file": "{path}",
        "violations": [{
          "pattern": "name",
          "severity": "HIGH | MEDIUM | LOW",
          "description": "...",
          "code": "...",
          "fix": "..."
        }],
        "positive": [{
          "description": "...",
          "code": "...",
          "worth_documenting": true
        }]
      }
```

---

### Phase 4: Validation (WebSearch)

```yaml
validation:
  for_each_improvement:
    search: "{pattern} best practices" (use current year dynamically)
    sources:
      - Official docs (go.dev, docs.python.org, etc.)
      - martinfowler.com, refactoring.guru
      - owasp.org (security)

    confidence:
      - 3+ sources: VALIDATED
      - 2 sources: MEDIUM
      - 1 source: LOW (flag)
      - 0 source: SKIP
```

---

### Phase 5: Application

```yaml
application:
  mode_docs:
    action: |
      FOR each VALIDATED improvement:
        Edit(file, old, new)
      Display modification summary

  mode_antipattern:
    action: |
      FOR each HIGH/MEDIUM violation:
        mcp__github__issue_write(
          owner: "kodflow",
          repo: "devcontainer-template",
          title: "pattern: {description}",
          body: "## Violation\n{details}\n## Code\n```\n{code}\n```\n## Fix\n{suggestion}",
          labels: ["documentation", "improvement", "auto-generated"]
        )

      FOR each positive worth_documenting:
        mcp__github__issue_write(
          owner: "kodflow",
          repo: "devcontainer-template",
          title: "new-pattern: {description}",
          body: "## New Pattern\n{description}\n## Context\n{context}",
          labels: ["new-pattern", "auto-generated"]
        )

      Display list of created issues
```

---

### Phase 6: Report

```yaml
report:
  mode_docs:
    output: |
      ═══════════════════════════════════════════════
        /improve - Documentation Enhancement Complete
      ═══════════════════════════════════════════════

        Files analyzed: {total}

        Results:
          ✓ OK: {ok}
          ⚠ Updated: {updated}
          ✗ Outdated: {outdated}

        Changes applied: {changes}

      ═══════════════════════════════════════════════

  mode_antipattern:
    output: |
      ═══════════════════════════════════════════════
        /improve - Anti-Pattern Detection Complete
      ═══════════════════════════════════════════════

        Repository: {repo}
        Files analyzed: {total}

        Violations:
          🔴 HIGH: {high}
          🟡 MEDIUM: {medium}
          🟢 LOW: {low}

        Positive patterns: {positive}

        Issues created: {issues}
          {issue_list}

      ═══════════════════════════════════════════════
```

---

## Pattern categories (~/.claude/docs/)

| Category | Scope |
|----------|-------|
| principles | SOLID, DRY, KISS, YAGNI |
| creational | Factory, Builder, Singleton |
| structural | Adapter, Decorator, Proxy |
| behavioral | Observer, Strategy, Command |
| performance | Cache, Lazy Load, Pool |
| concurrency | Thread Pool, Actor, Mutex |
| enterprise | PoEAA (Martin Fowler) |
| messaging | EIP patterns |
| ddd | Aggregate, Entity, Repository |
| functional | Monad, Functor, Either |
| architectural | Hexagonal, CQRS |
| cloud | Circuit Breaker, Saga |
| resilience | Retry, Timeout, Bulkhead |
| security | OAuth, JWT, RBAC |
| testing | Mock, Stub, Fixture |
| devops | GitOps, IaC, Blue-Green |

---

## Violation detection

| Type | Description |
|------|-------------|
| SOLID_VIOLATION | God class, poor coupling |
| DRY_VIOLATION | Duplicated code |
| MISSING_PATTERN | Missing but needed pattern |
| SECURITY | Vulnerabilities, hardcoded secrets |
| PERFORMANCE | N+1, missing cache |
| ERROR_HANDLING | Silent catch, missing retry |

---

## Guardrails (ABSOLUTE)

| Action | Status |
|--------|--------|
| Modify without WebSearch validation | FORBIDDEN |
| Create issue without code excerpt | FORBIDDEN |
| Sequential agents (when parallelizable) | FORBIDDEN |
| Issues on repo other than template | FORBIDDEN |
