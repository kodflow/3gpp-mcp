# refine/render.md — Skills Architecture v1.5

## FULL contract template (`.claude/goals/<slug>.md`)

```markdown
---
slug: <slug>
plan: .claude/plans/<slug>.md
context: .claude/contexts/<slug>.md
mode: FULL
lens_depth: light|full
generated_at: <ISO8601>
---

# Goal Contract: <plan title>

## Acceptance criteria
- [ ] <criterion 1>
- [ ] <criterion 2>

## Proof triplets per lens

### lens-1-correctness (critical)
- **Claim:** ...
- **Evidence:** `src/path:line`, `tests/path:line`
- **Remediation:** ...

[... one section per lens that produced findings ...]

## Rollback strategy
<from lens-4-rollback>

## Out of scope
<from lens-9-scope>
```

## BARE contract template (`.claude/goals/<slug>.md`)

```markdown
---
slug: <slug>
plan: null
context: null
mode: BARE
generated_at: <ISO8601>
---

# Goal Contract: <derived from WHAT slot>

## What
<WHAT slot>

## Why
<WHY slot, or "unspecified">

## Where
<WHERE slot — file/module list, or "unspecified">

## How (constraints)
<HOW slot — bullet list, or "no explicit constraints">

## Acceptance criteria
- [ ] <DONE slot decomposed into checkboxes>

## Out of scope
- proof triplets (BARE mode skips lens analysis)
- rollback strategy (no plan to roll back)
```

The BARE contract is intentionally lean — no proof triplets, no
rollback. The user is told this explicitly via the "Out of scope"
section so they don't expect lens-level guarantees.

## Runtime directive — the square-prompt template (single shape, all modes)

`/refine` ALWAYS emits a single, predictable structured directive.
There is no per-mode shape — FULL, BARE, and FROM-CONTRACT all
produce the same template. What varies is only **how the slots are
filled** (from lens findings, from a free-form description, or from
a re-read of an existing contract).

The directive is bounded by the **4000-char ceiling** of the /goal
tool (see `synthesis.md` for the ceiling/floor doctrine). The
template is dense by design — every section earns its bytes — and
the natural target is the minimum viable length given the content.

The shape is fixed so a harness reading the directive — and a human
reviewing it — always knows where each datum lives, and so a vague
condition like "fix it" never reaches the goal state because the
template REQUIRES paired binary acceptance + verifier entries.

### Canonical template

```
/goal "<slug>

# CONTEXT
<1-3 sentences: the system state and why this work matters now>

# OBJECTIVE
<one sentence stating the END STATE — not a process>

# SCOPE
In:  <files, modules, or areas the work touches>
Out: <explicit non-targets — sibling concerns, follow-up PRs>

# CONSTRAINTS
- <hard rule 1>
- <hard rule 2>

# ACCEPTANCE
- [ ] <binary, measurable criterion 1>
- [ ] <binary, measurable criterion 2>
- [ ] <…each criterion is a checkbox the harness can mark TRUE/FALSE>

# VERIFY
- <criterion 1> -> <bash command, grep, test name, or tool invocation>
- <criterion 2> -> <verifier — one per ACCEPTANCE line, 1:1 mapping>

# STOP
Halt only when every ACCEPTANCE box returns TRUE under its paired
VERIFY entry. Do not stop on partial progress, on 'looks fine', or
on a non-VERIFY heuristic. Vague conditions ('fix it', 'improve',
'make it work') are forbidden in ACCEPTANCE and rejected at synthesis."
```

### Slot population per mode

