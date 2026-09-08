# The local corpus pipeline

Build, resume and validate the whole 3GPP corpus on one machine. Replaces the
five `corpus-*` GitHub workflows and the two Kaggle GPU campaigns — about 200 KB
of YAML and five artefact channels — with one state machine.

Rationale lives in the ADRs, not here:
[ADR 0001](adr/0001-write-side-rust-read-side-go.md) (Rust writes, Go reads) and
[ADR 0003](adr/0003-local-goal-pipeline.md) (why a fingerprinted state machine).
Findings and their dispositions: [audit-resolution.md](audit-resolution.md).

---

## Quick start

```bash
bash scripts/local/toolchain-bootstrap.sh   # once: portable toolchain into .local/
make goal-plan                              # what would run, and why
make goal                                   # do it (resumes automatically)
make goal-status                            # what is valid right now
```

Nothing needs administrator rights and nothing is installed system-wide: Go,
mingw-UCRT, Rust, libduckdb, ONNX Runtime, the CUDA runtime and LibreOffice all
land under `.local/toolchain/`.

Interrupted? Run `make goal` again. That is the whole recovery procedure.

---

## The steps

```
toolchain ─┬─ build-go ── test
           ├─ build-rust ─────────┐
           └─ build-embedder ──┐  │
                               │  │
  3GPP  seed ─ discover ─ fetch ─ ingest ─ merge ─ embed ─ enrich ─ paragraphs ─ sparse ─ compact ─ index ─ validate ─┐
                                                                                                                     ├─ smoke ─ publish
  ETSI  seed-etsi ─ discover-etsi ─ fetch-etsi ─ ingest-etsi ─ embed-etsi ─ enrich-etsi ─ paragraphs-etsi ─ sparse-etsi ─ compact-etsi ─ index-etsi ─ validate-etsi ─┘
```

**The two arms are the same list twice.** Every data step of the 3GPP arm has a
same-named `-etsi` twin, in the same position, under the same contract. That is
not tidiness: every place the two arms differed was a place the ETSI half silently
went without something the 3GPP half had, and not one of them was found by
something failing — they were found by reading this list and seeing a gap in a
column. The glossary miner was built by `build-rust` and run by no step; the
contract ran on `3gpp.duckdb` and judged the ETSI half by one composite flag; one
shared `compact` declared the ETSI sparse import and not the 3GPP one.
`TestTheTwoArmsRunTheSameSteps` pins the pairing, and its `shared` map is the only
place an exception can be recorded.

`merge` has no twin, structurally: it folds the 3GPP shards, while the ETSI ingest
writes one database directly, so there is nothing to fold. `smoke` and `publish`
are not per corpus either — one server is started over both stores, one image is
pushed carrying both.

`seed` DID appear in that list, with a reason that described the curated seeds in
`enrich` rather than what the step does. `bootstrap.CorpusETSI` existed, was
exported, was tested and was called by `cmd/server` — and by no pipeline step, so
a fresh clone pulled 3GPP from a published snapshot in minutes and rebuilt ETSI
from etsi.org over hours. The exception was the defect, which is why
`TestNoArmExceptionOutlivesItsStep` now reads that map instead of only trusting
it. What `seed-etsi` does NOT yet close: ETSI has no delta anchor, so
`discover-etsi` and `fetch-etsi` still re-enumerate the archive. The expensive
half — `embed-etsi` and `sparse-etsi` — declines against the restored vectors.

