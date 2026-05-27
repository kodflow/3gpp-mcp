# File Generation

## Phase 4.0: File Generation

**Generate all files DIRECTLY from accumulated context. No templates.**

```yaml
generation_rules:
  - NO mustache/handlebars placeholders
  - NO template files referenced
  - Content is SYNTHESIZED from the full conversation context
  - Every file must contain real, specific, actionable content
  - Write vision.md FIRST, then remaining files in parallel
```

### Files to Generate

```yaml
files:
  # PRIMARY OUTPUT - written first
  - path: "/workspace/docs/vision.md"
    description: "Rich project vision synthesized from conversation"
    structure:
      - "# Vision: {name}"
      - "## Purpose — what and why"
      - "## Problem Statement — pain points addressed"
      - "## Target Users — who benefits and how"
      - "## Goals — prioritized list"
      - "## Success Criteria — measurable targets table"
      - "## Design Principles — guiding decisions"
      - "## Non-Goals — explicit exclusions"
      - "## Key Decisions — tech choices with rationale"

  # SUPPORTING FILES - written in parallel after vision.md
  - path: "/workspace/CLAUDE.md"
    description: "Project overview, tech stack, how to work"
    structure:
      - "# {name}"
      - "## Purpose — 2-3 sentences"
      - "## Tech Stack — languages, frameworks, databases"
      - "## How to Work — /init, /feature, /fix"
      - "## Key Principles — MCP-first, semantic search, specialists"
      - "## Verification — test, lint, security commands"
      - "## Documentation — links to vision, architecture, workflows"

  - path: "/workspace/AGENTS.md"
    description: "Map tech stack to available specialist agents"
    structure:
      - "# Specialist Agents"
      - "## Primary — agents matching tech stack"
      - "## Supporting — review, devops, security agents"
      - "## Usage — when to invoke each agent"

  - path: "/workspace/docs/architecture.md"
    description: "System context, components, data flow"
    structure:
      - "# Architecture: {name}"
      - "## System Context — high-level view"
      - "## Components — key modules/services"
      - "## Data Flow — how data moves"
      - "## Technology Stack — detailed breakdown"
      - "## Constraints — technical boundaries"

  - path: "/workspace/docs/workflows.md"
    description: "Development processes adapted to tech stack"
    structure:
      - "# Development Workflows"
      - "## Setup — prerequisites, installation"
      - "## Development Loop — code, test, commit"
      - "## Testing Strategy — unit, integration, e2e"
      - "## Deployment — build, release process"
      - "## CI/CD — pipeline stages"

  - path: "/workspace/README.md"
    description: "Update description section only, preserve existing structure"
    mode: "edit"
    note: "Only update the project description. Keep all other content."

  # CONDITIONAL FILES
  - path: "/workspace/.env.example"
    condition: "database OR cloud services mentioned"
    description: "Environment variable template"
    structure:
      - "# {name} Environment Variables"
      - "APP_NAME={name}"
      - "# Database, cloud, API vars as relevant"

  - path: "/workspace/Makefile"
    condition: "language with build tooling (Go, Rust, Python, Node)"
    description: "Build targets adapted to tech stack"
    structure:
      - "# {name} targets"
      - "Standard targets: build, test, lint, fmt, clean"
      - "Language-specific targets as relevant"
```

---

## Phase 4.5: CodeRabbit Configuration (AI Tools 1/3)

**Generate `.coderabbit.yaml` if missing, personalized from project context.**
**See also:** Phase 4.6 (Qodo Merge) and Phase 4.7 (Codacy) for the full AI tools configuration block.

