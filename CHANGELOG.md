# Changelog

All notable changes to this project are documented here.

## [Unreleased] — the two arms enumerate on the same clock (2026-09-12)

### Fixed

- **`enrich` no longer replays because 3gpp.org re-nonced a page.** `discover`
  re-downloads `status-report.htm` on a 6 h TTL, and `enrich` declared that file
  as an input — but www.3gpp.org serves it through Joomla behind Cloudflare and
  every response carries a fresh CSP nonce (22 of them), CSRF token, GDPR session
  id and re-obfuscated e-mail addresses. The file could not be equal to itself
  twice, so the whole 3GPP write side replayed on a clock: `enrich` →
  `paragraphs` → `sparse` → `compact` → `index` → `validate` → `smoke` →
  `publish`, **~18 min of corpus work and a re-pushed image** — measured on
  2026-09-12, which published the identical digest `74e60bb9…` twice in fourteen
  hours. The ETSI arm, which has no such input, sat still: one pipeline, two
  behaviours, paired step names hiding it.

  `discover --emit-catalog` now publishes `catalog-specs.tsv` — the four fields
  the overlay actually writes (spec_id, doc_type, working_group, title), one line
  per spec, sorted — and `ingest-catalog` takes `--catalog` instead of
  `--status-report`. Measured on two real downloads eight hours apart: 5 180 736
  vs 5 180 734 bytes of HTML, **365 860 bytes of projection over 3 695 specs,
  byte-identical**. Neutralising the volatile patterns one by one was the other
  option and it is a list upstream can extend without telling us; projecting onto
  what is READ cannot be.

- **`discover-etsi` can see what ETSI publishes.** Its only determinant was the
  scope, and a scope nobody types does not move — so once the ETSI work list
  existed, the step's fingerprint could never change again and a new version of,
  say, TS 103 221-1 was unreachable for ever. Nothing failed; the corpus was just
  quietly out of date in the half nobody watched. Both enumerations now bucket the
  age of their last visit through one helper (`internal/goal/freshness.go`), and
  the stamp is deliberately NOT an output — one that moved on every visit would
  replay `fetch` and `ingest` on the clock, the same cascade one edge lower.

  `discover`'s own bucket was the age of its HTTP cache and only worked BY
  ACCIDENT: `WriteAtomic` leaves an unchanged file untouched, so the bucket would
  have stayed expired and re-run on every invocation. It was the nonce that kept
  it honest — fixing the cascade would have broken the clock.

### Added

- **`TestBothArmsEnumerateOnTheSameClock`** reads the determinant, not the step
  name. `arm_parity_test.go` has paired the two arms by name since #326 and every
  gate stayed green while they behaved differently underneath those names; this is
  the invariant one level down. With `TestTheVisitStampIsNotDeclaredAsAnOutput`
  and the `catalog_projection_tests` module in `rust/discover`, which asserts the
  property rather than enumerating the patterns: a report differing only in page
  chrome must project identically, and a changed catalogue must still move.

## [Unreleased] — the changelog gets a writer (2026-09-10)

### Added

- **`ingest-crs`: the `changes` table has a writer again, and its source is the
  authority rather than a transcription of it.** The table had had none since the
  Go HTML-ingest write side was deleted (Phase 11b, `c635038`) and the Rust ingest
  never reimplemented the change-history parser. Measured on the corpus published
  2026-09-09: 61 321 rows over 3 452 specs, only **311 specs** naming anything
  citable, 3 026 rows summarised `"Date"` — the change-history table's column
  header, read positionally as a body row. PR #311 stopped `get_changelog` serving
  that header; it could not give the table a source.

  The source is the published **3GPP Change Request database**
  (`/ftp/Information/Databases/Change_Request/CRDB_<date>.zip`, 57 MB, 596 696
  records). Parsing the change-history table printed in each spec was the obvious
  repair and it is the wrong one, measured: the converted tree holds 1 410
  documents covering **1 038 of 3 568 specs**, and `ingest --resume` skips a
  (spec, version) the corpus already holds whatever the parser version says — so
  that repair would have covered nothing at all without re-fetching all 20 163
  versions from 3gpp.org. The printed table is a rendering of this database.

  Result: **256 471 change requests over 2 169 specs**, up from 311 citable specs
  — `get_changelog(23.501)` answers **3 064** records with meeting, TDoc,
  category and version transition, where it used to answer `count 1,
  summary "Date"`.

