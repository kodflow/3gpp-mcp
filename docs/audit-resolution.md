# Audit resolution register

Every finding from the 2026-08-23 resumption audit, **re-verified against the
code as it stands** and given an explicit disposition. Nothing here is taken on
the audit's word: each line was checked by reading the cited file, and four
findings turned out to be inaccurate as stated.

| Verdict | Meaning |
|---|---|
| `CONFIRMED` | true as described |
| `PARTIAL` | real, but the statement was inaccurate — the correction is in the entry |
| `OBSOLETE` | no longer true; the proof is in the entry |
| `REFUTED` | never true |

| Disposition | Meaning |
|---|---|
| `FIXED` | corrected in this branch, with a test or a runnable proof |
| `FIX_LATER` | real, off the critical path of a local build; the exact remedy is recorded |
| `ACCEPT` | kept deliberately, with the technical justification |
| `NONE` | nothing to do |

**Summary — 33 findings.** 8 fixed, 1 fixed by construction, 3 accepted,
1 obsolete, 20 recorded with an exact remedy. Three of the eight fixed were
blocking the product outright: F01 served every valid corpus as lexical-only,
F03 made `bootstrap` a guaranteed 404, and F27 turned the GPU embed campaign
into a silent hang.

---

## Fixed

### F01 — The served binary disabled vector search on every valid corpus
`CONFIRMED` · `FIXED` · commit `7f59986`

`internal/embed/embed_ffi.go:45` returned the bare family name `"bge-m3"`, while
the corpus stamps `schema_meta.embedding_model` with what `cmd/embedid` prints —
a 12-hex EmbedIdentity digest folding family, revision, tokenizer revision,
dimension, normalisation, precision, windowing and max_tokens
(`internal/embed/identity.go:135`). `cmd/server/main.go:192` compares the two and
calls `store.DisableVSS()` when they differ, so the comparison failed for *every*
correctly-built corpus: a DB full of valid vectors was served as pure lexical,
silently, with one line on stderr as the only trace.

**Fix.** The mapping moved to `internal/embed/ffi_identity.go`, deliberately
untagged and CGO-free, so the contract is covered by a plain `go test ./...` —
no cgo, no ONNX Runtime, no prior `cargo build` of `rust/embed-core`. Nothing
pinned the two sides together before, which is exactly why the regression
shipped unnoticed. `SparseModelID()` had the same defect (`"bge-m3-sparse"`) and
was fixed with it.

**Proof.** `TestFFIModelIDMatchesStampedIdentity` asserts equality with
`ResolveModelID("bge-m3")` *and* that the value is never the bare family name.
Three sibling tests pin the hash seam, the default branch and the sparse twin.

### F03 — `mcp-3gpp bootstrap` always 404s
`CONFIRMED` · `FIXED`

`cmd/server/bootstrap.go:18` used
`…/releases/latest/download/3gpp.duckdb.zst`. GitHub's `/releases/latest/` alias
resolves to the most recent non-prerelease — the `models` tag, not the literal
`latest` tag — so the URL redirects to a release that does not carry the asset.
Verified live: `302` to `…/releases/download/models/3gpp.duckdb.zst`, then `404`.

The project already knew: `corpus-image.yml:193` says *"the runtime DB-bootstrap
URL — releases/latest/download — 404s, so an empty light would have no DB"*, and
worked around it by baking the DB into the image rather than fixing two
constants. `scripts/install.sh:35` had the same bug for the binaries.

**Fix.** Both constants and `install.sh` now use `…/releases/download/latest/…`,
the form already used correctly in `scripts/finalize-corpus.sh:55`.

### F26 — The DuckDB pin guard could not fail (found during this resumption)
`PARTIAL` · `FIXED` (guard) + `FIX_LATER` (dependency alignment) · commit `ef8ef74`

Not in the original audit. `scripts/check-duckdb-pin.sh` claimed to prove that
the Rust writer and the Go reader share a DuckDB engine. It compared
**declarations**, not resolutions:

- it grepped `duckdb = { version = "1.4" }` from `rust/store/Cargo.toml` and
  concluded "Rust is on 1.4.x". But that is a caret requirement, and the duckdb
  crate moved to a `1.<MM><PP>.0` numbering — `rust/Cargo.lock` actually resolves
  **`duckdb 1.10504.0`, i.e. DuckDB 1.5.4**;
