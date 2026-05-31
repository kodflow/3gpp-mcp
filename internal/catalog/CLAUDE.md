<!-- updated: 2026-05-30T08:38:41Z -->
# internal/catalog — DynaReport Metadata Overlay

## Purpose

Seeds authoritative 3GPP spec metadata from the public DynaReport HTML reports
(axis #3), replacing filename-derived guesses:

- portal `Releases.aspx` → release calendar + `freeze_date` (the ordering fix);
- dynareport `status-report.htm` → per-spec title, TS/TR, real working group.

It is a metadata **overlay**: updates `specs`/`spec_versions` rows that content
ingest already created; it never invents catalogue-only specs (cite-or-silent).

## Structure

```text
catalog/
├── fetch.go     # HTTP GET of the DynaReport / portal pages
├── parse.go     # golang.org/x/net/html extraction → ReleaseMeta / SpecMeta
└── catalog.go   # apply overlay onto an existing DuckDB snapshot
```

## Conventions

- Additive & idempotent — run via `cmd/ingest-catalog` *after* `cmd/ingest`.
- Parsed with `golang.org/x/net/html` (already a project dep — no new dependency).
- `freeze_date` is what fixes non-monotonic version ordering (CLAUDE.md §8 #3).
