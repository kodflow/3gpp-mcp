<!-- updated: 2026-05-30T08:38:41Z -->
# cmd/ — Binaries

## Purpose

Entry points for the project. One sub-directory per binary; each `main.go` is a
thin CLI that wires `internal/` packages together. Only `server` ships to users
(the `mcp-3gpp` mono-binary, CLAUDE.md §1); the rest are offline / CI tooling.

## Structure

```text
cmd/
├── server/          # MCP server over stdio + `bootstrap` provisioning subcommand
│   ├── main.go
│   └── bootstrap.go
├── ingest/          # build the deterministic DuckDB snapshot from converted HTML (lexical)
├── embed/           # decoupled vectorisation of an EXISTING DB; re-embeds only changed clauses
├── merge/           # fuse per-shard DuckDB snapshots into one consolidated DB
├── discover/        # decide which series need (re)indexing → sizes the CI matrix
├── ingest-catalog/  # overlay DynaReport metadata (WG, title, freeze_date) onto a DB
├── ingest-openapi/  # load the 5GC OpenAPI corpus into an existing DB (additive)
├── li-audit/        # cross-check an external LI event catalogue vs indexed text
└── bench/           # offline retrieval benchmark (IR metrics); never shipped
```

## Binaries

| Binary | Role | Ships? |
|--------|------|--------|
| `server` | MCP server (stdio); `serve` / `bootstrap` / `version` subcommands | ✅ user-facing |
| `ingest` | Scrape-converted HTML → clauses + FTS → DuckDB (lexical; embed is now separate) | offline |
| `embed` | Vectorise an existing DB in place; re-embeds ONLY clauses whose `embedding_hash` drifted (micro-granular). Runs after ingest, never on a lexical-only build | offline/CI |
| `merge` | Concatenate disjoint shard DBs; offsets synthetic PKs, rebuilds FTS | CI |
| `discover` | Diff site versions vs `corpus-index.json` + changed subjects vs `subject-index.json` → JSON series array | CI matrix |
| `ingest-catalog` | Additive metadata overlay from DynaReport HTML (axis #3) | offline |
| `ingest-openapi` | Additive 5GC OpenAPI load; clears only `api_*` tables (axis #2) | offline |
| `li-audit` | Verify/relocate LI events against normative text → markdown report | tooling |
| `bench` | Score lexical/hybrid/rerank on a graded query set (axis #7) | tooling |

## Conventions

- Each file opens with a `// Command <name> …` package comment stating contract + usage.
- Ingest-family binaries (`ingest-catalog`, `ingest-openapi`) are **additive**: run
  them *after* `cmd/ingest`; they update/replace only their own tables, never `clauses`.
- Flags use stdlib `flag`; machine-readable output is JSON (`--report json`).
- No business logic here — orchestration only; logic lives in `internal/`.