- it inferred the Go engine from a comment about `go-duckdb`, while the engine
  really comes from `duckdb-go-bindings`, pinned in `go.mod` at `v0.10503.0` —
  **DuckDB 1.5.3**.

So both sides had moved to the 1.5 line while the guard printed *"pin OK: Rust +
Go both on DuckDB 1.4.x"*. A guard that cannot fail is worse than no guard.

**Fix.** The script now reads `rust/Cargo.lock` and the module graph, decodes
both numbering schemes, and additionally catches a straddle it was blind to: the
platform bindings sit at `v0.1.24` (DuckDB 1.4.3) while the root supplies 1.5.3
headers — and `go-duckdb/mapping@v0.0.21/mapping_windows_amd64.go:6` imports the
**old** family while `duckdb-go-bindings@v0.10503.0/prebuilt_windows_amd64.go`
imports the **new** one. A static build therefore links two engines.

**Remaining work (`FIX_LATER`).** Removing the straddle needs a coordinated move:
`go-duckdb/v2` has no release past `v2.4.3`, and its `mapping` module pins the old
family, so either the bindings root comes back down to `v0.1.24` (and
`rust/store` is pinned to the matching 1.4 line, re-resolved and re-round-tripped)
or `go-duckdb` ships a release using `lib/*`. The local build is unaffected: it
uses `-tags duckdb_use_lib`, where no platform module is linked at all and a
single supplied `libduckdb` decides. The guard now fails loudly until this is
settled — `ci.yml:182` runs it, so the alignment and the CI change land together.

### F27 — The GPU embed campaign hung instead of backing off (found during this resumption)
`CONFIRMED` · `FIXED`

Not in the original audit; found by resuming the campaign it blocks. The first
real embed run reached 8918/26350 at ~1500 clause/s, then produced **nothing for
41 minutes** and had to be killed (exit `0x40010004`). The log tells the whole
story: the CUDA BFC arena is extended ~40 times in 30 seconds, each block a
little larger than the last, up to `Total allocated bytes: 21381367808` — 21.4 GB
on a 20 GB card.

Two independent mechanisms combine:

- `rust/embedder/src/model.rs` selects `ArenaExtendStrategy::SameAsRequested`, on
  purpose: it keeps a single batch's peak predictable for the dynamic batcher.
  But the arena never *releases* a block, and `main.rs` **length-sorts** the
  work-list, so every successive batch asks for a strictly larger block that no
  freed block can satisfy. Arena growth is therefore monotonic by construction —
  the two design choices are individually right and jointly unbounded.
- Nothing capped it. `--vram-fraction` only sized *our* batches; it was never
  passed to the execution provider.

On Linux the overshoot would surface as a plain allocation failure, and
`run_adaptive` (`main.rs:508`) already knows how to absorb one: `shrink(1.5)`,
split the batch, retry. Under **Windows WDDM** the driver instead satisfies the
allocation out of *shared system memory*. No error is raised, so the backoff can
never fire; the run degrades to PCIe thrashing and looks, from the outside,
exactly like a hang. The recovery path the code was designed around was
unreachable on the only machine that runs it.

**Fix.** `Bge::load` takes a `mem_limit` and applies
`CUDAExecutionProvider::with_memory_limit`; `main.rs` probes the GPU *before*
committing the session and caps the arena at `total_vram × --vram-fraction`
(logged as `RESULT arena_cap`). The base is **total**, not free: the arena holds
the weights too, and the post-load `gpu::detect` that sizes batches already
measures free VRAM after they are resident — using free for both would
double-count them. Capping turns the overshoot back into an ordinary OOM, which
the adaptive batcher absorbs, so the campaign self-corrects to the real card
instead of stalling.

**And the cap alone was not enough.** With the arena capped, the very next run
failed at batch `[2048, 2560)` — no longer a hang, but still an abort:

```
BFCArena::AllocateRawInternal Available memory of 1177295872
is smaller than requested bytes of 1233125376
```

A *capped* arena reports exhaustion in its own words. That string carries none
of the CUDA driver wordings `is_oom` (`main.rs:557`) matched — not "out of
memory", not "failed to allocate", not "bad_alloc" — so `run_adaptive` treated
it as a fatal error and killed the campaign. The cap had converted a silent hang
into an immediate abort, which is better but still wrong: the same
guard-that-cannot-fire shape as F26. `is_oom` now recognises the BFC-arena
wordings, and three unit tests pin all three sides — the real message verbatim,
the classic CUDA wordings, and a shape error that must still abort loudly.