| Step | Does | Cost |
|---|---|---|
| `toolchain` | records the compiler identity | instant |
| `build-go` | server + offline tools | ~25 s |
| `test` | `go test ./...` | ~1 min |
| `build-rust` | ingest, merge, overlay, freeze-hnsw, embed-io, compact, discover | ~15 min cold |
| `build-embedder` | GPU dense embedder (ONNX Runtime + CUDA) | ~1 min |
| `seed` | adopt the published 3GPP snapshot **and its delta anchor** | one-off |
| `seed-etsi` | adopt the published `etsi-corpus` snapshot. There is no ETSI anchor to adopt with it, which is why discovery below still re-enumerates | one-off |
| `discover` | diff the live DynaReport catalogue against the local anchor | ~3 s |
| `discover-etsi` | re-enumerate `/deliver` and compare against `etsi-index.json` — not the 3GPP anchor, which is a 3GPP artefact | ~3 s |
| `fetch` | download + convert the 3GPP delta (LibreOffice) | minutes |
| `fetch-etsi` | download + convert the resulting work list (pdftotext) — a work list, not a delta | hours, CPU-bound |
| `ingest` / `ingest-etsi` | parse HTML into DuckDB | minutes/series; ~15 min ETSI |
| `merge` | fold the 3GPP shards into the corpus, rewrite the anchor | minutes |
| `embed` / `embed-etsi` | vectorise on the GPU, reusing every known content hash | **the long pole** |
| `enrich` | DynaReport catalogue, 5GC OpenAPI, LI registry | minutes |
| `enrich-etsi` | mine each deliverable's own Abbreviations clause into the glossary | **21.5 s** (measured 2026-09-08 over all 5 142 deliverables; it was 2 h 29 before the quadratic fix) |
| `paragraphs` / `paragraphs-etsi` | store each paragraph once and point at it (ADR 0004) | ~9 min |
| `sparse` / `sparse-etsi` | learned lexical postings (additive layer) | ~30 min |
| `compact` / `compact-etsi` | rewrite the corpus without its dead space — **declines** when there is nothing to reclaim | ~30 min, or 0 |
| `index` / `index-etsi` | build and freeze the HNSW cosine index | RAM-bound |
| `validate` / `validate-etsi` | the data-completeness contract (+ `anchorcheck` on the 3GPP arm) | seconds |
| `smoke` | start the real server over both stores, call real tools, assert vector search stays on | seconds |
| `publish` | compose the OCI image and push it — declines without a registry credential | ~25 min |

`publish` is a STEP, not a separate entry point. The image was the only output of
this repository with no determinants: nothing could say whether what consumers
pull was the corpus the machine had built, and twice in two days it went out
stale or unbootable while every local gate was green. It now depends on `smoke`
**and** `index-etsi` — both halves frozen and proved — and `make plan` states
whether the published image is behind the corpus before anything runs.

---

## What makes it resumable

A step is skipped **only** when all four hold:

```
the previous run succeeded
AND the fingerprint is unchanged
AND every declared output exists
AND the cheap validation passes
```

A present file is not proof. A timestamp is not proof. An old success is not
proof. The validation runs on every plan, which is what makes a *corrupted*
output invalidate its step rather than being trusted because its fingerprint —
which describes the inputs — still matches.

Fingerprints are precise on purpose. A Go upgrade rebuilds the binaries and does
not touch the corpus. A model change invalidates the vectors and the vector index
and nothing upstream. Editing a `_test.go` re-runs the tests and does not relink
eight binaries.

Long steps checkpoint internally, so an interruption costs minutes, not hours:

| Step | Resume unit |
|---|---|
| `fetch` | per resource — `corpus.sh` skips what is already downloaded and converted |
| `ingest` | per `(spec, version)` via the `ingest_log` table, stamped `PIPELINE_VERSION` |
| `merge` | per `(spec_id, release)` bucket via `--base` |
| `embed` | per clause **and per content hash**, via `.local/vecs/ledger.jsonl` |

State lives in `.local/state/steps/*.json`, written tmp→fsync→rename. A fresh
process — a new agent, a new terminal, a rebooted machine — reads those files and
git, and can say what is valid, what changed and what must run first. Nothing
depends on anyone remembering anything.

---

## The measured payoff: content-addressed reuse

Measured on the real corpus (2 855 712 clauses), not estimated:

| | |
|---|---:|
| Embeddable clauses | 2 282 337 |
| **Distinct texts** | **833 924** |
| **Dedup factor** | **2.74×** |
| Duplicated **between releases** | **79.8 %** |
| Dense vectors: raw → deduped | 9.35 GB → 3.42 GB |

