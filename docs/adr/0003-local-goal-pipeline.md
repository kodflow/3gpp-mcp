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

## Amendment — 2026-09-11: a seed pulls a digest, and the publisher moves it

**Context.** `seed` / `seed-etsi` pulled `ghcr.io/<owner>/{3gpp,etsi}-corpus` by
tag, `latest` by default (`MCP3GPP_CORPUS_TAG`, one variable for both arms). A tag
is moved by whoever publishes, so the pipeline's starting point was not a
function of the commit — against §1 of this ADR and CLAUDE.md's "reproducibility
of ingestion" — and the step's record could not say what it had pulled. Measured
on the registry that day: both `latest` were last moved on 2026-08-30 (3GPP
14:48Z, 8.1 GB layer; ETSI 14:33Z, 32 MB layer) with **no dated tag beside them**,
so the snapshot a clone received could not be named after the fact; the only
writer, `scripts/local/publish-corpus.sh`, is run by hand and nothing has run it
since, because the product image packs the corpus from `data/` directly.

**Decision.**

1. **The pin is a file in the tree:** `contracts/corpus-pin.txt`, one
   `ghcr.io/<owner>/<package>@sha256:<hex>` per package. What a fresh clone seeds
   from is decided by a reviewed commit.
2. **The publisher bumps it.** `publish-corpus.sh` reads the digest of what it
   just pushed back from the registry and rewrites that package's line
   (`scripts/lib/corpus-pin.sh`); the operator commits it. Nothing else writes it.
3. **The pull is verified.** The manifest served for a digest reference must hash
   to that digest before any layer is transferred (`bootstrap.ghcrManifest`);
   without the check a pin is a request, not a guarantee.
4. **Two determinants, in two places.** The configured reference (the pin, or an
   override) is an `Extra` of its own arm's seed — offline, so `goal plan` needs no
   network. The digest ACTUALLY pulled exists only after the run, so it is
   recorded through `Ctx.Produced` and folded into the step's provenance: a
   different snapshot replays what stands on it, even under an override naming a
   moving tag.
5. **A bump costs a decline, not a rebuild.** `seed` declines whenever a corpus is
   on disk and a decline carries the previous provenance, so on a machine that has
   built once a pin bump re-runs the two seeds (≈1 s each) and nothing behind
   them. `TestAPinBumpOnAMachineWithACorpusReplaysNothingBehindSeed` holds that.
6. **Overrides stay, per package.** `MCP3GPP_CORPUS_REF_3GPP` / `_ETSI` take a tag
   or a digest; `MCP3GPP_CORPUS_TAG` stays one tag for both and refuses a digest,
   which names one manifest of one package.
7. **The server keeps following `latest`.** `mcp-3gpp bootstrap` / `serve` are
   released on their own cadence and exist to pick up the next corpus without a
   re-release; compatibility is guarded by the embedding identity at open, not by
   the tag. They share the reference grammar and the digest check, and log the
   digest a tag resolved to.

**Not done here.** The published snapshots are twelve days behind the corpus the
image serves; republishing them is a push, and bumps the pin when it happens. The
3GPP delta anchor that `seed` adopts beside a fresh snapshot still comes from the
`latest` GitHub release (`corpus-index.json`, last written 2026-06-05) and is not
paired with the pinned corpus by digest.

## Amendment — 2026-09-11 (second): the anchor is derived from the corpus it describes

**Context.** The amendment above left one artefact of a clone's starting point
untied to the pinned snapshot: the 3GPP delta anchor, which `seed` downloaded from
the `latest` GitHub release. That asset was last written on 2026-06-05 and nothing
republishes it; the "corpus manifest" meant to pair it with a snapshot was verified
by `seed` but never written by anyone. Measured against the corpus the next
snapshot will be published from: the release anchor is behind it on 645 keys and
lacks 140. Run through the real `discover` against the 2026-09-11 status report,
a clone seeding the pinned snapshot gets a work list of **805** (spec, release)
pairs over 18 series with that anchor, against **20** over 6 series with the
anchor derived from the snapshot — the latter identical to this machine's own
work list. Independently, the path that was to regenerate a missing
anchor (`merge --index-out --base <corpus>` with no shard) is refused by merge
before it starts, so it failed `ingest` whenever it was reached.

**Decision.** The anchor is a function of the corpus — `spec|release -> highest
version` over `spec_versions`, the exact rule the fold applies — so it is DERIVED
from the corpus (`cmd/derive-anchor`, `internal/anchor`), never downloaded:

1. `seed`, after pulling a snapshot, derives that snapshot's anchor and replaces
   any anchor on disk, naming the drift (and the over-claims, if any) in the log.
2. A corpus already present keeps the anchor the fold wrote beside it; one with no
   anchor gets one derived (`seed` and `ingest`'s no-fold path alike).
3. An anchor identical to the derived one is not rewritten (discover fingerprints
   it by size and mtime).

Measured on the live corpus: the derivation takes 0.99 s and is byte-identical to
the fold's `.local/corpus-index.json` (554 235 bytes, 20 057 keys). Nothing is
published for it, so `publish-corpus.sh` needs no second artefact and no release
asset is consulted; `corpus-manifest.json` and its reader are removed.

§7 above still holds: the fold publishes corpus and anchor together. This makes
the other producer of the anchor — a seed — hold to the same rule by construction.
