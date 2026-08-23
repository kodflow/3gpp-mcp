# ADR 0001 — Rust writes the corpus, Go reads and serves it

Status: accepted 2026-06-19 (`arch-change`), **reconstructed 2026-08-23**

> **Reconstructed record.** The decision was taken and implemented, but its
> authoritative document never entered the repository: `CLAUDE.md:27` cites a plan
> `rust-writeside-go-readside.md` under `.claude/plans/`, a directory that is
> gitignored (`.gitignore:164`) and absent from disk, and the clone is shallow, so
> it cannot be recovered. Three other plans are cited the same way and are equally
> gone.
>
> This ADR is rebuilt from what survives: the code, the crate headers, the commit
> subjects and `contracts/identity.toml`. Where the original reasoning cannot be
> established it says so rather than inventing it. It exists so the next reader is
> not left with a dangling citation — see finding F16 in
> `docs/audit-resolution.md`.

## Context

The project began as a single Go binary: it scraped, parsed, chunked, embedded,
indexed, merged and served. That gave one language and one build, but coupled the
producer and the consumer of the DuckDB file through shared Go packages, so a
change to the ingestion path could alter the served path — and a change to serve
could not be shipped without dragging the whole write stack with it.

Two forces pushed the split:

- **The write side is a batch, offline, CPU/GPU-bound program.** It runs for
  hours over ~37 GB of documents, wants tight control over memory and
  parallelism, and never has to answer a query.
- **The read side is a latency-sensitive server** that must open the file
  read-only, load frozen indexes and answer in milliseconds. It benefits from
  Go's deployment story (one static binary) and from the MCP SDK ecosystem.

Keeping both in one language made the DuckDB write surface reachable from the
served path, which is the wrong default for a system whose whole promise is
"return cited fragments, never invent".

## Decision

**Rust writes the entire `.duckdb`; Go opens it read-only and serves.**

- Parsing, chunking, ingestion, catalogue/OpenAPI/LI overlays, bulk embedding,
  merging, index building and freezing, and corpus discovery are Rust binaries
  under `rust/`.
- `cmd/server` is the only shipped Go binary. `internal/store` exposes a
  `Reader` to the served path so read-only is a **compile-time** property, not a
  convention (Phase 11a).
- The former Go writers — `cmd/{ingest,embed,embed-io,merge,discover,overlay,
  freeze-hnsw,ingest-catalog,ingest-openapi}` and
  `internal/{ingest,htmlparse,ooxml,openapi}` — were deleted, not deprecated
  (Phase 11b). A shadow implementation that nobody runs is a liability: it drifts,
  and it makes "which one is authoritative?" a live question.

### The two sides agree through an explicit contract, not through shared code

Removing the shared packages removes the accidental agreement they provided, so
the agreement is made deliberate. `contracts/identity.toml` names every value
both sides must compute identically, and each is pinned by a test rather than by
a comment:

- **Schema** — `internal/store/schema.sql` is the single DDL.
  `rust/store/src/lib.rs:25` does `include_str!` on that exact file, so there is
  one schema, not two that look alike.
- **Clause fingerprint** — `sha256(heading + "\n" + text + "|" + embed_identity)`
  truncated to 16 hex, implemented in `internal/embed/apply.go` and
  `rust/embedder/src/hash.rs` and locked byte-for-byte by a golden test
  (`clause_hash("6 X1","body","embedv1-deadbeef0000") == "a2e0978ed24f247a"`).
- **Embed identity** — a digest folding family, revision, tokenizer revision,
  dimension, normalisation, precision, windowing and max_tokens. Computed by the
  Go `cmd/embedid` and handed to the Rust embedder via `--embed-identity`, so
  both sides hash the *same string*. This is the one place the Go read side
  stays inside the write loop, deliberately: the identity the server will check
  against must be produced by the server's own code.
- **Engine** — both must use the same DuckDB line, or a file written by one is
  unreadable (or silently upgraded) by the other. Guarded by
  `scripts/check-duckdb-pin.sh` and proven by the Rust-writes/Go-reads round-trip
  test.

## Consequences

- The served binary cannot write the corpus. Not "does not" — cannot.
- The two sides can be released independently: a serve fix ships without
  re-ingesting, and a parser fix re-ingests without touching serve.
- Every cross-language agreement is now a named, tested contract instead of a
  shared import. Breaking one fails a test rather than corrupting an index.
- The cost is real and was accepted: two toolchains, two dependency graphs, and
  a class of bug that only appears at the seam. One shipped and went unnoticed
  for months — the embed identity mismatch that disabled vector search
  (F01) — precisely because that seam had no test. That gap is now closed, and it
  is the strongest argument for keeping the contract table honest.

## Known drift from this decision, as of 2026-08-23

Recorded here rather than left implicit; details and remedies in
`docs/audit-resolution.md`.

- `CLAUDE.md` §2 claims a single inference implementation. There are two
  (`rust/embedder/src/model.rs` and `rust/embed-core/src/ort_backend.rs`), with
  constants kept in lockstep by hand (F14). Both now load ONNX Runtime
  dynamically, so at least one runtime serves the process.
- `CLAUDE.md` §9 still describes the deleted Go writers (F17).
- The `changes` table lost its producer in the migration: the Rust parser reaches
  the Change History annex and discards it, so a full rebuild empties a table the
  June corpus still populates (F09). This is a genuine regression of the split,
  not an original gap.
