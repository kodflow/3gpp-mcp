<!-- updated: 2026-05-30T08:38:41Z -->
# internal/store — Persistence

## Purpose

Wraps the persistence layer. DuckDB (FTS + VSS HNSW) is the V1 backbone for
catalog, clauses, vectors, change records and acronyms. V1 connectors expose
**read-only** query paths; writes go through `internal/ingest` (Parquet/CSV
batches + DuckDB bulk `COPY`). KuzuDB (property graph) is a V2 addition.

## Structure

```text
store/
├── store.go      # connection, schema bootstrap, core read queries
├── api.go        # public query surface consumed by search / mcp / subjects
├── catalog.go    # specs / spec_versions / list & filter queries
├── hnsw.go       # VSS HNSW index build/freeze + vector search
├── sharded.go    # multi-shard read coordination (per-series/-release DBs)
└── doc.go        # package contract
```

## Conventions

- Read-mostly: one writer (the ingest pipeline). Do not add write paths here.
- HNSW is built/frozen at end of ingest (`BuildAndFreezeHNSW`); per-shard indexes
  are **not** concatenable — `cmd/merge` rebuilds FTS on the merged DB.
- Sharding is by matrix axis (series or release); shards are disjoint on
  `(spec, release)`. Cross-shard reads fan out and merge in `sharded.go`.
- Cite-or-silent: queries return enough to build `{spec_id, release, version,
  clause, url}` citations (CLAUDE.md §1, §4).

## Attention Points

- `count_null` / `shards_coherent` tests guard against cache-hit count drift and
  incoherent shard sets — keep them green when touching shard logic.