The mechanism is **content-addressed reuse**, and the brick already existed:

```
clause_hash = sha256( heading + "\n" + text + "|" + embed_identity )[:16]
  Go   internal/embed/apply.go:38
  Rust rust/embedder/src/hash.rs:22   (byte-for-byte, golden-tested)
```

`rust/embedder` keeps two maps over the ledger: `chunk_id`s already written
(exact resume) and content-hash → vector. A clause whose text was already
embedded under a different `chunk_id` — another release, another series — is
filled by copy and never reaches the GPU.

**This is why merge comes before embed.** `ingest` rebases `chunk_id` to ~0 in
every shard, so two shards both contain a `chunk_id` 42; a ledger shared across
shards would silently skip clauses on that collision
(`rust/embedder/src/main.rs:263`). After the merge the ids are globally unique,
so one ledger is both safe and optimal.

The pipeline deduplicates the **computation**. Deduplicating the **storage** —
a `vectors(content_hash, embedding)` table, worth 5.93 GB — is deliberately not
done yet: DuckDB's VSS indexes a column of a table, and `internal/store/hnsw.go`
asserts the index is `clauses_hnsw`, so it is read-side surgery. See ADR 0003.

---

## Scope knobs

```bash
make goal ARGS="--scope '23 24 29 33 38'"   # restrict the series
make goal ARGS="--embed-floor Rel-19"       # vectorise only recent releases
make goal ARGS="--full"                     # ignore the anchor, reindex everything
.local/bin/goal run --from merge            # this step and everything after
.local/bin/goal invalidate embed            # forget a step; it and its dependants replay
```

`--embed-floor` is the practical lever. Measured throughput of BGE-M3 fp32 on an
RTX A4500 is **≈63 clause/s** end-to-end over a campaign (14 217 clauses in
3 min 40 s: 206 clause/s on the short head of the length-sorted work-list,
63 clause/s on the 1024-token tail). At that rate the full corpus is roughly a
ten-hour campaign, not the multi-day one this page previously claimed — the old
**≈7.5 clause/s** figure was measured while the CUDA arena was thrashing into
WDDM shared memory (F27), so it recorded the bug, not the card. Lowering the
floor later costs nothing that was already computed: the ledger is keyed by
content, and 79.8 % of what an older release contains is text a newer one
already has.

The rest of the step is not free either, and it does not scale with the floor:
importing the ledger into DuckDB ran at **≈120 vector/s** (48 320 vectors, 6 min
30 s, +229 MB on disk) because it rewrites every vector in the ledger, not just
the new ones.

---

## Hardware notes

| | |
|---|---|
| GPU | An RTX A4500 (20 GB) exceeds the T4 (16 GB) the code was written for. `rust/embedder` sizes its batches from `nvidia-smi`; no tuning needed. |
| RAM | The HNSW build needs the vectors in memory (2.85 M × 1024 × f32 ≈ 11.7 GB). What matters is RAM **+ swap**: CI managed it on a 7 GB runner with a 28 GB swapfile. |
| Disk | The full corpus is ~37 GB of archives **plus** ~37 GB of converted HTML. `fetch` deletes each archive once its HTML exists (`KEEP_ZIP=1` to disable) — that is a feasibility condition, not an optimisation. It is what filled the CI runner and turned every scheduled build red. |
| CPU | LibreOffice conversion is the wall-clock bottleneck, not the GPU. `--jobs` defaults to **6**. Measured 2026-08-26 by an A/B/B/A over the same 28 documents on an idle machine: **225 s at `--jobs 4`, 178 s at `--jobs 6`** — 6 is 21 % faster, and the two 6-runs agreed to the second. This **reverses** F33 in `docs/audit-resolution.md`, whose numbers (4.9 vs 2.4 conversions/min) came from windows sampled mid-run rather than from a controlled comparison. RAM is not the limit at either setting. Caveat: the benchmark times conversion only, on an already-downloaded sample. |