**Proof.** `cargo test --bin embedder` (21 tests) plus a re-run of
`goal run --only build-embedder,embed --embed-floor Rel-20`, resuming from the
ledger the killed run left behind.

### F28 — `build-embedder` staged a binary that could not start
`CONFIRMED` · `FIXED`

`stageRuntimeDLLs` copies the mingw runtime (`libstdc++-6`, `libgcc_s_seh-1`,
`libwinpthread-1`) next to the Rust binaries, because a `*-pc-windows-gnu` build
links them dynamically and they exist only on the build shell's PATH. It ran at
the end of `build-rust` only — and `build-embedder` does **not** depend on
`build-rust` (`Deps: ["toolchain"]`, deliberately: a GPU-less machine skips it).
So `goal run --only build-embedder,embed` against a clean `.local/rust-bin`
staged an `embedder.exe` that dies with `0xC0000139` before printing a line. It
was masked here only because an earlier `build-rust` had already run.

**Fix.** `build-embedder` calls `stageRuntimeDLLs` too. It is idempotent, so the
overlap costs one file copy.


### F29 — The step graph denied that a seeded corpus can be vectorised
`CONFIRMED` · `FIXED`

`data/3gpp.duckdb` has **two** legitimate producers: `merge` folds locally
ingested shards into it, and `seed` downloads the published snapshot instead —
which is the entire point of seeding, since it buys the corpus without 37 GB of
archives and ~30 h of LibreOffice. `stepEmbed` declared only
`Deps: ["merge", "build-embedder"]`.

The inaccuracy was not the damage. Because the graph made the supported path
unreachable, operators reached `embed` with `--only` — and `--only` does not
merely reorder the plan, it skips dependency checking entirely. Two consecutive
sessions did exactly that, and the state file recorded

```json
"dependencies": { "build-embedder": "cbbf03…", "merge": "missing" }
```

with nothing objecting. **A graph that must be bypassed to do a supported thing
trains people to bypass it.**

**Fix.** A new `Step.AnyDeps` models alternative producers of one artefact:
every alternative orders before the step and folds into its fingerprint exactly
as a `Dep` would, any of them re-running marks the step dirty, and — the half
that makes it a guard rather than a relaxation — **at least one must have
SUCCEEDED or the step refuses to run**. That check lives at execution time, not
planning time, precisely because `--only` bypasses the plan. `embed` now
declares `Deps: ["build-embedder"], AnyDeps: ["merge", "seed"]`.

**Proof.** Five tests: either producer satisfies it; neither produces a refusal
naming what is missing; switching producer replays the consumer (a seeded corpus
is not a merged one); a single `AnyDeps` entry is rejected as a disguised `Dep`;
an unknown name is rejected like an unknown `Dep`.

### F30 — The delta anchor claims 56 specs it has no text for
`CONFIRMED` · `FIX_LATER` (detector shipped; the holes need a fetch)

`seedAnchor` warns that an anchor over-claiming in the optimistic direction makes
discover skip specs that were never ingested, and that "no later step notices —
the corpus simply has a hole". That is not hypothetical. Verified with
`cmd/anchorcheck` against the published snapshot:

```
anchor_keys=19917 catalogue_keys=19917 clause_keys=19800
consistent=19800 filing_artefact=61 over_claim=0 hole=56
```

Against `spec_versions` the anchor is **exact** — 19 917 keys, zero divergence in
either direction. Against `clauses` — the text actually indexed — 117 keys have
no clause. 61 are filing artefacts (3GPP lists a spec's Rel-N entry at the
Rel-(N-1) version, and the text is indexed under the neighbour). The remaining
**56 have no clause anywhere for that spec+version**, concentrated in series 29
at Rel-20: `29.502`, `29.503`, `29.518`, `29.520`, `29.531`, `29.558` — core 5GC
service-based-interface specs.

**32 of the 56 are incidentally covered** by the drift list, because the site has
moved past the anchored version. **24 are not**: the site version *equals* the
anchor version, so `dump_drift` sees nothing and never will.

**Why nothing broke yet — and why that is fragile.** `emit_worklist`
(`rust/discover/src/lib.rs:74`) applies **no version comparison at all**: it
emits every spec of every selected series. That is why the work-list holds 20 225
entries when `dump_drift` reports only **986** genuine changes (327 missing +
659 stale). The 20× over-fetch is what accidentally fills the 56 holes. **The
obvious optimisation — filter the work-list by the drift set — would make all 56
permanent.** Any such change must consult `anchorcheck` first.

