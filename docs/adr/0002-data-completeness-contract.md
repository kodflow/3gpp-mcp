# ADR 0002 — Data-completeness contract gates the pullable image

Status: accepted (2026-06-15)

## Context

`3gpp-mcp` ships as two GHCR images (ADR split-data-image): the heavy
`3gpp-data` (DuckDB layer: clauses + FTS + dense vectors + frozen HNSW + reranker
+ — later — sparse / ETSI) and the code-only `3gpp-mcp` that inherits a
`3gpp-data` digest `FROM` at build time.

The producers of the data layer are **asynchronous, multi-hour Kaggle GPU
campaigns** (dense embed, sparse) dispatched by `corpus-matrix.yml` — they cannot
be inlined into a single CI run. Before this ADR, a **code push to `main`**
republished the pullable `3gpp-mcp:latest` on top of whatever `3gpp-data:latest`
happened to be, and the only build-time guard (`mcp-3gpp check-data`) asserted
**FTS + frozen HNSW** only. So an image could ship whose data layer lacked **dense
convergence**, **sparse**, or (later) **ETSI** — a half-loaded product, silently.

Serialising everything into one pipeline is infeasible. The correct model:
**make the pullable tag mean "complete", and gate every publish on a verified
completeness contract** + a **tag discipline** that keeps code pushes off the
pullable tag.

## Decision

### 1. One completeness contract, one source of truth
`scripts/data-contract.sh` maps the repo var `DATA_CONTRACT`
(`dense` | `dense+sparse` | `dense+sparse+etsi`, default `dense`) + `DATA_EMBED_FLOOR`
to a flag string consumed by BOTH gates:
- `cmd/validate` — the bake gate (corpus-data-image).
- `mcp-3gpp check-data` — the Dockerfile full-stage inheritance guard.

Contract levels:
- `dense` → `--require-fts --require-hnsw --require-embed-complete [--embed-floor X]`
- `dense+sparse` → `… --require-sparse` (identity-aware: a stale sparse model fails)
- `dense+sparse+etsi` → `… --require-etsi` (needs the flag, added with ETSI Phase C)

`--require-embed-complete` is **floor-aware** (`store.CountNullAtFloor`): no clause
at/above the floor may lack a vector. It is NOT global `--pending-zero` — below-floor
/ legacy (GSM) clauses are intentionally NULL and never counted.

### 2. The bake never promotes `:latest` onto an incomplete layer
`corpus-data-image.yml` runs `cmd/validate $(scripts/data-contract.sh)` on the
final (fused + compacted + frozen) DB **before any push**. Failure fails the bake →
`3gpp-data:latest` is unchanged. So `3gpp-data:latest` ≡ "passes the contract".

### 3. Tag discipline for the mcp image (`corpus-image.yml`)
- **Code push to `main`** → publishes ONLY `3gpp-mcp:edge` (+ `full-<sha>`); the
  guard is **lenient** (`--require-fts --require-hnsw`) so a code change is never
  blocked by data-campaign convergence, and it NEVER moves `:latest`.
- **Latest-moving paths** (`workflow_run` after a successful `Corpus Data Image`
  bake, or manual dispatch) → publish `3gpp-mcp:latest` / `:full`, built with the
  **full contract** (`DATA_CONTRACT_FLAGS` from the repo var). The Dockerfile
  full-stage guard re-asserts the contract, so `:latest` can only sit on a complete
  layer.

### 4. Ratchet (avoid bricking the pipeline today)
`DATA_CONTRACT=dense` until the first full **sparse** bake exists; then flip to
`dense+sparse`. `+etsi` only after `cmd/validate`/`check-data` gain `--require-etsi`
(ETSI ingestion, Phase C). `DATA_EMBED_FLOOR` defaults to `Rel-99` in the bake
(excludes legacy/GSM); tune if legitimate at-floor NULLs exist.

## The sequence the gates enforce (even though async)

```
corpus build (lexical)        → 3gpp-corpus:latest
  → dense embed converges     → 3gpp-vec:latest-fp16        (null-at-floor → 0)
  → sparse converges          → 3gpp-sparse:latest          (sparse-index.json)
  → bake fuses + freezes
     + cmd/validate CONTRACT   → 3gpp-data:latest            (only if complete)
  → mcp image (workflow_run)
     + check-data CONTRACT     → 3gpp-mcp:latest             (only on a complete layer)
  → labs redeploy
Code change → 3gpp-mcp:edge only (lenient guard); :latest untouched.
```

## Consequences

- The pullable `:latest` is, by construction, complete to the active contract level.
- Code ships fast on `:edge` without waiting days for a GPU campaign, and without
  ever degrading the pullable image.
- Tightening the contract (add sparse / ETSI) is a one-variable change
  (`DATA_CONTRACT`) — no workflow edits, no drift between the two gates.

## Not done here (deliberate)

- The **guard-convergence optimization** (skip a doomed bake before it starts) is
  deferred: the bake-time `cmd/validate` is the authoritative correctness gate; the
  guard pre-check would only save wasted compute.
- Making the data actually converge (running the sparse campaign, ingesting ETSI
  B/C) is the data work these gates **certify** — separate from this CI change.
