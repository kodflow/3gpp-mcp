<!-- updated: 2026-05-30T08:38:41Z -->
# internal/openapi — 5GC OpenAPI Ingest

## Purpose

Loads the 5GC OpenAPI corpus (canonical YAML from 3GPP Forge, fetched by
`scripts/fetch-5g-apis.sh`) into an existing DuckDB snapshot (axis #2):
operations and schemas become searchable, citable artefacts.

## Structure

```text
openapi/
├── parse.go    # YAML → operations + schemas (per release/file)
└── ingest.go   # write api_* tables; Stats summary (Releases/Files/Ops/Schemas)
```

## Conventions

- **Additive** to the HTML ingest: run `cmd/ingest-openapi` *after* `cmd/ingest`.
  It clears **only** the `api_*` tables — never `clauses`.
- Keyed by release so the same operation across releases stays distinct.
- Cite-or-silent: rows retain enough to point back at the source spec/release.