**Shipped.** `cmd/anchorcheck` compares the anchor against both `spec_versions`
and `clauses`, separates filing artefacts from true holes, and exits 1 on a hole
so it can gate. Three unit tests pin the classification and the numeric version
ordering (`19.14.0 > 19.9.0`, which string comparison gets backwards — and 3GPP
reaches double-digit minors routinely).

**Remaining.** The 56 holes need those specs fetched and ingested. Scoped to the
drift set plus the uncovered holes that is ~1 010 specs, not 20 225.


### F31 — The nine review findings, closed
`CONFIRMED` · `FIXED`

A second reviewer went over `docs/…` and the runbook and raised nine structural
points. Eight were correct as stated, one inverted a diagnosis, and one treated a
recorded decision as an oversight. All nine are now addressed.

**The root correction: upstream state ≠ corpus state.** One value — "the anchor
names version X" — was doing duty for two independent propositions, "upstream is
at X" and "the corpus holds X". `cmd/anchorcheck` now speaks a deliberately
different vocabulary from the anchor: `indexed`, `non_content`, `over_claim`,
`missing_content`, persisted to `.local/state/corpus-state.json` via
`--emit-state`. `non_content` is an explicit state, not an implicit exception —
the 61 legitimate absences can no longer be silenced by a filter that would take
the 56 real gaps with them.

**The repair set is now a primitive.** `discover --repair-plan --holes <file>`
computes `upstream_drift ∪ corpus_holes` in one command, and reports each
population so the identity `emitted = (missing + stale) + holes − overlap` is
checkable by eye. Measured: `upstream_missing=327 upstream_stale=659
corpus_holes=46 overlap=32 → repair_specs=1000`, against 20 225 for the
series-wide worklist. It also surfaced a fact nobody had: **10 of the 56 holes
are absent from the status report entirely** and cannot be fetched from it. They
are counted and named, never dropped.

`scripts/corpus.sh --worklist FILE` consumes that plan verbatim, and
`goal run --repair` wires the whole chain, computing the holes inside the step
rather than trusting a caller-supplied list — a repair plan is only proportionate
*because* it carries the holes.

**Guards that can now fail.**

- `validate` runs `anchorcheck` against `contracts/accepted-absences.txt`. An
  unfetchable key is a decision on the record with a reason; anything else is a
  build failure with `goal run --repair` as the remedy. A stale accept entry is
  reported too.
- The served binary **refuses to start** on an embedding-model mismatch
  (`--allow-lexical-fallback` / `MCP3GPP_ALLOW_LEXICAL_FALLBACK` to accept the
  degraded mode deliberately). Silent degradation to lexical was the exact
  contradiction of the runbook's own rule.
- `--only` no longer implies "trust me": preconditions are checked at execution
  time, where `--only` and `--from` actually bypass the plan. `--force-only`
  overrides them in red, and states in the log that the result is not
  reproducible from the repository's own definition of done.
- The embedder emits `RESULT gpu_evidence arena_cap=… peak_batch=… oom_splits=…
  final_k_attn=…`. `nvidia-smi` samples from outside and cannot be asserted on;
  this is produced by the process that did the work. Verified on a 50-clause run:
  `arena_cap=17171480576 peak_batch=49 oom_splits=0`.
- `corpus-manifest.json` digests the DB **and** the anchor in one document.
  `seedAnchor` already proved the DB against its sidecar; the anchor had no
  checksum at all, so an authentic corpus could still be paired with an anchor
  from another generation. A missing manifest is tolerated (legacy publishes) and
  **said out loud**, because a silent fallback to the unverified path reads
  exactly like a verified one.
- `scripts/snapshot-smoke.sh` downloads the published artefact into an empty
  directory, verifies it against the manifest, serves it and asserts vector
  search is on. It reads the bootstrap URL **out of `cmd/server/bootstrap.go`**
  rather than restating it, so a drifting constant breaks the check instead of
  users. Both F03 and F01 were invisible to every producer-side test by
  construction.

**Where the review was wrong.** It computed `14 217 / 220 s ≈ 64.6` and concluded
the documented 62.9 clause/s was wrong. The inconsistency was real but inverted:
62.9 is emitted by the embedder itself (`done_n / elapsed`); the rounded
"3 min 40" was the imprecise term. `14 217 / 62.9 = 226 s = 3 min 46`, corrected
in the runbook.

