# Changelog

All notable changes to this project are documented here.

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
  corpus, ETSI included. Written but **not exercised**: no Docker runtime here.

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
