<!-- updated: 2026-05-30T08:38:41Z -->
# internal/eval — Retrieval Eval Harness

## Purpose

Offline IR evaluation: scores retrieval systems (lexical / hybrid / hybrid+rerank)
on a graded query set with known-relevant clause refs, producing macro-averaged
metrics. Drives `cmd/bench` and the axis-#7 "pick the V2 stack from data" goal.

## Structure

```text
eval/
├── eval.go      # Query / Relevant types; load graded query set (JSON)
└── metrics.go   # IR metrics (recall@k, nDCG, MRR…) macro-averaged
```

## Conventions

- Read-only; never ships in `mcp-3gpp`. Exists to make stack choices empirical.
- Query set is JSON: free-text `query` + `relevant` clause refs (+ intent/domain).
- `metrics_test` pins metric math — keep deterministic.
