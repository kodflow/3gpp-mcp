# Phase 5.0: Decompose (RLM Pattern)

**Split the task into measurable sub-objectives:**

```yaml
decompose_workflow:
  1_analyze_task:
    action: "Extract the objectives from the task"
    example:
      task: "Migrate Jest to Vitest"
      objectives:
        - "Replace Jest dependencies with Vitest"
        - "Update the test config"
        - "Adapt imports in test files"
        - "Fix incompatible APIs"
        - "Verify that all tests pass"

  2_prioritize:
    action: "Order by dependency"
    principle: "Smallest change first"

  3_create_todos:
    action: "Initialize TaskCreate with sub-objectives"
```

**Output Phase 5.0:**

```
═══════════════════════════════════════════════════════════════
  /do - Task Decomposition
═══════════════════════════════════════════════════════════════

  Task: "Migrate Jest tests to Vitest"

  Sub-objectives (ordered):
    1. [DEPS] Replace jest → vitest in package.json
    2. [CONFIG] Create vitest.config.ts
    3. [IMPORTS] Adapt imports jest → vitest (23 files)
    4. [COMPAT] Fix incompatible APIs
    5. [VERIFY] All tests pass

  Strategy: Sequential with parallel validation

═══════════════════════════════════════════════════════════════
```
