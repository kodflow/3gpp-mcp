# Conversational Discovery

## Phase 1.0: Detect (Repository Identity - Template vs Personalized)

**Step 1: Identify the repository via git remote.**

```yaml
detect_repository:
  command: "git remote get-url origin 2>/dev/null"
  check: "does the URL contain 'kodflow/devcontainer-template'?"

  decision:
    if_is_devcontainer_template:
      action: "Continue to Step 2 (template marker check)"
      message: "devcontainer-template repo detected."
    if_is_other_project:
      action: "RESET — erase all generated docs, restart Phase 1 from scratch"
      message: "Different project detected. Resetting for fresh initialization."
      reset_files:
        - "/workspace/CLAUDE.md"
        - "/workspace/AGENTS.md"
      reset_directories:
        - "/workspace/docs/"    # rm -rf — template docs don't apply to new projects
      note: "README.md is NOT erased — only its description will be updated in Phase 3"
```

**Step 2 (only for devcontainer-template repo): Check template markers.**

```yaml
detect_template:
  check_markers:
    - file: "/workspace/CLAUDE.md"
      template_marker: "Kodflow DevContainer Template"
    - file: "/workspace/docs/vision.md"
      template_marker: "batteries-included VS Code Dev Container"

  decision:
    if_template_detected:
      action: "Run Phase 1 (Discovery Conversation)"
      message: "Template detected. Let's discover your project."
    if_personalized:
      action: "Skip to Phase 4 (Validation)"
      message: "Project already personalized. Validating..."
```

**Output Phase 0 (other project - reset):**

```
═══════════════════════════════════════════════════════════════
  /init - Project Detection
═══════════════════════════════════════════════════════════════

  Checking: git remote origin
  Result  : {remote_url} (NOT devcontainer-template)

  → Different project detected
  → Resetting docs for fresh initialization...
    ✗ CLAUDE.md        (reset)
    ✗ AGENTS.md        (reset)
    ✗ docs/            (removed)

  → Starting discovery conversation...

═══════════════════════════════════════════════════════════════
```

**Output Phase 0 (devcontainer-template - template markers):**

```
═══════════════════════════════════════════════════════════════
  /init - Project Detection
═══════════════════════════════════════════════════════════════

  Checking: git remote origin
  Result  : kodflow/devcontainer-template

  Checking: /workspace/CLAUDE.md
  Result  : Template markers found

  → Project needs personalization
  → Starting discovery conversation...

═══════════════════════════════════════════════════════════════
```

---

## Phase 2.0: Discovery Conversation

**RULES (ABSOLUTE):**

- Ask **ONE question at a time** as plain text output
- **NEVER** use AskUserQuestion tool
- **NEVER** offer predefined options or multiple-choice lists
- After **EACH** user response, display the updated **Project Context** block
- Adapt the next question based on accumulated context
- Minimum **4** exchanges, maximum **10**
- Questions must be open-ended and conversational

### Question Strategy

**Fixed questions (always asked first):**

```yaml
round_1:
  question: |
    Tell me about your project. What are you building
    and what problem does it solve?
  extracts: [purpose, problem]

round_2:
  question: |
    Who will use this? Describe the people or systems
    that will interact with it.
  extracts: [users]

round_3:
  question: |
    What should we call this project?
  extracts: [name]
```

**Adaptive questions (selected based on gaps in context):**

```yaml
adaptive_pool:
  tech_stack:
    trigger: "tech stack unknown"
    question: "What languages, frameworks, or tools are you planning to use?"
    extracts: [tech_stack]

  data_storage:
    trigger: "data storage relevant AND unknown"
    question: "How will your project store and manage data?"
    extracts: [database]

  deployment:
    trigger: "deployment unknown"
    question: "Where and how will this run in production?"
    extracts: [deployment]

  quality:
    trigger: "quality priorities unknown"
    question: "What matters most for quality — test coverage, performance, security, or something else?"
    extracts: [quality]

  constraints:
    trigger: "constraints unknown"
    question: "Are there any constraints I should know about — team size, timeline, compliance requirements?"
    extracts: [constraints]

  architecture:
    trigger: "complex project AND architecture unclear"
    question: "Do you have a particular architecture in mind — monolith, microservices, event-driven, or something else?"
    extracts: [architecture]

  follow_up:
    trigger: "previous answer was brief"
    question: "Can you tell me more about {topic}? I want to make sure I capture the full picture."
    extracts: [varies]
```

### Project Context Block

**Display this block after EVERY exchange, updated with new information:**

```
═════════════════════════════════════════════════════
  PROJECT CONTEXT
═════════════════════════════════════════════════════
  Name        : {name or "---"}
  Purpose     : {1-2 sentence summary or "---"}
  Problem     : {problem statement or "---"}
  Users       : {target users or "---"}
  Tech Stack  : {languages, frameworks or "---"}
  Database    : {database choices or "---"}
  Deployment  : {cloud/hosting or "---"}
  Architecture: {architecture approach or "---"}
  Quality     : {quality priorities or "---"}
  Constraints : {known constraints or "---"}
  [Discovery — exchange {N}/10]
═════════════════════════════════════════════════════
```

### Transition Criteria

Move to Phase 2 when **ALL** of these are true:

- Name is known
- Purpose/Problem is known
- Users are known
- At least one tech element is concrete
- At least 4 exchanges completed

**OR:** User signals readiness / 10 exchanges reached.

---

## Phase 3.0: Vision Synthesis

**Review the accumulated context with the user before generating files.**

```yaml
synthesis_workflow:
  step_1:
    action: "Display FINAL Project Context with all fields populated"
    output: |
      ═════════════════════════════════════════════════════
        FINAL PROJECT CONTEXT
      ═════════════════════════════════════════════════════
        Name        : {name}
        Purpose     : {purpose}
        Problem     : {problem}
        Users       : {users}
        Tech Stack  : {tech_stack}
        Database    : {database}
        Deployment  : {deployment}
        Architecture: {architecture}
        Quality     : {quality}
        Constraints : {constraints}
      ═════════════════════════════════════════════════════

  step_2:
    message: |
      Here is what I understand about your project.
      Review and tell me if anything needs to change.
      Say "generate" when you're ready for me to create
      your project documentation.

  step_3:
    loop: "Process any refinements, update context, repeat"
    exit: "User says 'generate' or confirms"

  step_4_warmup_chain:
    # PR1 — Skills Architecture v1.3: chain into /warmup once docs land so
    # the new session starts with the full project context cached.
    trigger: "After files are generated and committed"
    primitive: |
      Skill(skill="warmup")
    fallback_when_skill_absent: "/warmup"
```