---

## Windows and Linux

Linux/WSL2 is the reference target — it is what CI and the images build. The
whole chain also runs natively on Windows, which the audit had concluded was
impossible; that conclusion was too strong (see F13). Five things make it work,
each a trap on its own:

1. **The C toolchain must be UCRT.** `duckdb.dll` links UCRT; a msvcrt-based
   mingw gives the process two heaps and it dies with `0xC0000374` on the first
   query — with a perfectly green build.
2. **DuckDB is linked dynamically** (`-tags duckdb_use_lib`). The prebuilt static
   libraries are MSVC-built and export MSVC STL symbols no mingw can resolve; the
   C API bridges the two.
3. **ONNX Runtime is loaded dynamically** and its CUDA provider needs the CUDA 12
   and cuDNN 9 redistributables beside it. The CUDA directory must be on the PATH
   of the embedder *and nothing else* — adding it globally makes other tools die
   with `0xC0000139`.
4. **The CUDA arena must be capped.** WDDM does not refuse an allocation that
   exceeds physical VRAM — it serves it from shared system memory. An embedder
   whose arena outgrows the card therefore never sees the OOM its backoff waits
   for; it just thrashes over PCIe and looks hung (F27). `--vram-fraction` is
   passed to the execution provider as a hard arena limit, so the overshoot is an
   error the adaptive batcher can absorb. Look for `RESULT arena_cap` in the log.

5. **Every native binary needs NATIVE paths — this is the general rule, and 3
   and 4 are instances of it.** A POSIX path handed to a Windows executable does
   not produce an error. `soffice --convert-to` given
   `-env:UserInstallation="file:///tmp/…"` exits **0** and writes nothing; the
   caller sees no output and no failure. That cost ten minutes of four workers
   producing zero conversions, because each miss then burned four recovery
   attempts at a 900 s timeout (F32). `scripts/lib/convert.sh` translates with
   `cygpath -m` where it exists and passes paths through unchanged elsewhere.
   **Suspect this first** whenever a native tool reports success and produces
   nothing.

The first three are handled by `scripts/local/toolchain-bootstrap.sh` and
`scripts/local/toolchain-env.sh`; the fourth lives in `rust/embedder`, the fifth
in `scripts/lib/convert.sh`.

One more thing this platform gets wrong quietly: **a guard whose input cannot be
computed reports the guarded condition as violated.** `flock` is absent from Git
Bash, so a lock that could not be taken was announced as "another run in
progress". `df -BG --output=avail` returns nothing under the toolchain
environment, so a disk that could not be measured was announced as full — with
the number missing from its own message. Both now separate "cannot determine"
from "condition failed", and so should anything added here.

---

## The two overlays `enrich` cannot invent

`enrich` folds three sources into the corpus. The DynaReport catalogue is
derived from what `discover` already fetched; the other two are external and
have their own scripts, because the corpus is useless as a *retrieval* target
for them if they are missing and nothing says so:

```bash
./scripts/fetch-5g-apis.sh auto   # 5GC OpenAPI YAMLs -> data/sources/5g-apis
./scripts/fetch-li-asn.sh         # TS 33.128 ASN.1   -> data/sources/asn
```

- Without the first, `enrich` logs *"no data/sources/5g-apis — skipping the
  OpenAPI overlay"* and `search_api` answers from whatever a previous merge left
  behind. A from-scratch corpus would answer from nothing.
- Without the second, it logs *"no TS33128Payloads .asn found — li_events stays
  empty"* and the `li_events` tool returns nothing at all. The modules are not
  published on their own: they ride in a zip inside the zip of TS 33.128, and
  `fetch-li-asn.sh` reads the version code from the HTML the corpus already
  holds so the registry describes the same version as the text.