- `scripts/fetch-crdb.sh` acquires the newest export, reading the date from the
  directory listing rather than pinning a URL that 404s the day 3GPP re-exports.
  `enrich` runs it when the file is absent, like the OpenAPI and LI overlays.

- `changes_source` in `schema_meta` names the export the corpus was built from
  (`CRDB_20260715`), so `get_changelog`'s note can tell "no CR was raised" apart
  from "the export predates it" instead of describing its staleness vaguely.

### Fixed

- **A CR database is a record of PROPOSALS, and writing it whole would have made
  the corpus wrong by containing too much.** Tallied over the 596 696 records:
  313 039 never reached TSG level and carry the literal placeholder `..` as their
  target version; with `reissued`, `revised`, `withdrawn`, `rejected`,
  `postponed`, `not pursued`, `noted`, `merged` and `endorsed`, **331 489 rows —
  55.6%** — describe changes that did not happen. Only CRs the database records as
  `approved` *and* naming a real target version are written. Both halves are
  required and neither is redundant: 4 031 approved rows have no version allocated
  yet, and 1 454 rows name a version the status contradicts.

- **Two nullable columns had never been read as nullable, and one NULL aborted the
  whole call.** `cr_revision` and `clauses` are nullable in the schema, but
  `store.GetChangelog` scanned them into `int` and `[]any`. It went unnoticed for
  as long as the table had no writer — the fossil happened to hold a value in
  every row — and surfaced as `converting NULL to int is unsupported` and
  `storing driver.Value type <nil>`, returning an error for the spec instead of
  its changelog. Same shape as the `array_to_string`/NULL defect that broke
  `search_api` for 84% of the corpus: a producer that only ever writes non-NULL
  cannot find it, and only a real corpus can. Found by driving the real server
  over JSON-RPC, not by a fixture.

### Changed

- `get_changelog`'s note no longer says the table has no writer, because it now
  has one. It states the two silences that remain and are real: the database
  records change requests, so an editorial republication raises none, and anything
  approved after the export is absent rather than empty.

- **`replace_changes` lives in `rust/store/src/changes.rs`, not in `lib.rs`.**
  `merge` declares `rust/store/src/lib.rs`, so a changelog writer there would have
  replayed merge (34m15), paragraphs (20m32), compact (19m22) and index (8m06) and
  re-pushed 42 GB on every edit, to rewrite a table none of them read — the cost
  `vectors.rs` was split out to stop paying. `enrich` declares the new file, and
  `TestEnrichDeclaresTheChangelogWriter` plus
  `TestChangelogWriterHasExactlyOneCaller` hold the narrow declaration honest:
  narrow is the dangerous direction, so the tests count the callers rather than
  trusting the comment.

