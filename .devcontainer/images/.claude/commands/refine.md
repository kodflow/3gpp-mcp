---
name: refine
description: |
  Skills Architecture v1.6 — proof-bearing goal contract generator with
  three entry modes (auto-detected from arguments). FULL mode reads
  .claude/contexts/<slug>.md + .claude/plans/<slug>.md and runs 4-10
  review lenses. BARE mode skips lens dispatch and structures a
  free-form description. FROM-CONTRACT mode re-compacts an existing
  goal contract. ALWAYS emits a predictable square-prompt directive
  with the same 7-section shape (CONTEXT, OBJECTIVE, SCOPE,
  CONSTRAINTS, ACCEPTANCE, VERIFY, STOP) so a vague input like
  "fix ca" never reaches the goal state — synthesis rejects vague
  verbs and forces binary measurable acceptance criteria paired 1:1
  with verifiers. The /goal directive has a 4000-char ceiling (hard
  tool limit); /refine aims for the minimum viable length that
  preserves the contract, never pads to fill the budget.
allowed-tools:
  - "Read(**/*)"
  - "Glob(**/*)"
  - "Grep(**/*)"
  - "Task(*)"
  - "Agent(*)"
  - "TaskCreate(*)"
  - "TaskUpdate(*)"
  - "Write(.claude/goals/*.md)"
  - "mcp__context7__*"
---

$ARGUMENTS

@.devcontainer/images/.claude/commands/shared/team-mode.md

# /refine — Goal Contract Generator (v1.6 — Skills Architecture)

## Directive char-cap — ceiling, not target

The `/goal` tool accepts a maximum **4000-character directive string**.
This is a hard runtime limit of the tool — a **ceiling**, not a goal.

`/refine` treats 4000 as the **ceiling** in every mode (FULL / BARE /
FROM-CONTRACT) and regardless of LIGHT/FULL lens selection. The design
**target is the minimum viable length** that still preserves the
contract: enough chars to bind acceptance criteria, evidence, and
scope; not a single char more. `/refine` decides the length from what
it has to say, never from the input shape, and never pads to hit 4000.

There is no separate LIGHT ceiling. LIGHT vs FULL only affects **how
many lenses run** (4 critical vs all 10), not the directive ceiling —
the 4000-char cap stays uniform across both lens depths.

## Mode auto-detection (v1.6)

`/refine <arg>` detects the mode from the argument shape + disk state:

```
arg contains spaces OR > 1 word
  → BARE (free-form description)

arg is a kebab-case-or-similar single token
  ├─ plans/<arg>.md + contexts/<arg>.md both exist  → FULL
  ├─ goals/<arg>.md exists, no plan/context         → FROM-CONTRACT
  ├─ none exist                                     → BARE (arg is a short description)
  └─ ambiguous (only plan OR only context)          → BARE with WARN
```

Explicit overrides for the edge cases:

| Flag | Forces |
|---|---|
| `--bare` | BARE mode even when files exist |
| `--full <slug>` | FULL mode even if only the contract exists (re-analyze) |
| `--from-contract <slug>` | FROM-CONTRACT even if plan/context still exist |
| `--slug <name>` | Override the auto-derived slug in BARE mode |
| `--lenses light` | Run only 4 critical lenses in FULL mode |
| `--lenses full` | Run all 10 lenses in FULL mode (overrides AUTO) |
| `--auto` | Default: AUTO picks light/full lens depth from plan frontmatter |

## Phase 0: Mode + slug resolution

```bash
resolve_mode_and_slug() {
  local arg="$1"
  # Explicit override flags win
  case "$EXPLICIT_MODE" in
    bare)          MODE=BARE;          return ;;
    full)          MODE=FULL;          return ;;
    from-contract) MODE=FROM_CONTRACT; return ;;
  esac

  # Free-form description: spaces or >1 word
  if [[ "$arg" == *" "* ]] || [[ $(echo "$arg" | wc -w) -gt 1 ]]; then
    MODE=BARE
    SLUG=${USER_SLUG:-$(derive_slug "$arg")}
    DESCRIPTION="$arg"
    return
  fi

  # Single-token slug: disk decides
  SLUG="$arg"
  local PLAN=".claude/plans/$SLUG.md"
  local CTX=".claude/contexts/$SLUG.md"
  local CONTRACT=".claude/goals/$SLUG.md"
  if [[ -r "$PLAN" && -r "$CTX" ]]; then
    MODE=FULL
  elif [[ -r "$CONTRACT" && ! -e "$PLAN" && ! -e "$CTX" ]]; then
    MODE=FROM_CONTRACT
  elif [[ ! -e "$PLAN" && ! -e "$CTX" && ! -e "$CONTRACT" ]]; then
    MODE=BARE; DESCRIPTION="$arg"
  else
    MODE=BARE; DESCRIPTION="$arg"
    echo "WARN: ambiguous state for '$SLUG' (only plan OR only context found) — defaulting to BARE" >&2
  fi
}
```