```yaml
coderabbit_config:
  trigger: "ALWAYS (after file generation)"
  schema: "https://www.coderabbit.ai/integrations/schema.v2.json"

  1_check_exists:
    action: "Glob('/workspace/.coderabbit.yaml')"
    if_exists:
      status: "SKIP"
      message: "CodeRabbit config already exists."
    if_missing:
      status: "GENERATE"
      message: "Generating .coderabbit.yaml from project context..."

  2_detect_stack:
    action: "Map tech_stack from conversation to CodeRabbit tool names"
    mapping:
      # Language → tools to highlight in path_instructions
      "Go":         { linters: ["golangci-lint"], filePatterns: ["**/*.go"] }
      "Rust":       { linters: ["clippy"], filePatterns: ["**/*.rs"] }
      "Python":     { linters: ["ruff", "pylint"], filePatterns: ["**/*.py"] }
      "Node/TS":    { linters: ["eslint", "biome"], filePatterns: ["**/*.ts", "**/*.js"] }
      "Java":       { linters: ["pmd"], filePatterns: ["**/*.java"] }
      "Kotlin":     { linters: ["detekt"], filePatterns: ["**/*.kt"] }
      "Swift":      { linters: ["swiftlint"], filePatterns: ["**/*.swift"] }
      "PHP":        { linters: ["phpstan"], filePatterns: ["**/*.php"] }
      "Ruby":       { linters: ["rubocop"], filePatterns: ["**/*.rb"] }
      "C/C++":      { linters: ["cppcheck", "clang"], filePatterns: ["**/*.c", "**/*.cpp", "**/*.h"] }
      "C#":         { linters: [], filePatterns: ["**/*.cs"] }
      "Dart":       { linters: [], filePatterns: ["**/*.dart"] }
      "Elixir":     { linters: [], filePatterns: ["**/*.ex", "**/*.exs"] }
      "Lua":        { linters: ["luacheck"], filePatterns: ["**/*.lua"] }
      "Scala":      { linters: [], filePatterns: ["**/*.scala"] }
      "Fortran":    { linters: ["fortitudeLint"], filePatterns: ["**/*.f90"] }
      "Shell":      { linters: ["shellcheck"], filePatterns: ["**/*.sh"] }
      "Terraform":  { linters: ["tflint", "checkov"], filePatterns: ["**/*.tf"] }
      "Docker":     { linters: ["hadolint"], filePatterns: ["**/Dockerfile*"] }
      "Protobuf":   { linters: ["buf"], filePatterns: ["**/*.proto"] }
      "SQL":        { linters: ["sqlfluff"], filePatterns: ["**/*.sql"] }

  3_build_path_instructions:
    action: |
      For EACH detected language/framework, generate a path_instructions entry:
        - path: "{glob pattern from mapping}"
          instructions: "{language-specific review guidance based on project context}"

      ALSO add generic entries for:
        - path: "**/*.md" → "Check documentation accuracy"
        - path: "**/*.sh" → "Validate shell safety: strict mode, quoting, error handling, and command injection risks"
        - path: "**/*.yml" → "Validate CI/CD configuration"
        - path: "**/Dockerfile*" → "Check hadolint compliance, multi-stage builds"

  4_build_labels:
    action: |
      Generate labeling_instructions from project context:
        - ALWAYS include: "dependencies", "breaking-change", "security", "concurrency", "database", "performance", "shell", "correctness"
        - ADD project-specific labels based on architecture:
          - Microservices → "api", "service-{name}"
          - Monorepo → "package-{name}"
          - Frontend → "ui", "accessibility"
          - Backend → "api", "database"

  5_build_code_guidelines:
    action: |
      Populate knowledge_base.code_guidelines.filePatterns from detected stack:
        - Merge all filePatterns from step 2
        - Add: "**/*.yml", "**/*.yaml", "**/*.md", "**/*.json"

  6_generate_file:
    action: "Write /workspace/.coderabbit.yaml"
    template: |
      The file MUST strictly conform to the schema at:
      https://www.coderabbit.ai/integrations/schema.v2.json

      Structure (all sections required):
        language: "en-US"
        tone_instructions: "{derived from project quality priorities}"
        early_access: true
        enable_free_tier: true
        inheritance: false
        reviews:
          profile: "assertive"
          request_changes_workflow: true
          high_level_summary: true
          high_level_summary_instructions: "{from project context}"
          auto_title_instructions: "{conventional commits with project scopes}"
          labeling_instructions: [{from step 4}]
          auto_apply_labels: true
          path_filters: [standard exclusions]
          path_instructions: [{from step 3}]
          auto_review: { enabled: true, base_branches: ["main"] }
          finishing_touches: { docstrings: { enabled: true }, unit_tests: { enabled: true } }
          pre_merge_checks: { title: { mode: "warning" }, description: { mode: "warning" } }
          tools: {ALL tools enabled: true — CodeRabbit auto-detects relevance}
        chat: { art: false, auto_reply: true }
        knowledge_base: { code_guidelines: { filePatterns: [{from step 5}] } }
        code_generation: { docstrings/unit_tests path_instructions from detected stack }
        issue_enrichment: { planning: { enabled: true }, labeling: {from step 4} }

    schema_rules:
      - "pre_merge_checks uses: title, description, issue_assessment, docstrings, custom_checks"
      - "ast-grep has NO enabled property — use: essential_rules, rule_dirs, packages"
      - "issue_enrichment.labeling_instructions is INSIDE issue_enrichment.labeling (nested)"
      - "issue_enrichment.auto_apply_labels is INSIDE issue_enrichment.labeling (nested)"
      - "ALL other tools use: enabled (boolean)"

  7_validate:
    action: |
      python3 - <<'PY'
      import json, pathlib, urllib.request, yaml
      from jsonschema import validate

      cfg_path = pathlib.Path("/workspace/.coderabbit.yaml")
      cfg = yaml.safe_load(cfg_path.read_text())
      schema = json.load(urllib.request.urlopen("https://www.coderabbit.ai/integrations/schema.v2.json"))
      validate(instance=cfg, schema=schema)
      print("valid")
      PY
    on_failure: "Fix YAML syntax or schema violations and retry"
```

**Output Phase 4.5 (generated):**

```text
═══════════════════════════════════════════════════════════════
  CodeRabbit Configuration
═══════════════════════════════════════════════════════════════

  Status: GENERATED (new file)

  Detected Stack:
    ├─ Go       → golangci-lint
    ├─ Shell    → shellcheck
    └─ Docker   → hadolint

  Customizations:
    ├─ 5 path_instructions (language-specific)
    ├─ 8 labels (dependencies, breaking-change, security, concurrency, database, performance, shell, correctness)
    ├─ 3 filePatterns for code guidelines
    └─ Tone: "concise, technical, Go-idiomatic"

  Schema: valid (https://www.coderabbit.ai/integrations/schema.v2.json)

═══════════════════════════════════════════════════════════════
```

**Output Phase 4.5 (skipped):**

```text
═══════════════════════════════════════════════════════════════
  CodeRabbit Configuration
═══════════════════════════════════════════════════════════════

  Status: SKIPPED (file already exists)

═══════════════════════════════════════════════════════════════
```
