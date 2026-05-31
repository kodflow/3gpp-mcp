<!-- updated: 2026-05-30T08:38:41Z -->
# internal/ — Library Packages

## Purpose

All retrieval-engine logic. A **domain-agnostic core** (ingest, store, search,
mcp, model, embed) plus pluggable **domain verticals** (`subject/*`) registered
through `registry`. Adding a business domain is adding a package, not editing
the core (CLAUDE.md §1 philosophy).

## Structure

```text
internal/
├── model/        # domain types ↔ DuckDB tables (Spec, Clause, Change, Acronym…)
├── store/        # persistence: DuckDB (FTS + VSS HNSW), sharded, read-only query
├── ingest/       # offline pipeline orchestration (CLAUDE.md §6) + evolutions seed
├── htmlparse/    # LibreOffice-HTML → ParsedSpec (primary parser; ~55% legacy .doc)
├── ooxml/        # native .docx → ParsedSpec (drop-in swap; merged-table geometry)
├── catalog/      # DynaReport metadata overlay (release calendar, WG, freeze_date)
├── openapi/      # 5GC OpenAPI (YAML) ingest → api_* tables
├── embed/        # BGE-M3 ONNX embedder seam (dense 1024-dim; degrade-not-block)
├── rerank/       # optional BGE-reranker-v2-m3 cross-encoder seam (axis #7)
├── onnxrt/       # shared ONNX Runtime init (process-global, sync.Once)
├── search/       # intent router + retrieval backends (BM25 / vector / RRF)
├── releaseview/  # release-scoped clause views (introduced/obsolete/later)
├── eval/         # IR eval harness (graded queries + macro metrics)
├── bootstrap/    # self-provisioning: fetch DB snapshot + models into user cache
├── mcp/          # MCP tool surface (8 tools, CLAUDE.md §5) + resources + pagination
├── registry/     # wires the set of enabled subjects (the only core↔subject seam)
├── subjectmeta/  # CGO-free subject footprints (Version/Series) for incremental rebuild
└── subject/      # domain-vertical plugin contract
    ├── glossary/ # seeds acronyms from TS 21.905 (no tool of its own)
    └── li/        # Lawful Interception (TS 33.128): ASN.1 registry + li_events tool
        └── asn1/  # dependency-free TS 33.128 ASN.1 scanner
```

## Architecture: the Subject plugin pattern

```
core (ingest, mcp)  →  registry  →  subject (contract)  ←  li, glossary
```

- `subject` defines the contract; concrete subjects import it.
- `registry` is the **single** place listing enabled verticals (`li`, `glossary`).
- The core imports `registry`, **never** a concrete subject — no import cycles,
  no hardcoded spec ids in the core.

## Import rules (no cycles)

- `subject` imports only `store`, `model`, and mcp-go types.
- Concrete subjects import `subject` (+ `store`/`model`).
- `registry` imports the subjects; the core imports `registry`.
- `releaseview` lives outside any vertical so the core never depends on a subject.

## Conventions

- Every package leads with a `doc.go` (or package-header) comment tying it to a
  CLAUDE.md section; keep those authoritative when behaviour changes.
- **Cite-or-silent**: a type/path that cannot produce `{spec_id, release, version,
  clause, url}` is not returned by a tool (CLAUDE.md §1).
- ONNX-backed code is behind `-tags onnx`; default builds use noop/lexical
  fallbacks (degrade, never block). Share init via `onnxrt`.
- Writes go through `ingest`; `store` query paths are read-mostly.
