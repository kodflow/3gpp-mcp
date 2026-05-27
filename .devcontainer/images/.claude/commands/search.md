---
name: search
description: |
  Documentation Research with RLM (Recursive Language Model) patterns.
  LOCAL-FIRST: Searches internal docs (~/.claude/docs/) before external sources.
  Cross-validates sources, generates .claude/contexts/{slug}.md, handles conflicts.
  Use when: researching technologies, APIs, or best practices before implementation.
allowed-tools:
  - "WebSearch(*)"
  - "WebFetch(*)"
  - "Read(**/*)"
  - "Glob(**/*)"
  - "Grep(**/*)"
  - "Write(.claude/contexts/*.md)"
  - "Task(*)"
  - "AskUserQuestion(*)"
  - "mcp__context7__*"
  - "mcp__github__issue_write"
---

# Search - Documentation Research (RLM-Enhanced)

$ARGUMENTS

## Description

Research with **LOCAL-FIRST** strategy and RLM patterns.

### Priority: Validated local documentation

```
~/.claude/docs/ (LOCAL)  →  Official sources (EXTERNAL)
     ✓ Validated             ⚠ May be outdated
     ✓ Consistent            ⚠ May contradict local
     ✓ Immediate             ⚠ Requires validation
```

**Applied RLM patterns:**

- **Local-First** - Consult `~/.claude/docs/` first
- **Peek** - Quick preview before full analysis
- **Grep** - Filter by keywords before semantic fetch
- **Partition+Map** - Parallel multi-domain searches
- **Summarize** - Progressive summarization of sources
- **Conflict-Resolution** - Handle local/external contradictions
- **Programmatic** - Structured context generation

**Principle**: Local > External. Reliability > Quantity.

---

## Arguments

| Pattern | Action |
|---------|--------|
| `<query>` | New search on the topic |
| `--append` | Append to existing context (by slug) |
| `--status` | Display current context |
| `--list` | List all available contexts |
| `--clear` | Delete specific context (by slug) |
| `--clear --all` | Delete all context files |
| `--help` | Display help |

---

## --help

```
═══════════════════════════════════════════════
  /search - Documentation Research (RLM)
═══════════════════════════════════════════════

Usage: /search <query> [options]

Options:
  <query>           Search topic
  --append          Append to existing context (by slug)
  --status          Display current context
  --list            List all available contexts
  --clear           Delete specific context (by slug)
  --clear --all     Delete all context files
  --help            Display this help

Output: .claude/contexts/{slug}.md
  Slug generated from query keywords (lowercase, hyphens, max 40 chars)
  Example: "OAuth2 JWT authentication" → oauth2-jwt-auth

RLM Patterns (always applied):
  1. Peek    - Quick preview of results
  2. Grep    - Filter by keywords
  3. Map     - 6 parallel searches
  4. Synth   - Multi-source synthesis (3+ for HIGH)

Examples:
  /search OAuth2 with JWT
  /search Kubernetes ingress --append
  /search --status

Workflow:
  /search <query> → iterate → EnterPlanMode
═══════════════════════════════════════════════
```

---

## Official Sources (Whitelist)

**ABSOLUTE RULE**: ONLY the following domains.

### Languages
| Language | Domains |
|----------|---------|
| Node.js | nodejs.org, developer.mozilla.org |
| Python | docs.python.org, python.org |
| Go | go.dev, pkg.go.dev |
| Rust | rust-lang.org, doc.rust-lang.org |
| Java | docs.oracle.com, openjdk.org |
| C/C++ | cppreference.com, isocpp.org |
| C# / .NET | learn.microsoft.com, dotnet.microsoft.com |
| Ruby | ruby-lang.org, ruby-doc.org |
| PHP | php.net |
| Elixir | elixir-lang.org, hexdocs.pm |
| Kotlin | kotlinlang.org |
| Swift | swift.org, developer.apple.com |
| Scala | scala-lang.org, docs.scala-lang.org |
| Dart/Flutter | dart.dev, api.flutter.dev |
| Perl | perldoc.perl.org |
| Lua | lua.org |
| R | r-project.org, cran.r-project.org |
| Fortran | fortran-lang.org |
| Ada | ada-lang.io, learn.adacore.com |
| COBOL | gnucobol.sourceforge.io |
| Pascal | freepascal.org, lazarus-ide.org |

