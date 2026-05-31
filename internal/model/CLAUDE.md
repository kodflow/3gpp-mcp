<!-- updated: 2026-05-30T08:38:41Z -->
# internal/model — Domain Types

## Purpose

Defines the domain types backing every MCP response. Each type maps 1:1 to a
DuckDB table declared in CLAUDE.md §4. A type that cannot produce a citation
`{spec_id, release, version, clause, url}` must not be returned by a tool (§1).

## Structure

```text
model/
├── types.go       # core structs ↔ tables
├── spec3gpp.go    # spec-id / series / doc-type parsing & helpers
├── pipeline.go    # pipeline-version + reproducibility metadata
└── doc.go         # type↔table mapping contract
```

## Type ↔ table mapping

| Type | Table |
|------|-------|
| `Spec` | `specs` |
| `Version` | `spec_versions` |
| `Clause` | `clauses` |
| `Change` | `changes` |
| `Acronym` | `acronyms` |
| `Evolution` | `evolutions` |

## Conventions

- Pure data + parsing helpers — no DB or network access (that is `store`/`ingest`).
- Version ordering is **not** semver alone: `(release, version, freeze_date)` is
  required (CLAUDE.md §8 piège #3). `release_ordinal_test` pins lineage order.
- `pipeline_version` changes force a full index rebuild (indexing invariant).
