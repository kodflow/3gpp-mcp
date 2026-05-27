# Phase 1.0: Local Documentation (LOCAL-FIRST)

**ALWAYS execute first. Local documentation is VALIDATED and takes priority.**

```yaml
local_first:
  source: "~/.claude/docs/"
  index: "~/.claude/docs/README.md"

  workflow:
    1_search_local:
      action: |
        Grep("~/.claude/docs/", pattern=<keywords>)
        Glob("~/.claude/docs/**/*.md", pattern=<topic>)
      output: [matching_files]

    2_read_matches:
      action: |
        FOR each matching_file:
          Read(matching_file)
          Extract: definition, examples, related patterns
      output: local_knowledge

    3_evaluate_coverage:
      rule: |
        IF local_knowledge covers >= 80% of the query:
          status = "LOCAL_COMPLETE"
          → Skip Phase 1-3, go to Phase 6
        ELSE IF local_knowledge covers >= 40%:
          status = "LOCAL_PARTIAL"
          → Continue Phase 0+ for gaps only
        ELSE:
          status = "LOCAL_NONE"
          → Continue normal workflow

  categories_mapping:
    design_patterns: "creational/, structural/, behavioral/"
    performance: "performance/"
    concurrency: "concurrency/"
    enterprise: "enterprise/"
    messaging: "messaging/"
    ddd: "ddd/"
    functional: "functional/"
    architecture: "architectural/"
    cloud: "cloud/, resilience/"
    security: "security/"
    testing: "testing/"
    devops: "devops/"
    integration: "integration/"
    principles: "principles/"
```

**Output Phase 1.0:**

```
═══════════════════════════════════════════════
  /search - Local Documentation Check
═══════════════════════════════════════════════

  Query    : <query>
  Keywords : <k1>, <k2>, <k3>

  Local Search (~/.claude/docs/):
    ├─ Matches: 3 files
    │   ├─ behavioral/observer.md (95% match)
    │   ├─ behavioral/README.md (70% match)
    │   └─ principles/solid.md (40% match)
    │
    └─ Coverage: 85% → LOCAL_COMPLETE

  Status: ✓ Using local documentation (validated)
  External search: SKIPPED (local sufficient)

═══════════════════════════════════════════════
```

**If LOCAL_PARTIAL:**

```
═══════════════════════════════════════════════
  /search - Local Documentation Check
═══════════════════════════════════════════════

  Query    : "OAuth2 JWT authentication"
  Keywords : OAuth2, JWT, authentication

  Local Search (~/.claude/docs/):
    ├─ Matches: 1 file
    │   └─ security/README.md (50% match)
    │
    └─ Coverage: 50% → LOCAL_PARTIAL

  Status: ⚠ Partial local coverage
  Gaps identified:
    ├─ OAuth2 flow details (not in local)
    └─ JWT implementation specifics (not in local)

  Action: External search for gaps only

═══════════════════════════════════════════════
```

---

## Phase 2.0: Decomposition (RLM Pattern: Peek + Grep)

**Analyze the query BEFORE any search:**

1. **Peek** - Identify complexity
   - Simple query (1 concept) → Direct Phase 1
   - Complex query (2+ concepts) → Decompose

2. **Grep** - Extract keywords
   ```
   Query: "OAuth2 with JWT for REST API"
   Keywords: [OAuth2, JWT, API, REST]
   Technologies: [OAuth2 → rfc-editor.org, JWT → tools.ietf.org]
   ```

3. **Systematic parallelization**
   - Always launch up to 6 Task agents in parallel
   - Cover all relevant domains

**Output Phase 2.0:**
```
═══════════════════════════════════════════════
  /search - RLM Decomposition
═══════════════════════════════════════════════

  Query    : <query>
  Keywords : <k1>, <k2>, <k3>

  Decomposition:
    ├─ Sub-query 1: <concept1> → <domain1>
    ├─ Sub-query 2: <concept2> → <domain2>
    └─ Sub-query 3: <concept3> → <domain3>

  Strategy: PARALLEL (6 Task agents max)

═══════════════════════════════════════════════
```
