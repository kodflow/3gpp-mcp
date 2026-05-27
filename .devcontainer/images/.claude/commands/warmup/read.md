# Funnel Read Strategy (Phase 2-4)

## Phase 2.0: Funnel (Funnel Reading)

```yaml
funnel_strategy:
  principle: "Read from most general to most specific"

  levels:
    depth_0:
      files: ["/CLAUDE.md"]
      extract: ["project_rules", "structure", "workflow", "safeguards"]
      detail_level: "HIGH"

    depth_1:
      files: ["src/CLAUDE.md", ".devcontainer/CLAUDE.md"]
      extract: ["conventions", "key_files", "domain_rules"]
      detail_level: "MEDIUM"

    depth_2_plus:
      files: ["**/CLAUDE.md"]
      extract: ["specific_rules", "attention_points"]
      detail_level: "LOW"

  extraction_rules:
    include:
      - "MANDATORY/ABSOLUTE rules"
      - "Directory structure"
      - "Specific conventions"
      - "Guardrails"
    exclude:
      - "Complete code examples"
      - "Implementation details"
      - "Long code blocks"
```

**Reading algorithm:**

```
FOR depth FROM 0 TO max_depth:
    files = filter(claude_files, depth)

    PARALLEL FOR each file IN files:
        content = Read(file)
        context[file] = extract_essential(content, detail_level)

    consolidate(context, depth)
```

---

## Phase 3.0: Parallelize (Analysis by Domain)

```yaml
parallel_analysis:
  mode: "PARALLEL (single message, 4 Task calls)"

  agents:
    - task: "source-analyzer"
      type: "docs-analyzer-architecture"   # PR4 — routed via route-agent.sh phase=read-architecture
      scope: "src/"
      prompt: |
        Analyze the source code structure:
        - Main packages/modules
        - Detected architectural patterns
        - Attention points (TODO, FIXME, HACK)
        Return: {packages[], patterns[], attention_points[]}

    - task: "config-analyzer"
      type: "docs-analyzer-commands"       # PR4 — routed via route-agent.sh phase=read-commands
      scope: ".devcontainer/"
      prompt: |
        Analyze the DevContainer configuration:
        - Installed features
        - Configured services
        - Available MCP servers
        Return: {features[], services[], mcp_servers[]}

    - task: "test-analyzer"
      type: "docs-analyzer-agents"         # PR4 — routed via route-agent.sh phase=read-agents
      scope: "tests/ OR **/*_test.go OR **/*.test.ts"
      prompt: |
        Analyze the test coverage:
        - Test files found
        - Test patterns used
        Return: {test_files[], patterns[], coverage_estimate}

    - task: "docs-analyzer"
      type: "docs-analyzer-hooks"          # PR4 — routed via route-agent.sh phase=read-hooks
      scope: "~/.claude/docs/"
      prompt: |
        Analyze the knowledge base:
        - Available pattern categories
        - Number of patterns per category
        Return: {categories[], pattern_count}
```

**IMPORTANT**: Launch all 4 agents in ONE SINGLE message.

---

## Phase 4.0: Synthesize (Consolidated Context)

```yaml
synthesize_workflow:
  1_merge:
    action: "Merge agent results"
    inputs:
      - "context_tree (Phase 2)"
      - "source_analysis (Phase 3)"
      - "config_analysis (Phase 3)"
      - "test_analysis (Phase 3)"
      - "docs_analysis (Phase 3)"

  2_prioritize:
    action: "Prioritize information"
    levels:
      - CRITICAL: "Absolute rules, guardrails, mandatory conventions"
      - HIGH: "Project structure, patterns used, available MCP"
      - MEDIUM: "Features, services, test coverage"
      - LOW: "Specific details, minor attention points"

  3_format:
    action: "Format context for session"
    output: "Session context ready"
```

**Final Output (Normal Mode):**

```
═══════════════════════════════════════════════════════════════
  /warmup - Context Loaded Successfully
═══════════════════════════════════════════════════════════════

  Project: <project_name>
  Type   : <detected_type>

  Context Summary:
    ├─ CLAUDE.md files read: <n>
    ├─ Source packages: <n>
    ├─ Test files: <n>
    ├─ Design patterns: <n>
    └─ MCP servers: <n>

  Key Rules Loaded:
    OK MCP-FIRST: Always use MCP before CLI
    OK RTK-FIRST: PreToolUse hook compresses Bash output (60-90% token savings)
    OK Code in /src: All code MUST be in /src
    OK SAFEGUARDS: Never delete .claude/ or .devcontainer/

  Attention Points Detected:
    ├─ <n> TODO items in src/
    ├─ <n> FIXME in config
    └─ <n> deprecated APIs flagged

  Ready for:
    → /plan <feature>
    → /review
    → /do <task>

  Skill Discipline:
    Red flags (NEVER rationalize these):
    - "This is just a simple question" → Questions are tasks
    - "I remember this skill" → Skills evolve, read current version
    - "The skill is overkill" → Simple tasks become complex
    - "I'll do one thing first" → Check skills BEFORE acting

═══════════════════════════════════════════════════════════════
```