- `fetch-5g-apis.sh` needs a Python 3 for JSON. The Windows toolchain provisions
  none and `python3` on PATH is the Store stub — it prints an advert and exits
  non-zero — so the script accepts `PYTHON=…`, otherwise falls back to the
  interpreter bundled with LibreOffice, and **refuses to run** rather than fetch
  into empty JSON.
- **A Windows Python ends every `print()` with CRLF.** `$( )` strips the LF and
  keeps the CR, so that byte rides into every filename and every URL: the
  file-exists test misses files that are on disk, and `curl` reports `http=000`
  for blobs that answer 200 by hand. One `tr -d '\r'` where the interpreter is
  read is the fix — same shape as the soffice pitfall above.
- It takes a release as **one archive**, not as 484 raw blobs: ~40 requests for
  a full six-release fetch instead of ~3000. Per-file download stays as the
  fallback, retries with backoff, and never retries a 403/404 — on 3GPP hosts a
  403 *is* a 404.
- Measured: 1774 YAML files over REL-15..REL-20 and 405 LI events (Rel-19), both
  `missing=0`, in minutes.

---

## Serving the finished corpus

`smoke` proves the server starts, so the same command line is what a client
should use. `.mcp.json` at the repo root wires it into any `mcpServers` client:

```json
{ "mcpServers": { "3gpp": { "type": "stdio",
    "command": ".local/bin/server.exe",
    "args": ["serve", "--db", "data/3gpp.duckdb", "--etsi-db", "data/etsi.duckdb"] } } }
```

- **On Linux, drop the `.exe`** — `goal` names its binaries with the host's
  extension, so the file is `.local/bin/server`.
- `--etsi-db` attaches the ETSI Lawful-Interception corpus **alongside**, never
  merged: `get_spec`/`list_releases` route `ETSI …` ids there and `list_specs`
  unions both. It degrades to 3GPP-only on a missing or unreadable file, which
  is why `smoke` now asserts the "ETSI corpus attached" line rather than trusting
  the flag.
- The server needs no toolchain environment: `duckdb.dll` sits beside the binary
  and the embedder is off by default (`embedder=false`).
- For a hosted deployment, `.claude/skills/3gpp/SKILL.md` documents the
  Streamable-HTTP path (`claude mcp add --transport http 3gpp <origin>/mcp`).

---

## What is deliberately not here

- **The sparse arm.** It is produced but no consumer folds it into the served
  layer, so a successful campaign would change nothing. Wire the fold first
  (F04); spending GPU on an index with no consumer is the mistake.
- **fp16.** Precision is part of the EmbedIdentity, so switching costs a full
  re-embed. fp32 is chosen once, on purpose (F12).
- **`mean_pool` windowing.** Done (#208). `truncate@1024` dropped the tail of
  long clauses; the Rust embedder now windows a clause that does not fit whole and
  re-splits any window still reaching max_tokens. Measured on the 2026-08-29 run
  at the 300-word / 1024-token pairing: 2 771 904 windows over 2 207 218 clauses,
  131 932 of them multi-window, 49 277 forced token splits, and
  `truncated_windows = 11` — not 0, as this line used to claim. The 11 are the 11
  `unsplittable`: a single word longer than the model's context, typically a
  space-free ASN.1 blob, where there is nothing left to split. That is 0.0004% of
  the windows against the tails of 131 932 clauses before the fix.
- **2048 tokens / 600-word windows.** The pairing moved after the 1024 cap was
  traced to #196, which sized it for a Kaggle T4 at a fixed batch of 64 — a
  premise retired twice over (no T4 path, and 2b1482b made the batch VRAM-aware
  with OOM backoff). TeleEmbedBench (arXiv 2604.17778) measures 2048 as optimal
  for 3GPP specifically. Expect the multi-window count to fall sharply; re-read it
  from `RESULT windowing` rather than assuming.
- **Identity history.** `61ba446c0814` (truncate@1024) -> `6bf1f9a47710`
  (mean_pool@1024, #208) -> `38067f8c6efe` (mean_pool@2048). Each step is a full
  re-embed, taken deliberately before a campaign (F15).