| Slot | FULL | BARE | FROM-CONTRACT |
|---|---|---|---|
| CONTEXT | top-of-plan summary | inferred from description | contract's `## Why` |
| OBJECTIVE | plan title rewritten as end-state | first sentence of description | contract's first non-trivial bullet |
| SCOPE In | plan's owned_paths + touched modules | WHERE slot | contract's `## Where` |
| SCOPE Out | plan's "Out of scope" section | "(nothing explicit)" | contract's `## Out of scope` |
| CONSTRAINTS | HOW from lens findings + plan constraints | HOW slot | contract's `## How` |
| ACCEPTANCE | dedup'd findings → binary form | DONE slot decomposed | contract's `## Acceptance criteria` |
| VERIFY | bound by `refine-verifier-binder` (one per criterion) | derived from acceptance | re-bound from contract |
| STOP | always the verbatim STOP block (no per-mode variation) | same | same |

The STOP block is **literal** — never rephrased per mode. That is
what makes the directive predictable: any consumer reading the
directive sees the same termination contract every time.

### Forbidden ACCEPTANCE phrasings (rejected at synthesis)

| Vague | Rewrite into |
|---|---|
| `fix the bug` | `grep -c 'race in worker.go:42' src/worker.go == 0` |
| `make tests pass` | `make test → exit 0 with 0 failures` |
| `improve performance` | `bench cmp baseline.txt new.txt → no regression > 5%` |
| `looks correct` | binary command-bound assertion or it is dropped |
| `should work` | binary command-bound assertion or it is dropped |

If a slot is genuinely unknown (e.g. BARE mode with a one-line
description and no DONE clause), synthesis emits a single
`# ACCEPTANCE` bullet `- [ ] <user-must-fill-acceptance>` and a
matching `# VERIFY` line `- <user-must-fill-acceptance> -> manual`
— making the gap visible rather than hiding it behind soft prose.

## Post-render side effects

### FULL & BARE

```bash
# 1. Write contract
Write(file_path=".claude/goals/<slug>.md", content=<rendered>)

# 2. Append directive to goal state (PR1)
bash ~/.claude/scripts/goal-state.sh update "<slug>" \
  --decision met --decision-reason "refined" \
  --append-objective "directive-emitted"

# 3. Emit directive + manual-trigger suggestion (no auto-chain).
#    The directive is the square-prompt template defined above; the
#    suggestion is the SAME shape every time, regardless of mode.
printf '%s\n\nSuggested next step:\n  /goal %s\n' "$DIRECTIVE" "$SLUG"
```

### FROM-CONTRACT

```bash
# 1. NO contract write — input file is the source of truth, never overwritten.

# 2. Append directive to goal state
bash ~/.claude/scripts/goal-state.sh update "<slug>" \
  --decision met --decision-reason "recompacted" \
  --append-objective "directive-re-emitted"

# 3. Emit directive + manual-trigger suggestion (no auto-chain)
printf '%s\n\nSuggested next step:\n  /goal %s\n' "$DIRECTIVE" "$SLUG"
```

The trailing `Suggested next step:` line is the **only** post-emit
hand-off. There is no `Skill(skill=…)` call, no auto-chain — the
user remains the trigger for `/goal <slug>`. The directive itself
is always the square-prompt template; the user reading it sees the
same CONTEXT / OBJECTIVE / SCOPE / CONSTRAINTS / ACCEPTANCE / VERIFY
/ STOP structure every time.

## Slug derivation (BARE mode)

When the user invokes `/refine "<description>"` (auto-detected as BARE)
without `--slug <name>`, derive the slug deterministically:

```bash
derive_slug() {
  local desc="$1"
  echo "$desc" \
    | tr '[:upper:]' '[:lower:]' \
    | tr -cs '[:alnum:]' '-' \
    | awk -F- '{ for (i=1;i<=NF && i<=5 && length(out)<40;i++) out=out (out?"-":"") $i; print out }' \
    | sed 's/^-//;s/-$//'
}
# "Fix race in worker.go pool init" → "fix-race-in-worker-go"
```

Collision handling: if `.claude/goals/<slug>.md` exists, append a short
ISO date suffix (`-2026-05-20`) and try again. Refuse to overwrite an
existing BARE contract without explicit `--force`.
