# refine/synthesis.md — Skills Architecture v1.6

## Pipeline per mode

| Mode | Pipeline steps |
|---|---|
| FULL | `collect → dedup → rank → render → refine-pipeline → square-prompt-validate → compact-to-minimum → square-prompt-validate` |
| BARE | `template → render → refine-pipeline → square-prompt-validate → compact-to-minimum → square-prompt-validate` |
| FROM-CONTRACT | `read → extract → render → refine-pipeline → square-prompt-validate → compact-to-minimum → square-prompt-validate` |

The **refine-pipeline** step (post-lens, 10 mono-concern agents) is
documented in `dispatch.md`. The **square-prompt-validate** step
runs **TWICE** — once before `compact-to-minimum` for early
diagnostics, and again after compaction as the final guarantee:
compaction MUST NOT invalidate the section list, the STOP literal
block, or the ACCEPTANCE/VERIFY 1:1 mapping. The **compact-to-minimum**
step is mode-agnostic — single source of truth for the directive
char-cap lives in this file.

### Why validate twice

Compaction is allowed to drop low-severity findings, abbreviate
sub-bullets, and strip filler words. Without a post-compaction
validation pass, the compactor could:

- delete a verifier line and break the ACCEPTANCE/VERIFY 1:1 mapping,
- collapse the STOP block into a single sentence (losing the literal
  contract harness consumers rely on),
- merge two sections (e.g. SCOPE + CONSTRAINTS) when trimming
  whitespace, breaking the canonical section order.

The post-compaction validator catches any of these regressions and
either re-emits the canonical block (deterministic recovery) or
fails the synthesis with exit code 25 so the user sees the issue
before `/goal <slug>` ever runs. Failing **before** the user types
`/goal` is the entire point of running the validator at all.

## Square-prompt validation (mandatory, all modes)

Every mode produces the **same** directive shape:
CONTEXT, OBJECTIVE, SCOPE (In/Out), CONSTRAINTS, ACCEPTANCE, VERIFY,
STOP. The validator runs after rendering and rejects any directive
that fails any of these invariants:

| Invariant | Rule |
|---|---|
| All 7 sections present | Each `# <SECTION>` header appears exactly once, in canonical order |
| ACCEPTANCE non-empty | At least one `- [ ]` checkbox under `# ACCEPTANCE` |
| VERIFY 1:1 with ACCEPTANCE | Each ACCEPTANCE checkbox maps to exactly one VERIFY entry |
| No vague verbs in ACCEPTANCE | None of the rejected phrasings (see table below) |
| STOP block is literal | Exactly the canonical STOP wording — no rephrasing |

### Vague-verb rejection table (synthesis applies this before render)

| Forbidden phrasing (in ACCEPTANCE) | Reject reason | Required rewrite |
|---|---|---|
| `fix <X>` | non-binary, no measure | `grep -c <symptom> <file> == 0` or equivalent |
| `make <X> work` | non-binary, no measure | `<cmd> → exit 0` |
| `improve <X>` | non-binary, comparative | `<bench> regression < N%` |
| `make it better` | subjective | binary or dropped |
| `looks correct` | subjective | binary or dropped |
| `should work` | speculative | binary or dropped |
| `handle <X> properly` | non-binary | `<probe-cmd> returns <value>` |

If a candidate ACCEPTANCE bullet trips the table and synthesis cannot
rewrite it from available evidence, the bullet is replaced with the
sentinel `- [ ] <user-must-fill-acceptance>` and a matching VERIFY
entry `<user-must-fill-acceptance> -> manual`. The gap is **visible**
on disk rather than hidden behind soft prose, so the user notices
and supplies a real binary check before running `/goal <slug>`.

This is the mechanism that prevents directives like
`/goal vasy execute le plan` or `/goal fix ca` from ever reaching
the goal state: they would land as a single sentinel bullet and the
user would see immediately that the prompt is unactionable.

### Char-cap — ceiling, not target (v1.5)

```
ceiling   = 4000 chars  (hard tool limit on /goal directive)
target    = minimum viable length given the content
floor_warn = 800 chars  (suspect over-compression below this)
```

`/refine` enforces a **4000-char ceiling** in every mode. The number
4000 is the tool's hard limit — not an aesthetic goal. The design
target is the **minimum viable length** that still preserves the
contract (acceptance criteria, scope, evidence, rollback).

There is no LIGHT-vs-FULL ceiling split. The natural output is
whatever the content requires; the skill decides that based on
content, never on input shape or plan complexity. Below the 800-char
**floor warning**, the synthesis log emits a `suspect-over-compression`
note so a human reviewer can confirm the contract is still intact.

| Mode | Typical directive length | Notes |
|---|---|---|
| FULL with all 10 lenses + heavy findings | usually near ceiling (3500-4000) | Risk of bumping ceiling — refine-density-optimizer trims |
| FULL with only 4 critical lenses | often 1500-2500 | Minimum viable is naturally smaller |
| BARE with terse description | often 600-1200 | Floor warning may fire — usually fine |
| FROM-CONTRACT after manual trimming | whatever the contract holds | Re-derived directly from disk |

