# Phase 4.0: Pattern Consultation

**Consult `~/.claude/docs/` for patterns when applicable:**

> **Escape clause:** For trivial tasks (single-file edits, config changes, version bumps),
> skip pattern consultation and proceed directly to Phase 5.0 (Synthesize).
> Apply this phase only when architecture or design decisions are involved.

```yaml
pattern_consultation:
  1_identify_category:
    mapping:
      - "Object creation?" → creational/README.md
      - "Performance/Cache?" → performance/README.md
      - "Concurrency?" → concurrency/README.md
      - "Architecture?" → architectural/*.md
      - "Integration?" → messaging/README.md
      - "Security?" → security/README.md

  2_read_patterns:
    action: "Read(~/.claude/docs/<category>/README.md)"
    output: "2-3 applicable patterns"

  3_integrate:
    action: "Add to plan with justification"
```

**Output:**

```
═══════════════════════════════════════════════════════════════
  Pattern Analysis
═══════════════════════════════════════════════════════════════

  Patterns identified:
    ✓ Repository (DDD) - For user data access
    ✓ Factory (Creational) - For token creation
    ✓ Middleware (Enterprise) - For auth chain

  References consulted:
    → ~/.claude/docs/ddd/README.md
    → ~/.claude/docs/creational/README.md
    → ~/.claude/docs/enterprise/README.md

═══════════════════════════════════════════════════════════════
```

---

## DTO Convention (Go)

**If the plan involves DTOs, remind the convention:**

```yaml
dto_reminder:
  trigger: "Plan includes DTO/Request/Response structs"

  convention:
    format: 'dto:"<direction>,<context>,<security>"'
    values:
      direction: [in, out, inout]
      context: [api, cmd, query, event, msg, priv]
      security: [pub, priv, pii, secret]

  purpose: |
    Exempts structs from KTN-STRUCT-ONEFILE
    (grouping multiple DTOs in the same file is allowed)

  include_in_plan: |
    ### DTO Convention
    All DTO structs MUST use `dto:"dir,ctx,sec"` tags:
    ```go
    type CreateUserRequest struct {
        Email string `dto:"in,api,pii" json:"email"`
    }
    ```
    Ref: `~/.claude/docs/conventions/dto-tags.md`
```
