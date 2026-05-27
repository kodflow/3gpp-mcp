# Architecture: 3gpp-mcp

> Migrated from the original GitLab project's `CLAUDE.md` architectural verdict.
> The stack is **frozen**; deviations need a written justification and an
> `arch-change` label on the PR.

## Frozen stack

```
┌──────────────────────────────────────────────────────────────┐
│  mcp-3gpp  (single static Go binary, CGO, ~25 MB)            │
└──────────────────────────────────────────────────────────────┘

Language    : Go 1.25+, CGO allowed
Storage     : DuckDB (FTS BM25 + VSS HNSW + analytical SQL)
Graph (V2)  : KuzuDB embedded (NE↔NF evolutions)
Embeddings  : ONNX Runtime + BGE-M3 (1024-dim), behind `-tags onnx`
Reranker    : BGE-reranker-v2-m3 ONNX (optional, V2)
Doc parsing : HTML (LibreOffice-converted DOCX) + native DOCX (zip+xml)
MCP SDK     : github.com/mark3labs/mcp-go
Distribution: single binary; `bootstrap` pulls index + models
```

### Why these choices

- **Go + CGO**: solo-dev velocity with a single distributable binary; CGO buys
  real HNSW, BM25, and native ONNX. No Python runtime to manage.
- **DuckDB over SQLite**: at corpus scale (millions of chunks projected) SQLite
  + sqlite-vec degrades (brute-force vectors, slow analytics). DuckDB stays flat
  (~3 ms HNSW, ~80–200 ms analytics) with native Parquet/Arrow export.
- **KuzuDB over Neo4j** (V2): embedded, columnar, no JVM daemon, Cypher.
- **No Ollama in the query path**: Claude is already the reasoning engine; a
  second 7B–32B LLM adds latency and hallucinates 3GPP terminology. Local LLMs
  are allowed *only* for offline batch entity extraction, never at query time.

## Retrieval (router-based)

```
Claude query ──▶ Intent router (regex) ──▶ { BM25 FTS | HNSW vec | KuzuDB graph | SQL changelog }
                                              └──────────────┬──────────────┘
                                       Hybrid fusion (RRF k=60) + optional reranker
                                                             │
                              JSON [{spec_id, release, version, clause, heading, text, url, score}]
```

| Detected pattern | Backend |
|---|---|
| `TS \d\d\.\d\d\d` | BM25 filtered by spec |
| `(remplace\|évolution\|maps to)` | Graph (V2) |
| `(diff\|change) entre Rel-\d+ et Rel-\d+` | SQL on `changes` |
| definition + ALL-CAPS acronym | glossary lookup |
| otherwise | hybrid BM25 + vector + RRF |

## Data model (DuckDB)

`specs`, `spec_versions`, `clauses` (clause-level chunks with `embedding
FLOAT[1024]`, HNSW + FTS indexed), `changes` (CR-level, keyed by `cr_number`
because one CR can touch many specs), `acronyms` (glossary), `evolutions`
(NE↔NF). Plus additive overlays: `api_*` (5GC OpenAPI), `li_events` (TS 33.128).

## MCP surface — 8 core tools

`search_spec`, `get_spec`, `get_changelog`, `list_releases`, `resolve_term`,
`trace_evolution`, `find_cross_references`, `list_specs`. Every response carries
a `citations: [{spec_id, release, version, clause, url}]` block. Domain subjects
(glossary, LI, 5GC API) register extra tools via `internal/registry` with zero
core coupling.

## Ingestion pipeline

```
1. Sync    : scripts/corpus.sh — download DOCX from 3gpp.org FTP (incremental)
2. Convert : soffice --headless DOCX -> HTML
3. Parse   : internal/htmlparse (or internal/ooxml native DOCX) -> clauses
4. Embed   : internal/embed BGE-M3 (optional, -tags onnx)
5. Glossary: seed TS 21.905 + regex mining
6. Changelog: parse Change History annex
7. Write   : DuckDB bulk insert + build FTS/HNSW indexes
```

The index ("R") is `data/3gpp.duckdb`. How it is mirrored, built in CI, and
distributed is covered in [`docs/data-pipeline.md`](./data-pipeline.md).

## Reconciled deviation from the original verdict

The original `CLAUDE.md` locked **"native DOCX, no HTML"**. In practice ~55% of
the corpus is legacy binary `.doc`; `scripts/corpus.sh` converts everything to
HTML via LibreOffice and `internal/htmlparse` ingests that (100% corpus
coverage). A native DOCX parser (`internal/ooxml`) also exists and produces the
same `ParsedSpec`, so ingestion is swappable. **This document treats HTML
conversion as the supported default**, with native DOCX as the alternative —
superseding the original lock. Embeddings remain **disabled by default** (the
binary builds without the ~2 GB ONNX model; search degrades to lexical) and are
enabled with `-tags onnx`.

## Explicit locks (carried over)

❌ No Python · ❌ No Ollama/local LLM at query time · ❌ No bare KV store ·
❌ No Neo4j (JVM) · ❌ No Elasticsearch · ❌ No PDF parsing · ❌ No OCR ·
❌ No arbitrary token-window chunking (always clause-aware) · ❌ No server-side
summaries.

## External sources

| Source | URL | Usage |
|---|---|---|
| 3GPP archive | `3gpp.org/ftp/Specs/archive` | all spec versions |
| 3GPP DynaReport | `3gpp.org/dynareport` | release status, spec→WG, CR DB |
| 3GPP Forge | `forge.3gpp.org/rep/all/5G_APIs` | 5GC OpenAPI YAML (pinned SHA) |
| TS 21.905 | series 21 | canonical glossary seed |
| HuggingFace | BAAI/bge-m3 | embedding + reranker models |
