<!-- updated: 2026-05-30T08:38:41Z -->
# internal/ingest — Offline Ingestion Pipeline

## Purpose

Orchestrates the offline ingestion pipeline (CLAUDE.md §6): parse converted
specs → clause chunks → BGE-M3 embeddings → seed glossary → write DuckDB.
**Determinism is a goal**: a given `(corpus_state, ingest_version)` must produce
the same DB hash. Builds MUST be reproducible.

## Structure

```text
ingest/
├── ingest.go      # pipeline driver: parse → embed → store
├── evolutions.go  # curated NE↔NF evolutions seed (MME→AMF+SMF, eNB→gNB…)
└── doc.go         # pipeline contract (§6 steps)
```

## Pipeline (CLAUDE.md §6)

1. Scrape/convert sources (HTML via LibreOffice — see `htmlparse`).
2. Parse → clause-level chunks (never token-window; clause-aware, §13).
3. Embed via `internal/embed` (BGE-M3, batch 32, 1024-dim).
4. Seed glossary from TS 21.905 (`subject/glossary`).
5. (V2) CR pipeline.
6. Bulk-write DuckDB through `internal/store`, build FTS + HNSW at the end.

## Conventions

- The pipeline is the **only writer** to the store.
- Idempotent + diff-gated on normal runs; full rebuild when `pipeline_version`
  changes (indexing invariant decided in CI).
- `embed_floor_test` guards the minimum embedding coverage; keep it green.
- Subjects extract their artefacts during the same pass via `subject.IngestContext`.
