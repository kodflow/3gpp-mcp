<!-- updated: 2026-06-20 (arch-change: write-side Rust, read-side Go) -->
# cmd/ — Binaries

## Purpose

Entry points for the project. One sub-directory per binary; each `main.go` is a
thin CLI that wires `internal/` packages together. Only `server` ships to users
(the `mcp-3gpp` mono-binary, CLAUDE.md §1); the rest are offline / CI tooling.

**Write-side is Rust** (CLAUDE.md §2, arch-change 2026-06-19): the DuckDB
producers — ingest, embed, merge, discover, catalog/openapi/LI overlays — are
Rust binaries under `rust/` (`rust/ingest`, `rust/store` bins, `rust/embedder`,
`rust/discover`). The Go `cmd/` now holds the served binary plus read-only /
offline tooling. There is no Go DuckDB writer on the served path
(`internal/store.Reader`, Phase 11a).

## Structure

```text
cmd/
├── server/          # MCP server over stdio/HTTP + `bootstrap` provisioning subcommand (SHIPPED)
│   ├── main.go
│   └── bootstrap.go
├── validate/        # assert a baked DB meets the data contract (dense|+sparse|+etsi)
├── bench/           # offline retrieval benchmark (IR metrics); never shipped
├── li-audit/        # cross-check an external LI event catalogue vs indexed text (read-only)
├── dbcount/         # row-count utility over a DB (read-only)
├── embedid/         # print the canonical dense/--sparse EmbedIdentity (CGO-free resolver)
├── export-delta/    # export an incremental delta asset from a DB (offline)
├── split/           # split a DB into per-axis shards (offline)
└── discover-etsi/   # ETSI deliverable crawl/worklist (separate from the 3GPP Rust discover)
```

## Binaries

| Binary | Role | Ships? |
|--------|------|--------|
| `server` | MCP server (stdio/HTTP); `serve` / `bootstrap` / `version` subcommands; opens the DB read-only (`--writable` maintenance hatch) | ✅ user-facing |
| `validate` | Gate a baked DB against the data contract (`--require-embed-complete` / `--require-sparse` / `--require-etsi`) | CI |
| `bench` | Score lexical/hybrid/rerank on a graded query set (axis #7) | tooling |
| `li-audit` | Verify/relocate LI events against normative text → markdown report (read-only) | tooling |
| `dbcount` | Print row counts for a DB (read-only) | tooling |
| `embedid` | Resolve the active model FAMILY to its dense / `--sparse` EmbedIdentity digest (CGO-free; feeds `rust/discover --embed-identity`) | CI |
| `export-delta` | Export an incremental delta asset from a DB | offline |
| `split` | Split a consolidated DB into per-axis shards | offline |
| `discover-etsi` | Enumerate ETSI deliverables (crawl/worklist) for the ETSI pipeline | CI |

The former Go write-side binaries — `ingest`, `embed`, `embed-io`, `merge`,
`discover`, `ingest-catalog`, `ingest-openapi` — were removed in Phase 11b;
their Rust replacements live under `rust/` and own all DuckDB writes.

## Conventions

- Each file opens with a `// Command <name> …` package comment stating contract + usage.
- The served binary writes nothing: `internal/{mcp,search}` consume `store.Reader`
  (compile-time read-only, Phase 11a). Offline tools (`split`, `export-delta`,
  `bench` synth) may open `store.Open` (rw); `li-audit`/`dbcount` open read-only.
- Flags use stdlib `flag`; machine-readable output is JSON (`--report json`).
- No business logic here — orchestration only; logic lives in `internal/` (read)
  or `rust/` (write).