### Cloud & Infra

| Service | Domains |
|---------|---------|
| AWS | docs.aws.amazon.com |
| GCP | cloud.google.com |
| Azure | learn.microsoft.com |
| Docker | docs.docker.com |
| Kubernetes | kubernetes.io |
| Terraform | developer.hashicorp.com |
| GitLab | docs.gitlab.com |
| GitHub | docs.github.com |

### Frameworks
| Framework | Domains |
|-----------|---------|
| React | react.dev |
| Vue | vuejs.org |
| Next.js | nextjs.org |
| FastAPI | fastapi.tiangolo.com |

### Standards

| Type | Domains |
|------|---------|
| Web | developer.mozilla.org, w3.org |
| Security | owasp.org |
| RFCs | rfc-editor.org, tools.ietf.org |

### Blacklist

- Blogs, Medium, Dev.to
- Stack Overflow (except for problem identification)
- Third-party tutorials, online courses

---

## Phase Reference

| Phase | Module | Description |
|-------|--------|-------------|
| 1.0-2.0 | Read ~/.claude/commands/search/local.md | Local-first search + decomposition |
| 3.0-5.0, 8.0 | Read ~/.claude/commands/search/parallel.md | Parallel search + deep fetch + questions |
| 6.0-7.0 | Read ~/.claude/commands/search/validate.md | Cross-reference + conflict resolution |
| 9.0 | Read ~/.claude/commands/search/generate.md | Context file generation + management |

---

## Execution Flow

```
Phase 1.0: Local Documentation (LOCAL-FIRST)
  → LOCAL_COMPLETE? → Skip to Phase 9.0
  → LOCAL_PARTIAL?  → Search only for gaps
  → LOCAL_NONE?     → Full external search

Phase 2.0: Decomposition (Peek + Grep)
  → Extract keywords, identify domains

Phase 3.0: Parallel Search (Partition + Map)
  → Up to 6 Task agents in parallel

Phase 4.0: Peek at Results
  → Score relevance, filter < 5

Phase 5.0: Deep Fetch (Summarization)
  → Progressive summarization (3 levels)

Phase 6.0: Cross-referencing
  → Confidence scoring based on source count

Phase 7.0: Conflict Resolution
  → User resolution if local vs external conflict

Phase 8.0: Questions (if ambiguity)

Phase 9.0: Generate Context File
  → .claude/contexts/{slug}.md
```

---

## Guardrails

| Action | Status |
|--------|--------|
| Skip local documentation | **FORBIDDEN** |
| Ignore local/external conflict | **FORBIDDEN** |
| Prefer external over local without validation | **FORBIDDEN** |
| Non-official source | **FORBIDDEN** |
| Skip decomposition | **FORBIDDEN** |
| Sequential agents when parallelizable | **FORBIDDEN** |
| Info without source | **FORBIDDEN** |

**ABSOLUTE LOCAL-FIRST RULE:**

```yaml
local_first_rule:
  priority: "LOCAL > EXTERNAL"
  reason: "Local documentation is validated and consistent"

  workflow:
    1: "ALWAYS search in ~/.claude/docs/ first"
    2: "IF local sufficient → use local only"
    3: "IF conflict → ask the user"
    4: "IF update needed → create GitHub issue"
```

---

## Execution Examples

### Simple query

```
/search "Go context package"

→ 1 concept, 1 domain (go.dev)
→ Direct WebSearch + WebFetch
→ Validation 3+ sources
```

### Complex query

```
/search "OAuth2 JWT authentication for REST API"

→ 4 concepts, 3 domains
→ 6 parallel Task agents
→ Cross-reference fetch
→ RLM synthesis (3+ sources for HIGH)
```

### Multi-domain query

```
/search "Kubernetes ingress controller comparison"

→ 6 parallel Task agents
→ Coverage: kubernetes.io, docs.docker.com, cloud.google.com
→ Strict validation 3+ sources
```

---

## Canonical workflow footer (PR5a — Skills Architecture v1.3)

```
/search <query> → /plan → /refine → /goal
```

When implementation intent is detected (keywords in
`search/generate.md` Phase 9.5), `/search` chains into `/plan` via
`Skill(skill="plan", args="--context <slug>")`. From there
`/plan --goal` continues into `/refine` (PR5a). The legacy
`→ EnterPlanMode` footer is removed.