**Where it treated a decision as an oversight.** It proposed replacing "counters
must not decrease" with a per-spec comparison. `cmd/dbcount` documents the
opposite choice — *"deterministic, fail-closed — no 'significantly smaller'
heuristic"* — with `ALLOW_CORPUS_SHRINK` / `ALLOW_API_SHRINK` as the deliberate
override. Given that this pipeline's failure mode is the clever guard that never
fires, the blunt rule plus an explicit override is the safer shape. The runbook
omitted the override; that was the real defect and is fixed.

**Still open.** The 56 holes are detected, planned for, and gated on — but not yet
repaired: that needs a fetch, which is the operator's call.

### F32 — Five guards that reported something other than what they observed
`CONFIRMED` · `FIXED`

Launching the first real end-to-end run broke five times before the first
document converted. Not five unrelated bugs: **one bug, five instances.** Each
guard reported a conclusion it had not established, and each conclusion was
plausible enough to send the reader somewhere else.

| Guard | What it said | What had happened |
|---|---|---|
| LibreOffice on PATH | `soffice` not found | installed since bootstrap; `toolchain-env.sh` never exported it |
| `flock -n 9` | "another run in progress" | Git Bash ships no flock; the command failed 127 |
| `anchorcheck` in `seed` | "did not run — unverified" | it ran and found all 56 holes |
| `df -BG --output=avail` | "only **G** free (< 5G), abort" | the measurement was empty — the number is missing from its own message |
| `soffice --convert-to` | exit 0 | wrote nothing at all |

**The one that cost the most.** `soffice` is a native Windows binary and this
shell hands it POSIX paths. Given `-env:UserInstallation="file:///tmp/…"` it
exits **0** and produces no file. `_soffice_html` then correctly finds no HTML
and falls through four recovery attempts at `CONV_TIMEOUT=900` each — so four
workers spent ten minutes, and zero conversions, on documents that convert in
seconds. Proven rather than guessed:

```
POSIX paths   -> rc=0, no HTML
cygpath -m    -> rc=0, 721 KB of HTML
```

This is the CUDA trap from `docs/local-pipeline.md` §Windows, trap 3, on a
different binary. The lesson generalises: it applies to **every native binary
this pipeline drives**, not only the one where it was first met. `_conv_native`
and `_conv_url` now translate paths, and both are `export -f`'d — they run inside
the `xargs` worker subshell, and exporting the caller without its helpers gave
`_conv_native: command not found` once per conversion.

**Fixed by making each guard distinguish its two cases.** "Cannot measure" is now
different from "threshold violated"; "tool absent" from "lock held"; "check found
a problem" from "check could not run". The lock is a portable `mkdir` with stale-
PID reclamation, so it behaves identically on every platform instead of silently
degrading on one.

**A regression I introduced and caught by re-reading.** An early `perl -0pi`
substitution wrote `SERIES_FILTER=""` in place of `SERIES_FILTER="$2"` — `$2` is
a perl capture group, empty at that point. `--series` would have been silently
ignored, sending non-repair runs back to full enumeration. Found by reading the
file rather than by any test, which is its own finding: the arg parser has no
coverage.

---

## Fixed by construction

### F05 — No toolchain, and not enough disk
`CONFIRMED` · `FIXED`

The machine had no Go, no Rust, no C compiler, no `cmake`, no LibreOffice, no
Docker and no WSL; Python was a Microsoft Store stub; 43 GB were free on a
1023 GB volume.

**Resolved.** ~44 GB reclaimed, then a fully portable, elevation-free toolchain
under `.local/toolchain/`: Go 1.26.3, winlibs mingw-w64 **UCRT** (see below),
Rust 1.98 with `RUSTUP_HOME`/`CARGO_HOME` kept out of the user profile,
LibreOffice extracted via `msiexec /a`, DuckDB 1.5.3, ONNX Runtime 1.20.1 GPU,
and the CUDA 12.6 + cuDNN 9.9 redistributables. `scripts/local/toolchain-env.sh`
resolves it all and is sourced by every entry point.

Two non-obvious findings came out of this and are worth keeping:

- **The C runtime must be UCRT, not msvcrt.** w64devkit builds against
  `msvcrt.dll`; `duckdb.dll` is built against UCRT. A binary mixing them has two
  heaps — DuckDB allocates with `ucrtbase`, cgo frees with `msvcrt` — and the
  process dies with `0xC0000374 STATUS_HEAP_CORRUPTION` on the first query. The
  build is green; the crash is at runtime.