The skill never pads to hit the ceiling; it stops short when content
runs out. The ceiling is the **upper bound**, never the aim.

### FULL pipeline

#### 1. Collect

Each lens returns JSON of shape:

```json
{
  "lens": "lens-N-<name>",
  "findings": [
    {"severity": "blocker|high|medium|low",
     "claim": "...",
     "evidence_paths": ["src/...", "tests/..."],
     "remediation": "..."}
  ]
}
```

#### 2. Dedup

Two findings are duplicates iff their `claim` (lowercased, punctuation
stripped) matches AND their `evidence_paths` intersect. Keep the
finding from the lens with the higher criticality flag.

#### 3. Rank

Severity then critical-lens-origin:

```
blocker (critical lens) > blocker > high (critical) > high > medium > low
```

### BARE pipeline

#### 1. Template

The free-form description is parsed into 5 slots. Each slot is optional;
missing slots are inferred from the description heuristically.

```
WHAT  : <action verb> <object> <details>
WHY   : <motivation>
WHERE : <files / modules / layers>
HOW   : <constraints, patterns, things to avoid>
DONE  : <measurable success criteria>
```

Heuristics:
- First sentence → WHAT
- Sentence starting with "because/parce que/since" → WHY
- File paths or directory mentions → WHERE
- "without/sans/avoid/never" clauses → HOW
- "tests pass / coverage ≥ N / 0 errors" patterns → DONE

If the description is already 5-line-structured, the parser preserves it.

### FROM-CONTRACT pipeline

#### 1. Read

Read `.claude/goals/<slug>.md`. The file MUST have the v1.5 frontmatter
schema (slug, mode, generated_at). Reject if absent.

#### 2. Extract

Pull the **Acceptance criteria** + **Proof triplets** + **Rollback** +
**Out of scope** sections, ignore the rest. The full contract stays
untouched on disk; we only re-derive the runtime directive.

### Shared steps (all modes)

#### 4. Render

| Mode | What's written |
|---|---|
| FULL | `.claude/goals/<slug>.md` (full contract) + runtime directive |
| BARE | `.claude/goals/<slug>.md` (5-slot template) + runtime directive |
| FROM-CONTRACT | runtime directive only — input file is never overwritten |

#### 5. Refine-pipeline (post-render, post-lens)

The 10 mono-concern `refine-*` agents documented in `dispatch.md` run
in canonical order against the rendered directive. Each agent
compresses one dimension; outputs are additive, never merged. BARE
and FROM-CONTRACT skip agents 1-7 (no lens findings to compress) but
still run agents 8-10 (`refine-imperative-rewriter`,
`refine-chain-stripper`, `refine-density-optimizer`) on the rendered
directive.

#### 6. Compact-to-minimum

Single function, all modes. The ceiling is the only hard guarantee —
the natural target is the minimum viable length given the content.

```
compact_to_minimum(text, ceiling=4000, floor_warn=800):
  # First, hard ceiling enforcement
  if len(text) > ceiling:
    # Cut order (preserve highest-value content):
    #   1. trim low-severity findings (FULL only)
    #   2. trim non-critical lens findings (FULL only)
    #   3. compress prose to checklist
    #   4. strip filler words ("the", "very", "really", etc.)
    #   5. abbreviate sub-bullets, keep file paths + identifiers verbatim
    # Never trim:
    #   - blockers (FULL)
    #   - critical-lens findings (FULL)
    #   - WHAT slot (BARE)
    #   - acceptance criteria checklist (all modes)
    text = cut_until(text, ceiling)
  # Then, floor advisory (does not modify text — diagnostic only)
  if len(text) < floor_warn:
    emit_warning("suspect-over-compression", chars=len(text), floor=floor_warn)
  return text  # guaranteed <= ceiling
```

## Output schema (v1.5)

```json
{
  "mode": "FULL|BARE|FROM_CONTRACT",
  "lens_depth": "light|full|n/a",   // FULL only
  "lenses_run": N,
  "lenses_dropped": [...],
  "findings_total": N,
  "findings_after_dedup": N,
  "directive_char_count": N,
  "directive_char_target": 4000,
  "directive": "<≤4000 chars>",
  "contract_path": ".claude/goals/<slug>.md",
  "contract_written": true|false
}
```

`directive_char_target` is preserved as a schema field for the test
contract, but its semantic meaning is **char ceiling enforced by the
/goal tool** — not a length the synthesis aims for. The natural
target is the minimum viable length given the content.

`lenses_run`/`lenses_dropped`/`findings_*` are 0 / [] / 0 in BARE +
FROM-CONTRACT. `contract_written` is false in FROM-CONTRACT.