## Phase 1: AUTO lens depth — FULL mode only

Read `~/.claude/commands/refine/auto.md`. AUTO picks 4-critical-lenses
vs all-10 based on plan frontmatter:

| Field | Type | Notes |
|---|---|---|
| `risk` | string | `low` / `medium` / `high` / `critical` |
| `loc_estimate_max` | number | numeric upper bound on LOC change |
| `touches_public_api` | bool | true → all 10 |
| `touches_security_surface` | bool | true → all 10 |
| `touches_dev_infra` | bool | true → all 10 |

Any missing or non-numeric field defaults to **all 10**. The
char-cap is 4000 in both cases; lens depth is the only thing that varies.

BARE and FROM-CONTRACT skip this phase entirely (no lens analysis).

## Phase 2: Dispatch lenses — FULL mode only

Read `~/.claude/commands/refine/dispatch.md`. For each lens, call
`route-agent.sh --skill /refine --phase lens-N-<name>`. On exit code
20-31, fall back to the static lens map in `refine-static-fallback.sh`
(fix #17 — critical lenses must run regardless of router state).

## Phase 3: Synthesize (all modes)

Read `~/.claude/commands/refine/synthesis.md`.

- **FULL**: collect lens findings → dedup → rank → render → refine-pipeline → square-prompt-validate → compact-to-minimum → square-prompt-validate.
- **BARE**: apply WHAT/WHY/WHERE/HOW/DONE template → render → refine-pipeline → square-prompt-validate → compact-to-minimum → square-prompt-validate.
- **FROM-CONTRACT**: read contract → extract → render → refine-pipeline → square-prompt-validate → compact-to-minimum → square-prompt-validate.

Every mode goes through `square-prompt-validate` TWICE — once before
compaction (diagnostic) and once after (final guarantee). The directive
is ALWAYS the same 7-section square-prompt shape. Vague verbs like
"fix", "improve", or "make it work" are rejected **inside the
`# ACCEPTANCE` section only** (CONTEXT / OBJECTIVE / SCOPE / etc. may
freely use them in prose) and replaced with a visible
`- [ ] <user-must-fill-acceptance>` sentinel so the user notices the
gap before running `/goal`.

Output is bounded by the **4000-char ceiling**; the natural target is
the minimum viable length given the content. There is no padding.

## Phase 4: Emit (all modes)

Read `~/.claude/commands/refine/render.md`. Writes:

- **FULL**: `.claude/goals/<slug>.md` (proof triplets) + runtime directive.
- **BARE**: `.claude/goals/<slug>.md` skeleton (WHAT/WHY/WHERE/HOW/DONE) + runtime directive.
- **FROM-CONTRACT**: runtime directive only (input file is never overwritten).

The runtime directive is always appended to the goal-state file via
`goal-state.sh update` (PR1).

## Next step (manual, no auto-chain)

`/refine` ends by printing a textual suggestion of the form:

```
Suggested next step:
  /goal <slug>
```

There is **no auto-chain** into another skill — the user remains the
trigger. Run `/goal <slug>` to enter goal iteration once the contract
on disk has been reviewed.

## Arguments

| Pattern | Action |
|---------|--------|
| `<slug>` | Auto-detect mode from disk state (FULL / FROM-CONTRACT / BARE) |
| `"<multi-word description>"` | Force BARE — text contains spaces |
| `--bare` | Force BARE even when files exist |
| `--full <slug>` | Force FULL — re-analyze from plan even if contract exists |
| `--from-contract <slug>` | Force FROM-CONTRACT — re-compact contract |
| `--slug <name>` | Override auto-derived slug in BARE mode |
| `--lenses light` | FULL mode: only 4 critical lenses |
| `--lenses full` | FULL mode: all 10 lenses |
| `--auto` | Default for FULL: pick lens depth from plan frontmatter |
| `--help` | Display help |

## Workflow patterns (v1.6)

```
quick    : /refine "fix race in worker.go pool init"        →  Suggested next: /goal fix-race-in-worker-go
medium   : /plan ... --auto → /refine my-feature            →  Suggested next: /goal my-feature
full     : /search → /plan → /refine my-feature             →  Suggested next: /goal my-feature
edited   : (edit .claude/goals/my-feature.md) → /refine my-feature
                                                            →  Suggested next: /goal my-feature
```

Every chain ends with a printed suggestion; `/goal <slug>` is typed
by the user. No auto-chain, no magic. The 4000-char ceiling applies
in every case; the actual directive length is the minimum viable.

No explicit mode flag needed in normal usage — the skill detects from
the argument shape + disk state. Flags exist only for edge cases where
you want to override the default detection.