- **The prebuilt DuckDB static libraries are MSVC-built** and export MSVC STL and
  UCRT symbols that no mingw can link. The C API, being pure C, links fine — hence
  `-tags duckdb_use_lib` on Windows.

### F13 — Windows cannot provision the inference path
`CONFIRMED` · `PARTIAL` in scope · `FIXED` for the local goal

`internal/bootstrap/models.go:97` covers only linux/amd64, linux/arm64 and
darwin/arm64, and no `mcp-3gpp-onnx_windows_amd64` asset is published — so
`bootstrap --semantic` cannot work on Windows. That part stands.

The audit's conclusion — that Windows is therefore unusable and WSL2 is the only
path — does **not**. `duckdb-go-bindings` does publish a `windows-amd64`
artefact; the whole Go read-side compiles and its full test suite passes here;
and the GPU embedder runs on Windows once ORT is switched to `load-dynamic`.
What is missing is only the *provisioning helper*, not the capability.

**Done.** ONNX Runtime and the CUDA runtime are provisioned by the local
toolchain instead. `rust/embedder` now uses `load-dynamic` with
`default-features = false` — required, because ort's defaults include
`download-binaries` and cargo features are additive, so asking for
`load-dynamic` alone still made `ort-sys` panic with *"downloaded binaries not
available for target x86_64-pc-windows-gnu"*. This also removes the second
bundled ONNX Runtime, which is the first half of F14's remedy.

---

## Accepted

### F33 — More parallelism made the corpus build twice as slow
`SUPERSEDED 2026-08-26` · the default is now **jobs=6**

> **Reversed by a controlled measurement.** The numbers below were sampled from
> windows of a live run, with downloads, other steps and an unknown machine load
> mixed in. An A/B/B/A over the same 28 documents on an idle machine, driving the
> real `convert.sh`, measured **225 s at `--jobs 4` against 178 s at `--jobs 6`** —
> 6 is 21 % *faster*, and the two 6-runs agreed to the second. `scripts/corpus.sh`
> now defaults to 6; see `docs/local-pipeline.md`. What survives F33 is not its
> conclusion but its lesson about method: **a throughput figure taken mid-run is
> not a measurement.**

CPU sat at 73 % with 17 GB RAM free during conversion, so `--jobs` went from 4 to
6 to use the headroom. Measured over 10–15 minute windows:

| jobs | CPU | conversions/min |
|---|---|---|
| 4 | 73 % | **4.9** |
| 6 | 96–99 % | **2.4** |

Six LibreOffice instances on 8 logical threads — an EPYC 7543P partition, so
likely 4 physical cores — contend for the same execution units: each conversion
slows by more than the extra concurrency returns. **The visible headroom was not
usable by this workload.** Reverted to 4.

Also recorded, because it invalidated an earlier claim: throughput cannot be
extrapolated from a short window. The 4.9/min sample came from the small series
21–22 specs at the head of the work-list; when the run reached 23.501 and 23.502
(16 MB archives, 700+ pages) the rate fell to 0.6/min, then returned to 4–5/min
after them. The size distribution is heavy-tailed, so any ETA from a single
window is wrong in one direction or the other.

### F06 — No workflow republishes the DB to the Release
`CONFIRMED` · `ACCEPT`

Deliberate. `corpus-sync.yml:13` states it: *"WHAT it PUBLISHES: NOTHING…​ The
ability to clobber the canonical DB on the `latest` release was REMOVED (2026-06
CI cleanup)"*, and `git show 6275fe9` shows the removed `gh release upload`. A
second workflow able to overwrite the served DB is a footgun; the canonical
channel is the private GHCR image. Neutral for the local rebuild — the June
Release asset remains a valid, anonymously reachable starting point, and the
local pipeline uses it exactly that way.

Residue, no urgency: `docs/data-pipeline.md:15-20` still describes the removed
"Corpus Sync (C4)" publish.

### F07 — GHCR vector packages are private
`CONFIRMED` · `ACCEPT`

Also deliberate, and non-negotiable: the blobs contain verbatim 3GPP text under
3GPP/ETSI copyright. `corpus-data-image.yml:19-23` explains that the push uses a
PAT and the Dockerfile carries no `org.opencontainers.image.source` label so the
package is *born private*, and `:566-580` self-heals and fails loudly if it ever
turns public.

Consequence accepted for the local goal: the ~2.45 M vectors already computed are
unreachable without a PAT, so the corpus is re-embedded locally. The measured
2.74× content dedup is what makes that affordable.

