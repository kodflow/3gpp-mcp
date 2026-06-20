<!-- updated: 2026-05-30T08:38:41Z -->
# internal/ — Library Packages

## Purpose

All **read-side** retrieval-engine logic. A **domain-agnostic core** (store,
search, mcp, model, embed-query) plus pluggable **domain verticals**
(`subject/*`) registered through `registry`. Adding a business domain is adding
a package, not editing the core (CLAUDE.md §1 philosophy).

**Write-side is Rust** (CLAUDE.md §2, arch-change 2026-06-19): parsing, ingest,
embed (bulk), indexing, merge, discover and the catalog/openapi/LI overlays are
Rust (`rust/`). The Go packages here serve queries read-only — `internal/store`
exposes `Reader` to the served path (Phase 11a). The former Go writers
(`ingest`, `htmlparse`, `ooxml`, `openapi`) were removed in Phase 11b.

## Structure

```text
internal/
├── model/        # domain types ↔ DuckDB tables (Spec, Clause, Change, Acronym…)
├── store/        # persistence: DuckDB (FTS + VSS HNSW), sharded; Reader = read-only serve surface
├── catalog/      # DynaReport metadata parse (read; consumed by enrichmeta)
├── embed/        # BGE-M3 query-embed seam: FFI → rust/embed-core cdylib (-tags embed_ffi) + Local (tests)
├── rerank/       # optional BGE-reranker-v2-m3 cross-encoder seam (axis #7)
├── onnxrt/       # shared ONNX Runtime init (process-global, sync.Once) — reranker
├── search/       # intent router + retrieval backends (BM25 / vector / sparse / RRF)
├── releaseview/  # release-scoped clause views (introduced/obsolete/later)
├── enrichmeta/   # read-side metadata enrichment (catalog overlay at query time)
├── eval/         # IR eval harness (graded queries + macro metrics)
├── metrics/ retry/ etsicat/ evolseed/  # serve metrics, net retry, ETSI crawl, evolutions data
├── bootstrap/    # self-provisioning: fetch DB snapshot + models into user cache
├── mcp/          # MCP tool surface (8 tools, CLAUDE.md §5) + resources + pagination
├── registry/     # wires the set of enabled subjects (the only core↔subject seam)
├── subjectmeta/  # CGO-free subject footprints (Version/Series) for incremental rebuild
└── subject/      # domain-vertical plugin contract (read-side tools; Tools/EnrichTerm take store.Reader)
    ├── glossary/ # seeds acronyms from TS 21.905 (no tool of its own)
    └── li/        # Lawful Interception (TS 33.128): ASN.1 registry + li_events tool
        └── asn1/  # dependency-free TS 33.128 ASN.1 scanner
```

## Architecture: the Subject plugin pattern

```
core (mcp, search)  →  registry  →  subject (contract)  ←  li, glossary
```

- `subject` defines the contract; concrete subjects import it.
- `registry` is the **single** place listing enabled verticals (`li`, `glossary`).
- The core imports `registry`, **never** a concrete subject — no import cycles,
  no hardcoded spec ids in the core.

## Import rules (no cycles)

- `subject` imports only `store` (as `store.Reader`), `model`, and mcp-go types.
- Concrete subjects import `subject` (+ `store`/`model`).
- `registry` imports the subjects; the core imports `registry`.
- `releaseview` lives outside any vertical so the core never depends on a subject.

## Conventions

- Every package leads with a `doc.go` (or package-header) comment tying it to a
  CLAUDE.md section; keep those authoritative when behaviour changes.
- **Cite-or-silent**: a type/path that cannot produce `{spec_id, release, version,
  clause, url}` is not returned by a tool (CLAUDE.md §1).
- ONNX-backed code is behind `-tags onnx` / `-tags embed_ffi`; default builds use
  noop/lexical fallbacks (degrade, never block). The reranker shares init via `onnxrt`;
  query-embed links the Rust `embed-core` cdylib over FFI.
- **No DuckDB writes on the served path** — `internal/{mcp,search}` consume
  `store.Reader` (compile-time read-only, Phase 11a). All corpus writes are Rust (`rust/`).
