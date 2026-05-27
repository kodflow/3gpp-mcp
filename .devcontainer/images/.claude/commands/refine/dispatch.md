# refine/dispatch.md — Skills Architecture v1.5 (PR3, fix #17)

> **v1.6 note:** this phase is FULL-mode only. BARE and FROM-CONTRACT skip lens dispatch entirely — they jump straight to the synthesis pipeline (which itself adapts to the mode). The single-source-of-truth char-cap logic in `synthesis.md` is what BARE and FROM-CONTRACT reuse without reimplementing it. The char-cap is always 4000; lens depth (4 critical vs all 10) is independent.

## Lenses (10)

| Phase | Critical? | Default agent | Effort |
|---|---|---|---|
| `lens-1-correctness`  | yes | developer-executor-correctness   | xhigh  |
| `lens-2-security`     |     | developer-executor-security      | high   |
| `lens-3-edge-cases`   |     | developer-executor-correctness   | high   |
| `lens-4-rollback`     |     | devops-executor-linux            | medium |
| `lens-5-testability`  | yes | developer-executor-quality       | high   |
| `lens-6-dependency`   |     | devops-specialist-security       | medium |
| `lens-7-performance`  |     | developer-executor-design        | high   |
| `lens-8-observability`|     | developer-executor-shell         | medium |
| `lens-9-scope`        | yes | developer-orchestrator           | medium |
| `lens-10-goal-detect` | yes | developer-specialist-review      | high   |

Light depth runs only critical lenses (1, 5, 9, 10). Full depth runs all 10.
Char-cap stays 4000 in both cases.

## Dispatch sequence

```bash
# 1. Try router
ROUTER=~/.claude/scripts/route-agent.sh
DISPATCH=$(bash "$ROUTER" --skill /refine --phase "$LENS" --profile "$PROFILE")
RC=$?

# 2. Router exit 0 or 10 → use returned dispatch JSON
# 3. Router exit 20-31 → static fallback (fix #17)
if [ "$RC" -ge 20 ] && [ "$RC" -le 31 ]; then
  source ~/.claude/scripts/refine-static-fallback.sh
  DISPATCH=$(refine_static_lens "$LENS")
  [ -z "$DISPATCH" ] && {
    # Drop lens; annotate telemetry
    echo "{\"lens\":\"$LENS\",\"dropped\":true}" >> ~/.claude/logs/refine-dropped-lenses.jsonl
    continue
  }
fi

# 4. Hand off to actual agent invocation (Task or Agent primitive)
```

## Router-independence invariant

Critical lenses (1, 5, 9, 10) MUST reach the agent invocation step even
when `route-agent.sh` returns exit 20-31. This is enforced by the static
fallback above and verified by `TestRefineFallsBackToStaticWhenRouterErrors`.

## Refine pipeline (post-lens, 10 agents)

After lens findings are collected (FULL mode only — BARE and
FROM-CONTRACT skip lenses entirely), the synthesis step runs a
**second**, mono-concern pipeline: ten `refine-*` agents that each
compress one dimension of the directive. The pipeline is **ordered
and causal** — every agent's output feeds the next. No agent runs
before its inputs exist.

| # | Agent | Role |
|---|---|---|
| 1 | `refine-content-pruner` | Cheapest cut first: strip filler prose, redundant restatements, and meta-commentary before structural agents waste tokens on noise |
| 2 | `refine-scope-fencer` | Verify the pruner did not amputate in-scope work; flag scope creep introduced by upstream lenses |
| 3 | `refine-constraint-distiller` | Lock constraints in canonical form before any voice rewrite touches their wording |
| 4 | `refine-done-criteria-sharpener` | Sharpen acceptance criteria into binary, measurable assertions; output feeds the verifier binder |
| 5 | `refine-verifier-binder` | Bind one verifier (grep / bats / make) to each criterion; output feeds the escalation isolator |
| 6 | `refine-escalation-isolator` | Lift manual-only verifiers and ADR triggers into a dedicated escalation block; output feeds the sequence-causal-validator (step 7) which validates producer-before-consumer ordering before downstream steps 8–9 |
| 7 | `refine-sequence-causal-validator` | Diagnostic pass: validate the step order is causal (producer before consumer); runs late so it sees the final step list |
| 8 | `refine-imperative-rewriter` | Prose rewrite into imperative voice — runs only once semantics are stable |
| 9 | `refine-chain-stripper` | Strip any auto-chain language pasted in by upstream synthesis (`Skill(skill=…)`, "next, run /do", etc.) |
| 10 | `refine-density-optimizer` | Final density pass: token-cost compression that preserves structure — **MUST run last**, any earlier compression destroys the structure later agents need |

Pipeline invariants (locked by `refine-pipeline-rewire.bats`):

- Order is fixed (1 → 10). Steps 7 and 8 are parallelizable in principle
  but kept sequential for a linear synthesis log.
- Each agent is single-concern; outputs are additive, never merged.
- The density optimizer is **always** the terminal step; running it
  earlier breaks the structural assumptions of agents 7-9.
- BARE and FROM-CONTRACT skip steps 1-7 (no lens findings to compress);
  steps 8-10 still run on the rendered directive.

### Static fallback (refine-* pipeline)

If `route-agent.sh` cannot resolve a `refine-*` agent, fall back to
the static map in `refine-static-fallback.sh`. The 10 agent names
above are enumerated in the same order so the fallback preserves
pipeline causality.
