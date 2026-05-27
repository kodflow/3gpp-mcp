# Phase 1.0: Peek (RLM Pattern)

**Quick scan BEFORE deep exploration:**

```yaml
peek_workflow:
  0_recover_context:
    rule: "Before exploring, check .claude/contexts/*.md for related research"
    action: "Glob .claude/contexts/*.md — read most recent or matching slug"
    importance: "CRITICAL after context compaction — research survives on disk"

  1_context_check:
    action: "Check if .claude/contexts/*.md exists (--context flag or auto-detect)"
    tool: [Glob, Read]
    output: "context_available"
    logic:
      "--context=<name>": "Read .claude/contexts/{name}.md"
      "--context (no value)": "Read most recent .claude/contexts/*.md"
      "no flag": "Check if any .claude/contexts/*.md matches description keywords"

  2_structure_scan:
    action: "Scan project structure"
    tools: [Glob]
    patterns:
      - "src/**/*"
      - "tests/**/*"
      - "package.json | go.mod | Cargo.toml"

  3_pattern_grep:
    action: "Identify relevant patterns"
    tools: [Grep]
    searches:
      - Keywords from description
      - Related function names
      - Existing patterns
```

**Phase 1 Output:**

```
═══════════════════════════════════════════════════════════════
  /plan - Peek Analysis
═══════════════════════════════════════════════════════════════

  Description: "Add user authentication with JWT"

  Context:
    ✓ .claude/contexts/{slug}.md loaded (from /search)
    ✓ 47 source files scanned
    ✓ 23 test files found

  Patterns identified:
    - Existing auth: src/middleware/auth.ts
    - User model: src/models/user.ts
    - Routes: src/routes/*.ts

  Keywords matched: 15 occurrences

═══════════════════════════════════════════════════════════════
```

---

## Phase 1.5: Validate Scope (INTERACTIVE CHECKPOINT)

**Skip if `--auto` mode. In auto mode, AI decides scope internally and documents reasoning.**

```yaml
validate_scope:
  tool: AskUserQuestion
  trigger: "After Phase 1.0 output — SKIP if --auto flag"
  present: "The Peek Analysis output above"
  question: "Le scope identifie est-il correct ?"
  options:
    - label: "Oui, continue"
      description: "Decomposer la tache avec ce scope"
    - label: "Elargir le scope"
      description: "Ajouter des repertoires ou fichiers a explorer"
    - label: "Restreindre le scope"
      description: "Se concentrer sur un sous-ensemble"

  on_elargir:
    action: "Ask: Quels repertoires/fichiers ajouter ?"
    then: "Re-run Phase 1.0 with expanded scope"

  on_restreindre:
    action: "Ask: Quel sous-ensemble garder ?"
    then: "Filter Phase 1.0 results, continue to Phase 2.0"
```

---

## Phase 2.0: Decompose (RLM Pattern)

**Split the task into subtasks:**

```yaml
decompose_workflow:
  1_analyze_description:
    action: "Extract objectives"
    example:
      description: "Add user authentication with JWT"
      objectives:
        - "Setup JWT utilities"
        - "Create auth middleware"
        - "Add login/logout endpoints"
        - "Protect existing routes"
        - "Add tests"

  2_identify_domains:
    action: "Categorize by domain"
    domains:
      - backend: "API, middleware, database"
      - frontend: "UI components, state"
      - infrastructure: "config, deployment"
      - testing: "unit, integration, e2e"

  3_order_dependencies:
    action: "Order by dependency"
    output: "ordered_tasks[]"
```

---

## Phase 2.5: Validate Objectives (INTERACTIVE CHECKPOINT)

**Skip if `--auto` mode. In auto mode, AI validates objectives internally.**

```yaml
validate_objectives:
  tool: AskUserQuestion
  trigger: "After Phase 2.0 — SKIP if --auto flag"
  present: |
    Numbered list of objectives with:
    - Priority order
    - Domain (backend/frontend/infra/test)
    - Dependencies between objectives
  question: "Est-ce le bon decoupage ?"
  options:
    - label: "Oui, lance l'exploration"
      description: "Explorer le codebase avec ces objectifs"
    - label: "Modifier les objectifs"
      description: "Ajouter, supprimer ou reformuler des objectifs"
    - label: "Reordonner les priorites"
      description: "Changer l'ordre d'execution"

  on_modifier:
    action: "Incorporate changes, update objectives list"
    then: "Re-present for validation"

  on_reordonner:
    action: "Ask: Quel ordre preferes-tu ?"
    then: "Reorder and re-present"
```

---

## Phase 3.0: Parallelize (RLM Pattern)

**Multi-domain exploration in parallel:**

```yaml
parallel_exploration:
  mode: "PARALLEL (single message, multiple Task calls)"

  agents:
    # PR7 — Skills Architecture v1.3: 4 explorers routed via route-agent.sh
    # phase=explore. Default to per-language specialist via agent_template;
    # docs-analyzer-patterns handles the local KB consult.
    - task: "backend-explorer"
      type: "docs-analyzer-architecture"   # was Explore
      prompt: |
        Analyze backend for: {description}
        Find: related files, existing patterns, dependencies
        Return: {files[], patterns[], recommendations[]}

    - task: "frontend-explorer"
      type: "docs-analyzer-commands"       # was Explore
      prompt: |
        Analyze frontend for: {description}
        Find: components, state, API calls
        Return: {files[], components[], state_management}

    - task: "test-explorer"
      type: "docs-analyzer-agents"         # was Explore
      prompt: |
        Analyze tests for: {description}
        Find: existing coverage, test patterns
        Return: {coverage, patterns[], gaps[]}

    - task: "patterns-consultant"
      type: "docs-analyzer-patterns"       # was Explore (local KB)
      prompt: |
        Consult ~/.claude/docs/ for: {description}
        Find: applicable design patterns
        Return: {patterns[], references[]}
```

**IMPORTANT**: Launch ALL agents in a SINGLE message.
