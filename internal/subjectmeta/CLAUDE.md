# internal/subjectmeta — Subject Footprints (CGO-free)

## Purpose

The CGO-free source of truth for **which domain subjects exist, what version each
is at, and which 3GPP series each owns** — the metadata the incremental corpus
build uses to detect a *changed subject* and re-index only its series.

Separate from `internal/subject` + `internal/registry` because `cmd/discover`
runs **without CGO** (it only fetches + diffs the 3GPP status report) and so
cannot import the concrete subjects, which transitively pull in the DuckDB CGO
store. `subjectmeta` has zero CGO deps, so `discover`, `merge`, and `ingest`
all share it.

## Structure

```text
subjectmeta/
├── subjectmeta.go      # Meta{Name,Version,Series}, All, Footprint, Index, ChangedSeries
└── subjectmeta_test.go # lockstep-vs-registry guard + footprint/ChangedSeries cases
```

## Contract

- `All` lists every subject. **Kept in lockstep with `registry.Default()`** by
  `TestSubjectMetaMatchesRegistry` — a subject cannot be added to one without the
  other, so a subject change can never become invisible to `discover`.
- `Footprint(m)` = short sha256 over the subject's version (widen later by feeding
  more bytes, e.g. seed-file hashes; the published JSON shape stays the same).
- `Index()` = name→footprint, serialised by `merge --subject-index-out` to
  `subject-index.json` (published on the `latest` release).
- `ChangedSeries(published)` = series owned by any subject whose published
  footprint differs from current code; an empty map returns all subject series
  (once-only re-index after this feature first ships).

## Conventions

- **BUMP a subject's `Version`** whenever its extraction logic changes → its
  footprint shifts → `discover` folds its series into the next delta matrix → the
  shard rebuilds and the normal ingest pass re-extracts the subject (no full
  corpus rebuild, no re-embed of unrelated series).
- Pure data + hashing only; no DB, no network, no CGO. Keep it that way so
  `discover` stays CGO-free.
