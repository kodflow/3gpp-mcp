# CLAUDE.md Hierarchy Scan

## Phase 1.0: Peek (Hierarchy Discovery)

```yaml
peek_workflow:
  1_discover:
    action: "Discover all CLAUDE.md files in the project"
    tool: Glob
    pattern: "**/CLAUDE.md"
    output: [claude_files]

  2_build_tree:
    action: "Build the context tree by depth"
    algorithm: |
      FOR each file:
        depth = path.count('/') - base.count('/')
      Sort by ascending depth
      depth 0: /CLAUDE.md (root)
      depth 1: /src/CLAUDE.md, /.devcontainer/CLAUDE.md
      depth 2+: subdirectories

  3_detect_project:
    action: "Identify the project type"
    tools: [Glob]
    patterns:
      - "go.mod" → Go
      - "package.json" → Node.js
      - "Cargo.toml" → Rust
      - "pyproject.toml" → Python
      - "*.tf" → Terraform
      - "pom.xml" → Java (Maven)
      - "build.gradle" → Java/Kotlin (Gradle)
      - "build.sbt" → Scala
      - "mix.exs" → Elixir
      - "composer.json" → PHP
      - "Gemfile" → Ruby
      - "pubspec.yaml" → Dart/Flutter
      - "CMakeLists.txt" → C/C++ (CMake)
      - "*.csproj" → C# (.NET)
      - "Package.swift" → Swift
      - "DESCRIPTION" → R
      - "cpanfile" → Perl
      - "*.rockspec" → Lua
      - "fpm.toml" → Fortran
      - "alire.toml" → Ada
      - "*.cob" → COBOL
      - "*.lpi" → Pascal
      - "*.vbproj" → VB.NET
```

**Output Phase 1:**

```
═══════════════════════════════════════════════════════════════
  /warmup - Peek Analysis
═══════════════════════════════════════════════════════════════

  Project: /workspace
  Type   : <detected_type>

  CLAUDE.md Hierarchy (<n> files):
    depth 0 : /CLAUDE.md (project root)
    depth 1 : /.devcontainer/CLAUDE.md, /src/CLAUDE.md
    depth 2 : /.devcontainer/features/CLAUDE.md
    ...

  Strategy: Funnel (root → leaves, decreasing detail)

═══════════════════════════════════════════════════════════════
```

---

## Phase 1.5: Feature Context (Conditional)

```yaml
phase_1.5_features:
  condition: ".claude/features.json exists"
  action: |
    Read .claude/features.json
    IF version == 1: note "Schema v1 detected — run /feature to auto-migrate to v2"
    IF version == 2: run infer_hierarchy (see /feature Hierarchy Inference)
  output: |
    Inject active features as hierarchy tree:
      F001  [L0] DDD Architecture       | completed
      ├─ F002  [L1] HTTP Server         | in_progress
      └─ F003  [L1] Database layer      | completed
    Orphans (level > 0 with no parent) shown with ⚠ warning.
```

---

## Phase 3.0: Parallelize (Analysis by Domain)

```yaml
parallel_analysis:
  mode: "PARALLEL (single message, 4 Task calls)"

  agents:
    - task: "source-analyzer"
      type: "docs-analyzer-structure"     # PR4 — routed via route-agent.sh, replaces generic Explore
      scope: "src/"
      prompt: |
        Analyze the source code structure:
        - Main packages/modules
        - Detected architectural patterns
        - Attention points (TODO, FIXME, HACK)
        Return: {packages[], patterns[], attention_points[]}

    - task: "config-analyzer"
      type: "docs-analyzer-structure"     # PR4 — routed via route-agent.sh, replaces generic Explore
      scope: ".devcontainer/"
      prompt: |
        Analyze the DevContainer configuration:
        - Installed features
        - Configured services
        - Available MCP servers
        Return: {features[], services[], mcp_servers[]}

    - task: "test-analyzer"
      type: "docs-analyzer-structure"     # PR4 — routed via route-agent.sh, replaces generic Explore
      scope: "tests/ OR **/*_test.go OR **/*.test.ts"
      prompt: |
        Analyze the test coverage:
        - Test files found
        - Test patterns used
        Return: {test_files[], patterns[], coverage_estimate}

    - task: "docs-analyzer"
      type: "docs-analyzer-structure"     # PR4 — routed via route-agent.sh, replaces generic Explore
      scope: "~/.claude/docs/"
      prompt: |
        Analyze the knowledge base:
        - Available pattern categories
        - Number of patterns per category
        Return: {categories[], pattern_count}
```

**IMPORTANT**: Launch all 4 agents in ONE SINGLE message.
