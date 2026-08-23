# ADR 0003 — The corpus pipeline is a local, fingerprinted state machine

Status: accepted (2026-08-23)
Supersedes operationally: the scheduled `corpus-*` GitHub workflows and the
Kaggle GPU campaigns (kept in the tree, triggers disabled).

## Context

Corpus production ran as five loosely-chained GitHub workflows plus two Kaggle
GPU campaigns, coordinated by cron and by GHCR image digests rather than by
`needs`. By 2026-08 the chain was continuously red and self-re-arming:

| Workflow | successes | failures |
|---|---:|---:|
| `Corpus · Sparse (Kaggle GPU)` | **0** | **104** |
| `Corpus · Rust embed (Kaggle GPU)` | 2 | 26 (+17 cancelled) |
| `Corpus Data Image` | 0 | 5 consecutive |

Both orchestrators reported success every hour because they only *dispatch* —
they never verify that the dispatched GPU job converged. About 28 failed runs a
day, indefinitely. `corpus-matrix.yml:1059` records the root cause: the merge job
dies of *"No space left on device"* inside the runner agent, leaving no readable
step log.

Three structural problems sat underneath:

1. **Nothing knew what was already done.** Resume was spread over three unrelated
   mechanisms (an Actions cache key, a GHCR baseline image, an auto-rerun
   workflow), none of which could answer "is this artefact still valid?".
2. **The delta anchor was never republished.** `corpus-index.json` has been frozen
   at 2026-06-05 since the June CI cleanup, so the "delta" was measured against a
   photograph and could never converge.
3. **Green did not mean done.** An orchestrator that successfully dispatched a
   failing worker was recorded as a success.

Meanwhile the hardware assumption had changed: a local RTX A4500 (20 GB) is
strictly better than the Kaggle T4 (16 GB) the code targets, available on demand,
and not subject to a 30 h/week quota or a poll window shorter than the kernel's
own budget.

## Decision

**The pipeline becomes a state machine, executed locally, defined once in code.**

### 1. One source of truth

`internal/goal/pipeline.go` declares the steps, their dependencies, what defines
each one, what it produces and how to prove it worked. `make goal`, the `/goal`
command and the status report are thin wrappers. The pipeline is never restated
in YAML, in shell, or in prose — every duplicate description eventually disagrees
with the code, which is how the retired CI drifted.

### 2. Deterministic fingerprints, precise dependencies

```
fingerprint(step) = H(
    step_version
  + implementation_hash      // declared files, content-hashed, CRLF-normalised
  + input_hash               // declared data, size+mtime
  + extra determinants       // embed identity, release floor, index metric…
  + dependencies' fingerprints
  + toolchain identity       // ONLY for steps that declare it
)
```

Precision is the point. A Go upgrade rebuilds the binaries and does not touch the
corpus. A model change invalidates the vectors and the vector index and nothing
upstream. An edit to a `_test.go` re-runs the tests and does not relink eight
binaries (`go build` does not compile them, so they are not determinants of a
build step).

### 3. A step is skipped only under four simultaneous conditions

```
previous.status == success
AND previous.fingerprint == current.fingerprint
AND every declared output exists
AND the cheap validation passes
```

A present file is not proof. A timestamp is not proof. An old success is not
proof. The validation runs on every plan, which is what makes a *corrupted*
output invalidate its step instead of being trusted because its fingerprint —
which describes the inputs — still matches.

### 4. Success is written after the outputs, never before

A step is marked successful only once its declared outputs exist **and**
validate. A worker that exits 0 while producing nothing is a failure. This is the
direct answer to the failure mode that kept the old CI green.

### 5. No hidden failures

There is no `|| true` and no ignored exit code. A step allowed to fail declares
`Optional` at the step level, where it is visible and reviewable, and its failure
is still recorded and reported. Retries are bounded and classified: network
transients back off, deterministic failures are never hammered.

### 6. Merge before embed

The retired CI embedded each shard, then merged. `rust/ingest` rebases `chunk_id`
to ~0 in every shard, so two shards both contain a `chunk_id` 42; a ledger shared
across shards would make one shard's clauses silently skipped
(`rust/embedder/src/main.rs:263`). After the merge, ids are globally unique, so a
single ledger is both safe and optimal — and its content-hash map deduplicates
across every release and series at once.

Measured on the real corpus (2 855 712 clauses): 2 282 337 embeddable clauses for
**833 924 distinct texts** — a 2.74× reduction, with **79.8 % of clauses
duplicated verbatim between releases**.

### 7. The delta anchor is published atomically with the corpus

`merge` writes the new DB and the new `corpus-index.json` together, after it
succeeds. Publishing the anchor first would let a crash leave an index claiming a
corpus state that was never written; the next `discover` would then believe it is
up to date and silently skip real work.

### 8. Locking respects live owners and reclaims dead ones

The lock records pid, host, start time and command. A contender reclaims it only
when the owner is provably gone. A killed run never bricks the project, and a
multi-hour GPU pass is never stomped by a TTL.

## Consequences

- A second immediate run executes **zero heavy steps**.
- An interruption at any point is followed by a relaunch that resumes rather than
  restarts; steps checkpoint internally (fetch per resource, ingest per
  (spec, version) via `ingest_log`, embed per content hash via the ledger).
- A fresh agent with no memory of any prior session reads `.local/state/` and git,
  and can answer what is valid, what changed, and what must run first.
- Cost and latency collapse: no cron chain spanning calendar days, no Kaggle poll
  window, no GHCR round-trip for data that never leaves the machine.

## Not done here (deliberate)

- **The sparse arm stays out.** It is produced but never folded into the served
  layer (`corpus-data-image.yml` contains no reference to `3gpp-sparse`), so a
  successful campaign would still change nothing. Wiring the fold is a separate
  piece of work; spending GPU on an index with no consumer is not.
- **Vectors remain stored per clause.** Deduplicating the *computation* (2.74×) is
  done; deduplicating the *storage* would need a `vectors(content_hash, embedding)`
  table and a join, and DuckDB's VSS indexes a column of a table while
  `internal/store/hnsw.go` asserts the index is `clauses_hnsw`. That is read-side
  surgery, to be done once the chain is green. See `docs/local-pipeline.md` §9.
- **`truncate` windowing is kept.** Changing it flips the embed identity and costs
  a full re-embed; the decision is recorded rather than taken by accident.
