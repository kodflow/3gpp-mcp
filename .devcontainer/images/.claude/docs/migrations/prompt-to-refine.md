# Migration: /prompt → /refine

Skills Architecture v1.3 — PR5a deprecation, PR6 deletion.

## Why

`/prompt` was a paper template the user filled by hand to phrase a
`/plan` request precisely. `/refine` (W3) does the same job
automatically by reading the plan + context pair and emitting a
proof-bearing `/goal` contract. The user no longer has to phrase
anything — the agents do the proof-bearing work.

## Workflow change

```
Before:           /search → /prompt → /plan → /do
After  (v1.3):    /search → /plan → /refine → /goal → /do --goal-turn
```

`/prompt` shipped no acceptance criteria; `/refine` ships a `/goal`
contract with proof triplets (claim, evidence, remediation) per lens.

## Behaviour map

| /prompt action | /refine equivalent |
|---|---|
| Display template | n/a — /refine reads the plan directly |
| Anti-pattern hints | embedded in `lens-9-scope` outputs |
| Filled example | `.claude/goals/<slug>.md` is the rendered contract |

## Code references to update

A consumer fork that imported `/prompt` should:

1. Replace any `/prompt` invocation with `/refine <slug>` (where `<slug>`
   matches the existing `.claude/plans/<slug>.md`).
2. Drop the "fill the template" step entirely.
3. If a custom prompt template was needed, add a one-off section in the
   relevant plan's frontmatter; `/refine` reads everything it needs from
   there.

## Timeline

- PR5a (2026-05-20): banner added to `prompt.md`, `/plan --goal` ships.
- PR6 (next wave): `prompt.md` deleted.
- All `/prompt` references except `CHANGELOG.md` and this file blocked
  by `TestPromptNoReferences` (PR6).