Residue: `serve --vec-ghcr` (`internal/bootstrap/ghcr_vec.go`) is dead code whose
token handshake has been broken since 2026-06-12 — remove or repair it rather
than leaving a path that cannot work.

---

## Obsolete

### F19 — The bake cron ran before the build it consumed
`OBSOLETE` · `NONE`

Was true: `corpus-data-image.yml` fired at `37 3 * * *` while `corpus-matrix.yml`
fired at `17 4 * * *`, so the bake always consumed the previous day's corpus.
Both schedules are now commented out by commit `071aa35`; the pipeline is local
and directly chained. If those crons are ever re-armed, the bake must be moved
*after* the build.

---

## Recorded with an exact remedy (`FIX_LATER`)

These are real and each entry carries the precise change. None is on the critical
path of a local build; most concern the retired CI, which is dormant.

| # | Finding | Verdict | Why deferred |
|---|---|---|---|
| **F02** | The delta anchor is never republished, so CI's delta is measured against a photograph | `PARTIAL` | True for CI only. The **local** anchor is rewritten by every merge and regenerated when missing, so the local loop closes. Remedy: read the anchor from the GHCR corpus image where it is actually published, rather than from a Release asset nobody uploads. |
| **F04** | The sparse layer is published to GHCR but no consumer folds it in | `CONFIRMED` | `grep -c 3gpp-sparse corpus-data-image.yml` = 0. 104 failed campaigns would have changed nothing even if they had succeeded. The overlay capability exists (`rust/store/src/lib.rs:271` `overlay_sparse`); only the CI wiring is missing. Deliberately out of scope: spending GPU on an index with no consumer is the mistake, not the fix. |
| **F08** | `cmd/split` drops `clause_sparse`, `schema_meta`, `ingest_log` | `CONFIRMED` | 13 tables listed against 16 in the schema, and no test. Note the trap: `clause_sparse` has no `release` column, so simply adding the name would copy it whole into every shard and then violate the primary key. |
| **F09** | `changes` has no producer — `get_changelog` always returns nothing | `CONFIRMED` | `rust/parse/src/lib.rs:344` parses the Change History annex **in order to discard it** (`cur = None` makes `flush()` drop the buffer). The June snapshot still holds 61 321 rows written by the retired Go pipeline, so this is a **regression**, and a full rebuild would empty the table. Needs a real annex-table parser. |
| **F10** | Orphaned `clause_sparse` rows at incremental merge | `CONFIRMED` | `delete_spec_release` purges `clauses`/`spec_versions`/`specs` but not `clause_sparse`, and `max_chunk_id` reads only `clauses` — so the recomputed offset can drop below orphan ids and violate the PK. Latent only because sparse never reached production; it would fire on the second `merge --base`. Remedy is three lines and is recorded in full. |
| **F11** | `fold_shard` copies 8 of the 14 content tables | `CONFIRMED` | Loses `li_events`, `li_event_fields`, `li_nf_clauses`, `asn1_types`, `api_operations`, `api_schemas`. The local pipeline compensates by running `enrich` *after* `merge`, explicitly and non-optionally — do not skip that step. |
| **F12** | Two incompatible embed identities (fp32 `:latest` vs fp16 `:latest-fp16`) | `CONFIRMED` | The fp32 channel is published and never consumed. Local choice: **fp32**, once, recorded in ADR 0003 — precision is part of the EmbedIdentity, so switching later costs a full re-embed. |
| **F14** | Two distinct ORT/BGE-M3 implementations, contradicting `CLAUDE.md` | `CONFIRMED` | `rust/embed-core/src/ort_backend.rs:2` admits it: *"A single-query, serve-side mirror of rust/embedder/src/model.rs"*, with `MAX_TOKENS`, `DENSE_OUTPUT` and the dimension duplicated and kept in lockstep by hand. Half-remedied here: both now use `load-dynamic`, so a single ONNX Runtime serves the process. Constants still need a single home. |
| **F15** | `mean_pool` windowing (#208) is still not ported to Rust | `RESOLVED` | Ported. The Rust embedder windows a clause only when it does not fit in `max_tokens` whole, then re-splits any window that still reaches the cap — measured, 300-word windows hit it 10.8% of the time on this corpus, so the word split alone would have reconducted the defect. `truncated_windows` reaches 0. The word split itself is pinned across Go and Rust by a synthetic fixture (`internal/embed/testdata/window_parity.json`); it is synthetic because DATA_NOTICE.md forbids verbatim clause text on a public channel. Identity moved `61ba446c0814` -> `6bf1f9a47710`, which is the full re-embed this entry warned about. |
| **F16** | The authoritative architecture plans are absent | `CONFIRMED` | `.claude/plans/` does not exist and is gitignored (`.gitignore:164`), yet `CLAUDE.md:27` cites it as the source of truth for the write-side migration. Partly answered here: ADR 0003 records the local pipeline's decisions in a versioned file. ADR 0001 for the Rust write-side migration is still owed. |
| **F17** | `CLAUDE.md` §9 and §4 describe a tree and a schema that no longer exist | `PARTIAL` | §9 lists five binaries that were deleted and omits seven that exist; §4 describes 6 tables against 16 in `internal/store/schema.sql`. The audit's "Go 1.23 vs go.mod" point is **wrong** — they agree. Mitigated: `cmd/CLAUDE.md` and `internal/CLAUDE.md` already describe the real state; it is the root document that drifted. |
| **F18** | Contract level 3 emits a `--require-etsi` flag that exists nowhere | `CONFIRMED` | Selecting `dense+sparse+etsi` makes both gate binaries reject an unknown flag. Remedy: make the level fail with an explanatory message until the flag exists, instead of emitting a phantom one. |
| **F20** | `corpus-matrix` path filters point at deleted directories | `PARTIAL` | Five dead paths, and the real write-side (`rust/store`, `rust/embed-core`) is absent from the list. Inert today — the whole `push:` trigger is commented out. Must be fixed before any re-activation. |
| **F21** | Dead or misleading GPU tuning variables | `CONFIRMED` | `SPARSE_BATCH` is computed with an anti-OOM comment and never passed to the binary (the effective batch is the default 64, 4× the stated value); `ORT_EP` is read by no Rust code; the `batch` workflow input is documented as the GPU batch while it only affects the CPU fallback. Kaggle-only, and Kaggle is retired. |
| **F22** | The Kaggle poll expires before the kernels' own budget | `CONFIRMED` | Dense: poll ≈ 3 h 21 against a 10 h 50 kernel budget. A run that used its budget was classified `quota` and pointlessly failed over to a second account. Explains part of the 104 failures; moot now that the campaigns are retired. |
| **F23** | Catalogue defaults are wrong: `doc_type` forced to `TS`, acronym `domain` always empty | `CONFIRMED` | `rust/parse/src/lib.rs:178` hard-codes `doc_type: "TS"` — the filename genuinely does not carry TS/TR, so the honest fix is an empty placeholder that `update_spec_meta` fills, not a guess. This makes `enrich` **mandatory**, which the local pipeline enforces. |
| **F24** | The documented intent router does not exist as a router | `CONFIRMED` | `search.Classify` has one non-test caller: a display field in the `search_spec` response. `Engine.Search` runs every capable arm and fuses with RRF k=60, gated only by `mode`. The behaviour is fine — arguably better — but `CLAUDE.md` §3's diagram is fiction. Documentation fix. |
| **F25** | Volumetry figures contradict each other by 4× to 20× | `CONFIRMED` | "~10 M", "~2.45 M" and "~2.85 M" clauses for the same corpus; 1.7 GB vs 8 GB vs 33 GB for the same DB; 2.3 / 5 / 6.5 GB for the same models. Superseded by a single measured source: **2 855 712 clauses, 6.49 GB decompressed**, dated and reproducible (`docs/local-pipeline.md` §2). |

---

## Measurements taken during the resumption

These replace the contradictory figures of F25. All are from the published
snapshot (`3gpp.duckdb`, 2026-06-05), queried directly.

| Quantity | Value |
|---|---:|
| Clauses | 2 855 712 |
| Embeddable clauses (`length(trim(text)) > 0`) | 2 282 337 |
| **Distinct texts** (`heading + "\n" + text`) | **833 924** |
| **Content dedup factor** | **2.74×** |
| Clauses duplicated **between releases** | **79.8 %** |
| Dense vectors, no dedup / deduped | 9.35 GB / 3.42 GB |
| Clause text, no dedup / deduped | 3.07 GB / 1.59 GB |
| Decompressed lexical DB | 6.49 GB |
| Specs / spec_versions / changes rows | 3 537 / 20 084 / 61 321 |
| GPU throughput, BGE-M3 fp32, RTX A4500 | ~7.5 clause/s |