- `replace_changes` REPLACES rather than appends, which makes the step idempotent
  on its output — the lesson `ingest-li` (#286) and `ingest-etsi` (#316) each paid
  for separately. Proven by running it twice: identical counts. It is
  cite-or-silent like every other overlay, skipping 8 736 records for specs this
  corpus holds no text for.


## [Unreleased] — the corpus stops growing on its own (2026-09-09)

Published and verified: `ghcr.io/kodflow/3gpp-mcp@sha256:0349248311a48073f8eb4b2252914e326b67f9d27b9434926cb13751cb2e3ec6`
(digest re-read from GHCR, `make prove` → `PROVE OK`).

Measured on the served corpora: 3GPP **2 751 918 clauses** / 20 163 versions,
ETSI **3 168 482 clauses** / 11 822 versions; glossary 14 126 + 28 154.

### Fixed

- **The ETSI corpus gained 566 clauses on every build, from a converted tree
  that never changed** (3 175 274 → 3 175 840 → 3 176 406 across three builds).
  The resume check read each file with `std::fs::read_to_string` — strict UTF-8,
  `Err` on anything else — while the ingest read the SAME file through
  `html_bytes::read_html`, which falls back to windows-1252. A deliverable that
  is not valid UTF-8 could therefore be INGESTED but never RECOGNISED as already
  ingested. Exactly two files of 11 822 are not UTF-8, and both had been written
  **fifteen times**: 1 155 rows for 77 clauses, 7 335 for 489 — 77 + 489 = 566.
  `ingest: ETSI → 21 spec(s), 566 clause(s)` is now `19 spec(s), 0 clause(s)`.
- **`get_changelog` served the change-history table's HEADER row as a change
  record.** TS 23.501 answered `count 1, summary "Date"`. 3 352 such rows exist
  (3 026 summarised "Date"), and 3 163 of the 3 452 specs with any record had
  nothing else. `model.Change.Citable` drops records that name no CR and no
  version transition — deliberately the weakest rule that catches the header,
  because 1 047 rows are MCC editorial updates with a real transition and no CR.
- **`get_changelog` answered a bare 0 for the 3GPP half.** The `changes` table
  has had no writer since the Go HTML-ingest write-side was deleted (Phase 11b),
  so it covers 311 of 3 568 specs; 3 326 of the specs that do have records hold
  a version newer than their newest recorded change. Both silences now say what
  they are, and neither half borrows the other's reason.
- **Two over-declared provenances**, each measured. A shell variable used only by
  the ETSI fetch sat in the shared prelude, so `ingest-etsi` inherited it and
  replayed the whole ETSI arm plus a 42 GB image re-push. And `enrich` named Go
  package DIRECTORIES, so adding one `_test.go` replayed a data step.

### Added

- **`validate --require-no-reingest`** — the gate that was missing.
  `--require-worklist` asks whether anything is MISSING; nothing was, so it
  stayed green for fifteen builds. A corpus can be wrong by holding too MUCH. It
  reports any `(spec_id, release, version)` whose EVERY distinct clause row is
  stored more than once. Release is part of the identity: without it the
  predicate accused `30.531 v1.62.0` of nine copies, when it is legitimately
  catalogued under nine releases. Declared by BOTH gate binaries, because the
  image's entrypoint runs the same flag list.
- **`cmd/repair-reingest`** (dry run by default). ETSI 3 176 406 → 3 168 482,
  3GPP 2 752 688 → 2 751 918. Safe because `bodies`/`paragraphs` are
  deduplicated by `(heading, text)`: fifteen copies share one set of bodies, so
  the damage is confined to `clause_occ`/`clause_sparse`. It keeps the first
  block of chunk_ids rather than one row per distinct clause — a document may
  legitimately repeat a clause — and refuses a group that is not an exact
  multiple.
- **`fetch-etsi` records WHICH deliverables it could not convert**, not just how
  many. It counted failures into empty marker files, so "failed=4" was a number
  and not a fact. `validate --require-worklist` now reconciles work list, corpus
  and register: 11 826 versions, 4 excused, and the four are named.
- **`TestNoStepCountsTestFilesByAccident`** — no pipeline step may count test
  files in its fingerprint without excluding them or recording why, with the
  measured cost of fixing it. It immediately found two steps a hand enumeration
  had missed.
- The divergence between the two glossary plausibility rules is **recorded and
  measured in both directions**: adopting ETSI's rule on the 3GPP arm destroys
  4 270 of 14 126 correct rows (30.2 %), and the reverse destroys 1 377 of
  28 154 (4.9 %). Neither rule is the better one; they guard different sources.

### Changed

- `mcp-go` 0.58.0 → **1.0.0**, verified by the full suite plus `make prove`
  before merging rather than on the strength of the commit gate.

## [Unreleased] — the corpus is built on one machine (2026-08-26)

Indexing moved off Kaggle GPU + five GitHub workflows and onto a single
machine, and ran to the end for the first time. Runbook:
`docs/local-pipeline.md`; rationale: `docs/adr/0003-local-goal-pipeline.md`.

Measured on the finished corpus: **2 752 688 clauses**, 20 163 spec versions,
8 562 API operations, 405 LI events; data contract 5/5 (FTS present, HNSW
frozen, `null_at_floor=0`); `anchorcheck` **`missing_content=0`**, with no
absence accepted to get there.

### Added

- **The corpus is stored content-addressed, at paragraph granularity**
  (`docs/adr/0004`). Each distinct paragraph is stored once, each distinct
  `(heading, paragraph sequence)` body once, and one occurrence row per real
  `(spec, release, version, clause)`. **30.25 GB → 12.36 GB**, vectors
  2 752 688 → 821 146, `smoke` 45 s → 4 s. Splitting on `\n\n` and re-joining
  reproduces the original for **2 752 688 / 2 752 688** clauses, and the
  migration asserts it rather than assuming it.
- Lexical retrieval now ranks deduplicated text instead of versions. The 12-hit
  window for `CHECK_IMEI` used to be one clause repeated across twelve releases,
  with the real answer never in it; it is now 8 distinct clauses with TS 29.273
  at rank 3. **nDCG@10 0.014 → 0.072.**
- `trace_clause`: paragraph-level provenance. `get_changelog` says a CR touched a
  clause; this says what the clause SAYS differently — which releases carry each
  statement, when it was introduced, whether it is gone from the newest one. It
  reports plainly when a corpus cannot answer that (ETSI is served alongside and
  is not converted) instead of guessing.
- `cmd/freeze-hnsw` (Go): the vector index is now built by the side that knows
  where the vectors are. `rust/store`'s version names `clauses`, which on a
  converted corpus is a view over 2 752 688 references to 897 556 vectors — and
  DuckDB will not index a view at all. `internal/store.hnswTarget` already
  resolved both shapes and was tested on both, so this is a thin front for it.
- `migrate-paragraphs --restore`, the exact inverse of `--drop-clauses`, and
  `merge` runs it before folding. `merge --base` compact-copies a corpus **table
  by table**, so a converted corpus's `clauses` VIEW is left behind and
  `schema.sql` recreates it empty — the fold would then write the delta into an
  empty table while `clause_occ` still held every occurrence, with
  `max_chunk_id()` reading 0 and handing the shard colliding ids. Restoring the
  shape the write side has always known costs one grouped reconstruction (1 m 47
  for 2.87 GB) and keeps ADR 0004's layout out of the write side entirely.
  Proven on a real 46 440-occurrence slice: convert → restore → fold a bucket →
  convert again loses and invents **0 rows**.
- The two external overlays acquire themselves (`scripts/fetch-5g-apis.sh` now
  resolves through the 3GPP archive endpoint; `scripts/fetch-li-asn.sh` is new),
  so `enrich` no longer depends on files someone fetched by hand.
- `scripts/local/build-image.sh` builds both images from a locally produced
  corpus, ETSI included. The `full` image itself has never been built — no
  container runtime here — but the script was dry-run end to end against a stub
  `docker`, and CI's `image-smoke` builds the `light` target on every push.

- `cmd/goal` + `internal/goal`: a 20-step resumable state machine that owns the
  whole build — toolchain, build, seed, discover, fetch, ingest, merge, embed,
  enrich, paragraphs, index, validate, smoke, plus the four ETSI steps. Every step is
  content-addressed, so `goal plan` shows the differential and
  `goal run --only <steps>` executes a subset **without** skipping its
  preconditions.
- The ETSI corpus, built alongside 3GPP and deliberately kept **split**: 14
  Lawful-Interception deliverables in their own `etsi.duckdb`, same embedder,
  same index. `server --etsi-db` federates the two at serve time; `get_spec`
  and `list_releases` route `ETSI …` ids there and `list_specs` unions both.
- `smoke`: starts the shipped binary over stdio, calls real tools, and asserts
  vector search was not silently disabled at startup — the failure that shipped
  for months and that no unit test can see.
- `cmd/anchorcheck` and `contracts/accepted-absences.txt`: the delta anchor may
  not claim text the corpus does not hold. Keys that genuinely cannot be
  acquired are recorded **with a reason**, never to silence a red check.
- `scripts/fetch-li-asn.sh`, which acquires the TS 33.128 ASN.1 payload
  registry — it ships in a zip inside the zip of the spec — so `li_events` and
  `asn1_types` stop being empty.
- `.mcp.json`, because the finished corpus was being served to nobody.
- `AMBIGUOUS` verdict in `li-audit`: several specs naming an operation equally
  well is a draw, and a draw is not a hallucination.
- The `scripts/*_test.sh` suites now run inside the `test` step. They were
  written, green, and executed by no runner.

### Fixed

- **The in-image data guard was weaker than both.** `mcp-3gpp check-data`, the
  `RUN` that fails a `full` image build when the inherited data layer is
  incomplete, compared `hnsw_state` to `"frozen"` and stopped there — it did not
  even check the index existed. A corpus carrying the word "frozen" over nothing
  passed it. It now runs `LoadVSS` too and reports `hnsw_usable`, so the last
  gate before a corpus starts answering queries asks the same question as the
  thing that will answer them.
- `scripts/local/build-image.sh` built the **wrong image**: `light` is the last
  stage in the Dockerfile by design, so a build without `--target full` silently
  produced the lexical-only image, ignored `DATA_IMAGE`, and tagged it as the
  full one. It now passes `--target full` and feeds the guard the real contract
  from `scripts/data-contract.sh` instead of the Dockerfile's two-flag default.
- Two more in the same script, found by running it against a stub `docker` that
  prints its argv — the only way a quoting bug shows itself short of a real
  build. `${CONTRACT:+--build-arg "DATA_CONTRACT_FLAGS=$CONTRACT"}` looks quoted
  and is not: the expansion is word-split afterwards, so docker received
  `--build-arg DATA_CONTRACT_FLAGS=--require-fts` followed by two loose
  positional arguments. And `io.kodflow.3gpp.duckdb.rows` was being filled from
  `dbcount | head -1`, i.e. `spec_versions=20163` — a catalogue size labelled as
  a row count, telling an operator the wrong thing about the image they pulled.
- **`validate --require-hnsw` asked a weaker question than the server.** It
  checked `hnsw_state` and `HNSWIndexPresent`, which resolves the index name
  through `hnswTarget()`; the server asks `store.LoadVSS`, which additionally
  compares `embedding_count` against the vectors actually present. Two checks of
  the same property, and the gate read green on a corpus the server refused. It
  now runs `LoadVSS` itself and reports `serve_usable`, so the contract gate
  fails the build the same way the server would — verified against the exact
  defect above: "the server would REFUSE this index: embedding count drift".
- **The server exact-scanned every vector on the shipped corpus, and said
  nothing.** `store.LoadVSS` — the serve-time gate that decides whether the
  frozen index may be trusted — looked for `clauses_hnsw` by name, while the
  index had been moved to `bodies_hnsw` along with the vectors. It reported
  "hnsw index absent" over a corpus carrying a perfectly good index, fell back
  to an O(N) exact scan, and returned correct answers slowly with no error
  anywhere. Behind it sat the same miss again: `schema_meta.embedding_count`
  still held the pre-conversion 2 207 218 against the 821 146 vectors actually
  present, so even with the right name the gate failed on "embedding count
  drift". The guard now follows `hnswTarget()`, and the conversion re-stamps
  both markers — it is what changed the vector population, so it is what has to
  say so. Found by running the real server over the real corpus rather than by a
  test, which is why there are now two.
- **Every write-side tool failed at bootstrap on a converted corpus.**
  `schema.sql` carries three `CREATE INDEX ... ON clauses`, and DuckDB answers
  those against a view with "can only create an index on a base table"; schema
  application is all-or-nothing on both sides, so `merge`, `embed-io`, the three
  `enrich` ingesters and `freeze-hnsw` all died before reading a row. Go's
  `migrate()` had a second one (`ALTER TABLE clauses ADD COLUMN ...` →
  "Can only modify view with ALTER VIEW statement"). The index statements are now
  bracketed by markers in `schema.sql` and both readers strip them when the name
  resolves to a view — markers in the shared file so the two languages cannot
  drift. Nothing was silently wrong: the tools refused to open rather than
  corrupting anything. The test that was supposed to catch this applied a
  two-column stand-in for the schema instead of the schema, and passed
  throughout; it now applies the real one and asserts the raw form still fails.

### Removed

- The eleven corpus/Kaggle workflows the local pipeline replaced. `ci.yml` and
  `post-commit.yml` stay — they gate this repository's own commits.

### Changed

- **Merge before embed** (the CI did the opposite). `ingest` rebases `chunk_id`
  per shard, so a ledger shared across shards drops clauses by collision. After
  the merge the ids are unique, which makes one ledger both safe and a
  corpus-wide content-dedup — a measured 2.74× reduction in GPU work.
- The corpus cron workflows are disabled (`workflow_dispatch` kept): they were
  failing ~28 times a day against infrastructure that no longer indexes.
- The server refuses to start when the embed identity disagrees with the corpus
  stamp instead of degrading to lexical in silence
  (`--allow-lexical-fallback` to assume it explicitly).

### Fixed

Most of these reported SUCCESS while doing nothing. The recurring shape is a
step that writes `skip` on stderr and returns 0.

- A corpus discarded over its **encoding**: LibreOffice keeps the Word source's
  windows-1252, `read_to_string` refused it, and six specs were downloaded,
  converted and thrown away on three consecutive runs while the series reported
  SUCCESS.
- A **series list that did not cover what the work list reached** — 400 KB of
  converted HTML per release, never read, every step green.
- A **stale binary behind a green build**: `skipDirs["bin"]` pruned
  `rust/store/src/bin/`, hiding the sources of `merge`, `embed-io`, `overlay`
  and `freeze-hnsw` from the fingerprint.
- A **merge that cloned dead space** instead of reclaiming it, so the corpus
  grew 38 → 135 GB across runs and each run started from the previous file.
- **`403` treated as transient** on www.3gpp.org, where 403 *is* "not found".
- A **`.wal` left behind** by the publishing rename, leaving a sound corpus
  that could not be opened (`Conflict on tuple deletion!`).
- The HNSW build's ceiling reported as **RAM when it is temp disk**
  (`max_temp_directory_size` defaults to 90 % of free space).
- A **Windows Python's CRLF** riding into every filename and URL of the OpenAPI
  fetch: `http=000` for 478 blobs that answered 200 by hand.
- `li-audit` taking a **top-K over a table that holds every release**, so its
  window was versions rather than candidates, and letting a 19 KB table of
  contents stand as evidence for the 43 events citing `33.108 §Annex`.

### Performance

| Lever | Before | After |
|---|---|---|
| `fetch` (403 is not-found) | 54m35 | **4m10** |
| Merge's compact copy (skip the FTS it rebuilds) | 77 min | **~6 min** |
| `index` with its own ceilings | 19m05 | **1m46** |
| Vector import (let DuckDB read the ledger) | — | **23×** |
| Corpus on disk | 135 GB | **30 GB** |

---

## Skills Architecture v1.5 (2026-05-20)

### Changed — v1.5 patch on top of v1.4

- `/refine` directive char-cap is now **uniformly 4000 chars** (the
  actual `/goal` tool limit, not 4096 — corrected from v1.4).
- The cap is a **target**, not a floor: natural output may be shorter
  when content warrants it; the skill never pads to hit 4000.
- Dual budget removed (no more LIGHT 2000 / FULL 4096 split). LIGHT vs
  FULL now affects only **lens depth** (4 critical vs all 10), never
  char-cap.
- `/refine` now **auto-detects mode** from argument shape + disk state.
  Explicit `--bare` / `--from-contract` flags become **overrides** for
  edge cases, not the primary entry point:
  - `/refine "free-form text"` → auto BARE (arg has spaces)
  - `/refine my-slug` (with plan+context on disk) → auto FULL
  - `/refine my-slug` (only goal on disk) → auto FROM-CONTRACT
  - `/refine inexistant-slug` → BARE (slug treated as description)
- `--lenses light|full` replaces `--light` / `--full` for FULL mode
  lens-depth override.

## Skills Architecture v1.4 (2026-05-20) — superseded by v1.5

### Changed — v1.4 patch on top of v1.3

- `/refine` gains three input modes (initial design used explicit
  `--bare` / `--from-contract` flags; v1.5 supersedes with auto-detection).
- Budget logic moves to single source of truth in
  `refine/synthesis.md`; BARE and FROM-CONTRACT reuse the same
  compact step as FULL.
- The "standalone /goal" use case ships without bringing back the
  deprecated `/prompt` skill — the migration doc remains valid.

## Skills Architecture v1.3 (2026-05-20)

### Added

- `/refine` skill: 10-lens goal-contract generator with AUTO mode,
  static lens fallback (router-independent for critical lenses), and
  Markdown-frontmatter-aware metadata parser.
- `route-agent.sh`: router that resolves `(skill, phase, profile)` to a
  concrete `(subagent_type, model, effort)` dispatch via
  `routing-table.jsonl`. Supports `agent_template` + `expand_from` for
  per-language fanout.
- `goal-state.sh`: lifecycle CRUD on `.claude/state/goals/<slug>.json`
  (create/read/update/mark-stale/gc) enabling `/do --goal-turn`.
- `probe-primitives.sh`: emits `.claude/state/primitives.json` with
  presence and `ExitPlanMode` schema for the 16 primitives the
  initiative depends on.
- `frontmatter.sh`: helper extracting YAML frontmatter from `.md` files
  before `yq` evaluation (fixes the v1.2 bug where `yq` was invoked on
  the full Markdown body).
- 5 new specialist agents: `developer-specialist-react`,
  `data-specialist-postgres`, `developer-specialist-playwright`,
  `devops-specialist-cloudflare`, `tooling-specialist-github-actions`
  (86 agents total, up from 81).
- 6 new facets in `detect-project.sh`: `cloud[]`, `container[]`, `k8s`,
  `os`, `ci`, `test_frameworks[]`.
- `agent-drift-patterns.md` + `migrated_skills.txt` + `routing-table.jsonl`.
- `primitives-compat.md`: documented fallback policy per primitive.

### Changed

- `/plan` Phase 6.0 now invokes `ExitPlanMode(plan=<full md>)` with
  schema validation against `.claude/state/primitives.json`.
- `/plan` gains `--goal` flag — chains into `/refine` via `Skill`.
- `/git --merge` uses `mcp__github__merge_pull_request` (no
  `gh pr merge` fallback).
- `/git --watch` prefers `Monitor` over `sleep(60)` polling.
- `/do` adds `--goal-turn <slug>` flag and `Skill(*)` allowed-tool.
- `/do` loop emits `PushNotification` on terminal state.
- `/ktn` dispatches `devops-executor-linux` instead of `general-purpose`.
- `/search` parallel mode routes per-language specialists via
  `agent_template` instead of generic `Explore`.
- `/warmup` scan/read use `docs-analyzer-*` specialists.
- `/review --loop`, `/git --commit`, `/init`, `/search` now use real
  `Skill(...)` recursive calls instead of magic-string `/X` mentions
  (cycle detection capped at depth 5).
- `registry.json` counts: 79 → 86 agents, distribution opus 3→4,
  sonnet 32→38, haiku 46→39.
- `AGENTS.md` header: 79 → 86.

### Removed

- `/prompt` skill removed (`.devcontainer/images/.claude/commands/prompt.md`).
  Use `/refine` instead — see
  `.devcontainer/images/.claude/docs/migrations/prompt-to-refine.md`.
