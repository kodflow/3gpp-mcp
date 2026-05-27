---
name: test
description: |
  E2E and frontend testing with Playwright MCP and RLM decomposition.
  Automates browser interactions, visual testing, and debugging.
  Use when: running E2E tests, debugging frontend, generating test code.
allowed-tools:
  - "mcp__playwright__*"
  - "Bash(npm:*)"
  - "Bash(npx:*)"
  - "Read(**/*)"
  - "Write(**/*)"
  - "Glob(**/*)"
  - "mcp__context7__*"
  - "Grep(**/*)"
  - "Task(*)"
---

# /test - E2E & Frontend Testing (RLM Architecture)

$ARGUMENTS

## CONTEXT7 (RECOMMENDED)

Use `mcp__context7__resolve-library-id` + `mcp__context7__query-docs` to:
- Verify Playwright API usage and available selectors
- Check test framework APIs (Jest, Vitest, pytest, Go testing)
- Validate assertion library patterns

---

## Overview

E2E tests and frontend debugging with **RLM** patterns:

- **Peek** - Analyze the page before interaction
- **Decompose** - Split the test into steps
- **Parallelize** - Simultaneous assertions and captures
- **Synthesize** - Consolidated test report

---

## Arguments

| Pattern | Action |
|---------|--------|
| `<url>` | Open the URL and explore the page |
| `--run` | Run the project's Playwright tests |
| `--tdd` | TDD mode: RED-GREEN-REFACTOR cycle enforcement |
| `--debug <url>` | Interactive debug mode |
| `--trace` | Enable tracing for the session |
| `--screenshot <url>` | Screenshot the page |
| `--pdf <url>` | Generate a PDF of the page |
| `--codegen <url>` | Generate test code |
| `--help` | Show help |

---

## --help

```
═══════════════════════════════════════════════════════════════
  /test - E2E & Frontend Testing (RLM)
═══════════════════════════════════════════════════════════════

Usage: /test <url|action> [options]

Actions:
  <url>               Open and explore the page
  --run               Run project tests
  --debug <url>       Interactive debug mode
  --trace             Enable tracing
  --screenshot <url>  Screenshot
  --pdf <url>         Generate a PDF
  --codegen <url>     Generate test code

RLM Patterns:
  1. Peek       - Analyze the page (snapshot)
  2. Decompose  - Split into test steps
  3. Parallelize - Simultaneous assertions
  4. Synthesize - Consolidated report

MCP Tools:
  browser_navigate    Open a URL
  browser_click       Click element
  browser_type        Type text
  browser_snapshot    Capture state
  browser_expect      Assertions

Examples:
  /test https://example.com
  /test --screenshot https://myapp.com/login
  /test --run
  /test --codegen https://myapp.com

═══════════════════════════════════════════════════════════════
```

---

## Module Reference

| Action | Module |
|--------|--------|
| MCP tools & guardrails | Read ~/.claude/commands/test/playwright.md |
| RLM phases & test workflows | Read ~/.claude/commands/test/workflow.md |

---

## Execution Mode Detection (Agent Teams)

@.devcontainer/images/.claude/commands/shared/team-mode.md

Before dispatching test suites, determine runtime mode:

```bash
source "$HOME/.claude/scripts/team-mode-primitives.sh"
MODE=$(detect_runtime_mode)
```

Branch:
- `TEAMS_TMUX` / `TEAMS_INPROCESS` → **TEAMS test dispatch** (3 parallel suites)
- `SUBAGENTS` → legacy sequential/Task dispatch from `test/workflow.md` (unchanged)

### TEAMS test dispatch

Lead: `developer-orchestrator`. Spawn up to 3 suite teammates:

```text
TaskCreate × 3:
  test-unit         → using developer-specialist-<detected-lang>
                      scope: tests/unit/ (or project's unit test path)
                      access_mode: read-only (analysis) OR write (if --generate)
  test-integration  → using developer-specialist-<detected-lang>
                      scope: tests/integration/
                      access_mode: read-only OR write
  test-e2e          → using developer-specialist-playwright  # PR7 — was developer-specialist-review fallback
                      scope: tests/e2e/ + Playwright traces
                      access_mode: read-only
```

### Framework routing (PR7 — Skills Architecture v1.3)

```bash
# Test framework dispatch via route-agent.sh phase=e2e
ROUTER=~/.claude/scripts/route-agent.sh
DISPATCH=$(bash "$ROUTER" --skill /test --phase e2e \
  --profile .claude/state/profile.json)
# Returns developer-specialist-playwright when test_frameworks contains "playwright".
```

Wait for all 3 → aggregate coverage + failures → Phase 4.0 synthesis. Token ceiling ≤ 2x (test suites parallelize naturally).

---

## Routing

1. **Any URL action**: Start with Phase 1.0 Peek from `workflow.md`
2. **--run / --trace / --codegen**: Execute specific workflow from `workflow.md`
3. **--tdd**: Execute TDD workflow below
4. **MCP tool reference**: Refer to `playwright.md` for tool details
5. **Guardrails**: Refer to `playwright.md` for safety rules

---

## TDD Mode (`--tdd`)

Test-Driven Development cycle enforcement. Use when building new features or fixing bugs.

**Iron Law:** If you didn't watch the test fail, you don't know if it tests the right thing.

### RED-GREEN-REFACTOR Cycle

```yaml
tdd_cycle:
  RED:
    action: "Write ONE minimal failing test showing expected behavior"
    test_name: "Descriptive name of the behavior being tested"
    use_real_code: "No mocks unless absolutely unavoidable"

  VERIFY_RED:
    action: "Run test suite, confirm the NEW test FAILS"
    check:
      - "Failure is for the RIGHT reason (feature missing, not typo)"
      - "Failure message is meaningful and expected"
      - "If test passes → you're testing existing behavior, fix the test"

  GREEN:
    action: "Write the MINIMAL code to make the test pass"
    rules:
      - "Simplest possible implementation"
      - "Don't add features beyond what the test requires"
      - "Don't refactor during this step"
      - "Don't improve other code"

  VERIFY_GREEN:
    action: "Run test suite, confirm the NEW test PASSES"
    check:
      - "New test passes"
      - "ALL other tests still pass"
      - "No warnings or errors in output"

  REFACTOR:
    action: "Clean up code while keeping tests green"
    allowed:
      - "Remove duplication"
      - "Improve naming"
      - "Extract helpers"
    forbidden:
      - "Adding new behavior"
      - "Changing test expectations"

  COMMIT:
    action: "Commit after each green-refactor cycle"
```

### TDD Anti-patterns (STOP immediately)

| Pattern | Problem |
|---------|---------|
| Writing code before test | Proves nothing when test passes |
| Test passes immediately | You're testing existing behavior |
| Multiple changes before running | Can't isolate what worked |
| "Too simple to test" | Simple code breaks. Test takes 30 seconds. |
| "I'll test after" | Tests-after answer "what does it do?" not "what should it do?" |
| "Keep code as reference, write tests" | You'll adapt it. That's testing after. |

### TDD Completion Checklist

```text
- [ ] Every new function/method has a test
- [ ] Watched each test fail before implementing
- [ ] Each test failed for expected reason (not typo)
- [ ] Wrote minimal code to pass each test
- [ ] All tests pass
- [ ] Output pristine (no errors, warnings)
- [ ] Tests use real code (mocks only if unavoidable)
- [ ] Edge cases and error paths covered
```
