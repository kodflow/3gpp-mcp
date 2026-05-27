# Test Generation & Execution Workflow

## Phase 1.0: Peek (RLM Pattern)

**Analyze the page BEFORE interaction:**

```yaml
peek_workflow:
  1_navigate:
    tool: mcp__playwright__browser_navigate
    params:
      url: "<url>"

  2_snapshot:
    tool: mcp__playwright__browser_snapshot
    output: "Accessibility tree of the page"

  3_analyze:
    action: "Identify interactive elements"
    extract:
      - forms: "input, select, textarea"
      - buttons: "button, [type=submit]"
      - links: "a[href]"
      - content: "main content areas"
```

**Phase 1 Output:**

```
═══════════════════════════════════════════════════════════════
  /test - Peek Analysis
═══════════════════════════════════════════════════════════════

  URL: https://myapp.com/login

  Page Structure:
    ├─ Header (nav, logo, menu)
    ├─ Main
    │   ├─ Form#login
    │   │   ├─ Input[email]
    │   │   ├─ Input[password]
    │   │   └─ Button[Submit]
    │   └─ Link[Forgot password]
    └─ Footer

  Interactive Elements: 5
  Forms: 1
  Testable: YES

═══════════════════════════════════════════════════════════════
```

---

## Phase 2.0: Decompose (RLM Pattern)

**Split the test into steps:**

```yaml
decompose_workflow:
  example_login_test:
    steps:
      - step: "Navigate to login"
        action: browser_navigate
        url: "/login"

      - step: "Fill email"
        action: browser_type
        element: "Email input"
        value: "user@test.com"

      - step: "Fill password"
        action: browser_type
        element: "Password input"
        value: "******"

      - step: "Submit form"
        action: browser_click
        element: "Submit button"

      - step: "Verify redirect"
        action: browser_expect
        expectation: "URL contains /dashboard"
```

---

## Phase 3.0: Parallelize (RLM Pattern)

**Simultaneous assertions and captures:**

```yaml
parallel_validation:
  mode: "PARALLEL (single message, multiple MCP calls)"

  actions:
    - task: "Visibility check"
      tool: mcp__playwright__browser_expect
      params:
        expectation: "to_be_visible"
        ref: "<dashboard_ref>"

    - task: "Text check"
      tool: mcp__playwright__browser_expect
      params:
        expectation: "to_have_text"
        ref: "<welcome_ref>"
        expected: "Welcome"

    - task: "Screenshot"
      tool: mcp__playwright__browser_screenshot
      params:
        fullPage: true
```

**IMPORTANT**: Launch ALL assertions in a SINGLE message.

---

## Phase 4.0: Synthesize (RLM Pattern)

**Consolidated test report:**

```yaml
synthesize_workflow:
  1_collect:
    action: "Gather all results"
    data:
      - step_results
      - assertions_passed
      - screenshots
      - timing

  2_analyze:
    action: "Identify failures and root causes"

  3_generate_report:
    format: "Structured test report"
```

**Final Output:**

```
═══════════════════════════════════════════════════════════════
  /test - Test Report
═══════════════════════════════════════════════════════════════

  URL: https://myapp.com/login
  Scenario: Login flow

  Steps:
    Navigate to /login (245ms)
    Fill email input (32ms)
    Fill password input (28ms)
    Click submit button (156ms)
    Verify dashboard redirect (1.2s)

  Assertions:
    Dashboard visible
    Welcome message present
    User avatar displayed

  Artifacts:
    - Screenshot: /tmp/test-login-success.png
    - Trace: /tmp/trace-login.zip

  Result: PASS (5/5 steps, 3/3 assertions)

═══════════════════════════════════════════════════════════════
```

---

## Workflows

### --run (Execute project tests)

```yaml
run_workflow:
  1_peek:
    action: "Scan test files"
    tools: [Glob]
    patterns: ["**/*.spec.ts", "**/*.test.ts", "**/e2e/**"]

  2_decompose:
    action: "Categorize tests"
    categories:
      - unit: "**/unit/**"
      - integration: "**/integration/**"
      - e2e: "**/e2e/**"

  3_parallelize:
    action: "Run test suites in parallel"
    tools: [Task agents]

  4_synthesize:
    action: "Consolidated test report"
```

### --trace (Debug with tracing)

```yaml
trace_workflow:
  1_start:
    tool: mcp__playwright__browser_start_tracing
    params:
      name: "debug-session"

  2_interact:
    action: "Perform interactions"

  3_stop:
    tool: mcp__playwright__browser_stop_tracing
    output: "trace.zip (viewable in trace.playwright.dev)"
```

### --codegen (Generate test code)

```yaml
codegen_workflow:
  1_peek:
    action: "Analyze page structure"

  2_record:
    action: "Record interactions"

  3_synthesize:
    action: "Generate Playwright test code"
    output: "*.spec.ts file"
```
