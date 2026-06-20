<!-- updated: 2026-05-30T08:38:41Z -->
# internal/store — Persistence

## Purpose

Wraps the persistence layer. DuckDB (FTS + VSS HNSW) is the backbone for
catalog, clauses, vectors, change records and acronyms. The **served path is
read-only**: `internal/{mcp,search}` consume `store.Reader` (reader.go), the
read-only surface — a compile-time guarantee that serve never writes (Phase 11a,
CLAUDE.md §2). Corpus writes are produced by the **Rust write-side** (`rust/`):
parse → ingest → embed → merge build the `.duckdb`; Go opens it read-only.

The concrete `*Store` still carries write methods (Insert/Upsert/Set/Build/…)
for offline Go utilities (`cmd/{split,export-delta,bench}`, scripts/smoke-seed),
which are NOT the served binary. KuzuDB (property graph) is a V2 addition.

## Structure

```text
store/
├── store.go      # connection, schema bootstrap, core read queries
├── reader.go     # store.Reader — the read-only serve surface (mcp/search consume this)
├── api.go        # public query surface consumed by search / mcp / subjects
├── catalog.go    # specs / spec_versions / list & filter queries
├── hnsw.go       # VSS HNSW vector search (+ OpenReadOnly, producer marker check)
├── sharded.go    # multi-shard read coordination (per-series/-release DBs)
└── doc.go        # package contract
```

## Conventions

- Served path is **read-only by construction** (`store.Reader`); never widen it
  with a write method. Add new read queries to `Reader` so serve can call them.
- HNSW is built/frozen by the Rust merge (`rust/store` build_and_freeze_hnsw);
  per-shard indexes are **not** concatenable — the Rust merge rebuilds FTS/HNSW
  on the merged DB.
- Sharding is by matrix axis (series or release); shards are disjoint on
  `(spec, release)`. Cross-shard reads fan out and merge in `sharded.go`.
- Cite-or-silent: queries return enough to build `{spec_id, release, version,
  clause, url}` citations (CLAUDE.md §1, §4).

## Attention Points

- `count_null` / `shards_coherent` tests guard against cache-hit count drift and
  incoherent shard sets — keep them green when touching shard logic.
