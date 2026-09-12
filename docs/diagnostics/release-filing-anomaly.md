# A version filed under a release its own number contradicts: measurement and decision

**Measured 2026-09-12, read-only**, on the published 3GPP corpus (`data/3gpp.duckdb`,
20 163 `spec_versions` rows over 3 568 specs) and on the DynaReport status report the
pipeline cached the same day (`.local/state/status-report.htm`, 5 180 736 bytes,
20 237 `(spec, release)` keys).

The case that opened this: PR #347 left a note — *"26.510 v18.4.0 is filed under
Rel-20 by the catalogue and its clauses are stored only there … That is what the
DynaReport row says."* The first half is true. **The second half is no longer true**:
today's status report does not file 26.510 under Rel-20 at all.

---

## 1. Who decides `spec_versions.release`

**The 3GPP archive URL carries no release.** The canonical download is
`https://www.3gpp.org/ftp/Specs/archive/26_series/26.510/26510-i40.zip`
(`rust/parse/src/lib.rs:130`, `internal/model/spec3gpp.go:154`). There is no
`.../Rel-18/...` anywhere in it. So the release is not read off the file — it is
**chosen**, once, and then copied four times:

| # | where | what it does |
|---|---|---|
| 1 | `rust/parse/src/catalog.rs:44` / `internal/catalog/parse.go:14` | the `activeRel-NN` / `deadRel-NN` anchors cut status-report.htm into per-release sections; every spec row under an anchor becomes `VersionRel{spec, release, version}` |
| 2 | `rust/discover/src/lib.rs` (`emit_worklist`, `emit_repair_worklist`) | that release becomes column 1 of the work list: `Rel-20 <url> <name>` |
| 3 | `scripts/corpus.sh:301,361` | column 1 becomes the directory: `data/sources/convert/Rel-20/26510-i40.html` |
| 4 | `rust/parse/src/lib.rs:176-184` | `parse_filename_meta` reads the release back **off the parent directory** — "the AUTHORITATIVE convert-dir release when present" — overriding the version-major decode |
| 5 | `rust/ingest/src/main.rs:274` → `rust/store/src/lib.rs:1230` | `upsert_version(…)` — `ON CONFLICT DO NOTHING` |

So **the DynaReport section decides, and `rust/discover` is where that decision is
made** — that is the chain as it stood when the corpus in §2 was written, and step 2
copied the section verbatim. (§3 changes step 2, and only step 2: the report still
supplies the release, and `filing_release` may then send the file to the release the
version's own major names. Steps 3-5 are unchanged — they still read whatever column
1 says.) Two consequences fall straight out of step 5:

- `upsert_version` and `fold_shard_buckets` (`rust/store/src/lib.rs:1162`) are both
  `ON CONFLICT DO NOTHING`. **Nothing in the write path ever retires a filing.** The
  corpus is append-only against a catalogue that is not.
- The `enrich` step **throws the catalogue's own `(spec, release, version)` rows
  away**: `ingest_catalog.rs:36` binds them to `_vers` and writes only titles and
  doc types. It could not correct a filing even if it wanted to.

`metadata_source` says `dynareport` on every one of the 20 163 rows
(`ingest_catalog.rs:64` sets it unconditionally), which is accurate for the *title*
and misleading for the *release*: the release came from the convert directory.

## 2. What the corpus holds

`releaseMajor` (`internal/store/store.go:1169`): Rel-99 → 3, otherwise Rel-N → N.

| population | rows | clauses |
|---|---:|---:|
| version major **=** release major | 19 517 | — |
| drafts (major < 3), release unconstrained | 570 | — |
| **version major ≠ release major** | **74** | **7 667** |

(2 further rows carry `release = 'GSM'`, which names no major and is out of scope here.)

The 74, split by what today's live catalogue says about the same `(spec, release)`:

| class | rows | clauses | today's catalogue |
|---|---:|---:|---|
| **A** Rel-4 filings of a Rel-99 document (3.x.y) | 51 | 3 181 | **agrees, exactly** (51/51, same version) |
| **B** 33.816 Rel-11 @ 10.0.0 (also filed Rel-10 @ 10.0.0) | 1 | 215 | **agrees** |
| **C** Rel-20 filings of a Rel-18/Rel-19 document | 16 | 4 112 | **does not list the key at all** |
| **D** metadata-only rows (32.291 Rel-17, 29.532 + 29.559 Rel-18, 29.486 Rel-19) | 4 | 0 | lists the key, at a **newer** version |
| **E** `32.153-031` and `32.807-003` @ `27.14.31`, Rel-7 | 2 | 159 | **does not exist** |

Class A is the whole point of the mechanism and must be kept: the corpus has **no
Rel-99 section at all** (the floor is Rel-4), so those 51 Rel-4 rows are the only
copy of 3 181 clauses, and the report still files them there. 3GPP carries a version
forward into a release that never re-issued the spec, and `cmd/anchorcheck` already
names it: *"3GPP routinely lists a spec's Rel-N entry at the Rel-(N-1) version, so
this is bookkeeping, not a gap"* (its `NonContent` verdict, `cmd/anchorcheck/main.go:93`).

### Class C, the defect

```
spec_id | release | version | clauses | same version, its own release
26.510  | Rel-18  | 18.5.0  |   364   |
26.510  | Rel-19  | 19.2.0  |   376   |
26.510  | Rel-20  | 18.4.0  |   364   | row exists at Rel-18, 0 clauses
```

All 16 rows: 24.283, 24.549, 24.559, 26.113, 26.264, 26.510, 26.512, 26.517, 26.532,
29.486, 29.557, 29.558, 29.580, 29.583, 29.594, 29.675 — each at a 18.x or 19.x
version. **12 of them carry text, 4 112 clauses, and in all 12 the Rel-20 filing is
the only copy of that version anywhere in the corpus.** The other 4 are the correct
state: bookkeeping row, text under the release the version names.

Thirteen of the sixteen make the corpus contradict itself outright — a later release
served by an **older** version than its predecessor:

```
26.510   Rel-19=19.2.0  ->  Rel-20=18.4.0
29.558   Rel-19=19.7.0  ->  Rel-20=19.5.0
29.486   Rel-19=19.4.0  ->  Rel-20=18.3.0     (and 10 more)
```

Today's report has three rows in that shape and all three involve a **draft**
(23.873 Rel-5 2.0.0, 33.900 Rel-5 0.4.1, 36.833-1 Rel-13 0.4.0), which is legitimate.

### Where class C was written

The `docx_url` names the culprit file, and the two rows of 26.510 @ 18.4.0 are
decisive:

```
26.510 | Rel-18 | 18.4.0 | dynareport | (empty)
26.510 | Rel-20 | 18.4.0 | dynareport | .../26_series/26.510/26510-i40.zip
```

The Rel-18 row is catalogue bookkeeping with no file behind it; the Rel-20 row is the
one that ingested the archive. `data/sources/convert/Rel-20/26510-i40.html` is still
on disk, converted 2026-08-25 14:01.

The 12 that carry text were acquired by the **holes loop of `emit_repair_worklist`**
(`rust/discover/src/lib.rs`). Its own comment records the reasoning and the count:
*"29.558 reports 19.7.0 … while the hole is `29.558|Rel-20` anchored at 19.5.0 … All
twelve such holes on the local corpus were verified to resolve to a real file."* The
hole was real. The remedy took the **corpus's own key** as proof of the release and
re-downloaded a Rel-19 document into `convert/Rel-20/`. Downloading was right; the
directory was not.

`scripts/corpus.sh` already refuses to do this one step later: its download fallback
matches the release-major since 812e7e1 — *"NEVER a higher release's version that
would then be mis-filed under Rel-6 … the version-major IS the release ordinal"*. The four distinct FALLBACK
events in the local build logs all stay inside their release-major. The invariant simply was not held where
the release is chosen.

### Class E, a separate defect (not fixed here)

`rust/parse/src/lib.rs:158` is
`^([0-9]{4,5}(?:-[0-9]+)?)-([0-9a-z]{3}|[0-9]{6})(?:[-_].*)?$`. On a converted file
named `32153-031-rev…`, the greedy part group takes `32153-031` as a multi-part spec
id and `rev` as the version code — `r`=27, `e`=14, `v`=31 — giving spec `32.153-031`
at version `27.14.31`. TS 32.153 v0.3.1 and TS 32.807 v0.0.3 are therefore in the
corpus under ids no caller can ask for, holding 159 clauses. Fixing the regex is a
`rust/parse` change, which replays `ingest` and rewrites the corpus; it is recorded
here rather than done.

## 3. Decision

**The catalogue is right 56 times out of 74** (classes A, B, D) and the writer copied
it faithfully. Those must be served as they are: a 3.x.y document filed under Rel-4
*is* the Rel-4 text, because Rel-4 never re-issued it. PR #347's fold already serves
one copy when several filings carry identical text.

**The writer is wrong 16 times** (class C), and the fault is a missing invariant, not
a missing fetch: *a document is acquired under the release its own version names,
whenever the report files that spec under that release too.* Applied to the same
downloads, 26510-i40.zip lands in `convert/Rel-18/`, `26.510|Rel-20` becomes the
bookkeeping row anchorcheck already expects (`NonContent`), and nothing is lost.

Implemented in `rust/discover/src/lib.rs` (`filing_release`), applied by
`emit_worklist` and by both loops of `emit_repair_worklist`. Class A is untouched
because the condition requires the report to list the spec under the version's own
release, and there is no Rel-99 section — and, since that is an accident of the
report's shape, a target below the release floor is refused as well.

`delta_series` and `emit_repair_worklist` ask "do we already hold this document?"
**of the release the line lands in**. Asked of the key, a carrying row the corpus
can never mirror — no `26.510|Rel-20` row is ever written once the file lands
under Rel-18 — would read as drift on every build for ever.

**What this does NOT do: write the carrying row as bookkeeping.** Nothing in the
pipeline ever has. The only production writer of `spec_versions` is
`upsert_version`, called once per ingested file (`rust/ingest/src/main.rs:274`),
and `enrich` discards the catalogue's own `(spec, release, version)` rows at
`ingest_catalog.rs:36`. So today the carrying key exists only when a file was
ingested under it — which is the defect, not the record. After this change a
carrying row the report carries but the corpus cannot mirror is simply **absent**
rather than **present holding the wrong text**; making it present-and-empty needs
a writer that does not exist. The live report holds 20 237 keys against the
corpus's 20 057, so that writer's first run would insert ~180 rows — a corpus
write, priced with the rest in §4.

Measured against today's live report:

| | before | after |
|---|---:|---:|
| `--emit-worklist` lines | 20 225 | 20 224 — the `Rel-11` duplicate of `33816-a00.zip` re-files onto the `Rel-10` line already there and is dropped: `0 re-filed, 1 deduped` |
| `--repair-plan` lines (the production path) | 201 | 201, **byte-identical** |
| series delta (the ingest matrix) | `["21","23","28","30","33","55"]` | identical |

And replayed against the report as it must have stood at crawl time — today's
Rel-18/Rel-19 rows for these 16 specs, plus the 16 Rel-20 rows the corpus
recorded:

| | before | after |
|---|---:|---:|
| lines filed under Rel-20 | **16** | **0** (16 re-filed) |
| `26510-i40.zip` | `Rel-20 …` | `Rel-18 …` |

## 4. The existing corpus: the price, as it was quoted

**Historical — this is the estimate that was put to the orchestrator, before the
repair was authorised. §5 is what was actually done, and it differs in two ways:
the repair took 2 s rather than the ~1 h this section feared, because it turned out
to need no re-derivation at all; and it does NOT drop the four empty carrying rows
this section proposed dropping.** Kept as written because the decision was taken on
these numbers.

The 16 rows are **not repaired here**, and deleting them would be wrong: 12 of them
hold the only copy of 4 112 clauses. The correct repair is a **re-filing**, not a
deletion:

```
UPDATE the filing's release from Rel-20 to the release its version major names
  (Rel-18 / Rel-19) — 12 rows with text, 4 112 clause occurrences;
then drop the 4 now-duplicate empty Rel-20 rows.        <- NOT DONE, see §5:
                                                           they are the catalogue's
                                                           bookkeeping and are kept
```

Six of the twelve already have a waiting bookkeeping row at the target release
(24.559, 26.113, 26.510, 26.512, 29.486, 29.583), so those collapse onto it.

**Price, which is why it is not done in this PR.** Any write to `spec_versions` or to
the clause occurrences touches `data/3gpp.duckdb` — 23 175 114 752 bytes, one image
layer per corpus half. The only step light enough to host such a targeted idempotent
writer is `enrich` (it already reads `status-report.htm` as an `Input` and already
opens the corpus read-write; a new writer would go in a file of its own beside
`rust/store/src/changes.rs`, for the reason that file exists). Replaying it costs, on
this machine's measured figures: `enrich` ~1 min, then `paragraphs` 20m32, `sparse`,
`compact` 19m22, `index` 8m06 — and because the bytes really do change, the 3GPP
corpus layer is re-pushed in full (22 GB on the wire, `publish` 22m39 measured on
2026-09-11). Roughly **1 h of rework and 22 GB, to correct 16 rows out of 20 163**
(0.08 %).

That is a trade for the orchestrator to make, not for this PR to make silently. The
change here costs nothing and stops the population from growing.

*(The trade was taken. What the estimate above got wrong: it priced `enrich` and a
full replay of `paragraphs`, `sparse`, `compact` and `index` because it assumed any
corpus write must re-derive. It does not — the repair moves rows between releases
and creates none, so the paragraph attestation holds and every derived table is
untouched. The 22 GB layer push is the whole of the cost. See §5.)*

## 5. The repair, as taken (2026-09-12)

The trade was taken. `cmd/repair-release-filing` does it, **dry-run by default**.

**It moves and never deletes,** and the reason it can is that the release axis lives
in exactly two base tables. `clauses` is a VIEW over `clause_occ` joined to `bodies`;
`clause_sparse` is keyed by `chunk_id` alone; `bodies`, `paragraphs` and `body_seq`
are content-addressed and carry no release; the BM25 index is `fts_main_paragraphs`,
over `paragraphs(para_id, part)`. So the whole repair is:

```
UPDATE clause_occ SET release = <the release the version major names>
INSERT the destination spec_versions row where it does not exist        (6 of 12)
UPDATE its docx_url where it exists but names no archive                (6 of 12)
```

No `chunk_id` moves, no body moves, no vector moves, no posting is rewritten.

The §4 plan said to "drop the 4 now-duplicate empty Rel-20 rows". **It does not**,
and that is the correction §4 needed: the carrying row is what the catalogue said,
and once its text is gone it becomes exactly the bookkeeping row `cmd/anchorcheck`
already expects — its `NonContent` verdict. Deleting it would throw away a catalogue
fact to fix a text fact.

**Refusals, each doing work on the real corpus:** 8 filings with no occurrence; the
51 Rel-4 filings of a 3.x.y document (no Rel-99 section exists — Rel-4 is their only
home, and the same rule refuses `32.153-031 @ 27.14.31`, whose "Rel-27" does not
exist); 33.816, filed at 10.0.0 under Rel-10 and Rel-11 with 215 occurrences under
each, which is two copies and not a move; drafts.

### The twelve filings moved

| spec | from | version | to | occurrences | destination |
|---|---|---|---|---:|---|
| 24.283 | Rel-20 | 19.1.0 | Rel-19 | 259 | created |
| 24.549 | Rel-20 | 19.1.0 | Rel-19 | 136 | created |
| 24.559 | Rel-20 | 19.4.0 | Rel-19 | 207 | existed, no archive URL |
| 26.113 | Rel-20 | 19.0.0 | Rel-19 | 156 | existed, no archive URL |
| 26.264 | Rel-20 | 19.1.0 | Rel-19 | 130 | created |
| **26.510** | Rel-20 | 18.4.0 | **Rel-18** | **364** | existed, no archive URL |
| 26.512 | Rel-20 | 18.6.0 | Rel-18 | 466 | existed, no archive URL |
| 26.517 | Rel-20 | 19.1.0 | Rel-19 | 155 | created |
| 29.486 | Rel-20 | 18.3.0 | Rel-18 | 751 | existed, no archive URL |
| 29.558 | Rel-20 | 19.5.0 | Rel-19 | 1 097 | created |
| 29.580 | Rel-20 | 19.4.0 | Rel-19 | 199 | created |
| 29.583 | Rel-20 | 19.1.0 | Rel-19 | 192 | existed, no archive URL |
| | | | | **4 112** | |

### Measured on a copy of the published corpus

| | before | after |
|---|---|---|
| run time | — | **2.0 s** |
| `clause_occ` rows | 2 751 918 | **2 751 918** (nothing lost, nothing invented) |
| `spec_versions` rows | 20 163 | 20 169 (+6 destinations; **0 deleted**) |
| `clause_sparse` rows | 194 051 110 | 194 051 110 |
| distinct releases in `clause_occ` | 19 | 19 (`validate --expected-releases` safe) |
| `clause_occ` under Rel-20 | 103 373 | 99 261 |
| paragraph attestation | `paragraphs=3789493 bodies=897556 body_seq=8404379 clause_occ=2751918` | **identical**, `migrate-paragraphs --attested` exits 0 |
| destinations with no archive URL | 6 of 12 | **0 of 12** |

**The corpus still keeps its own contract.** `validate` with the full 3GPP flag set
(`--require-fts --require-hnsw --require-embed-complete --require-no-reingest
--require-sparse`) passes every gate on the repaired copy, identically to the
original — `clauses=2751918`, `null_at_floor=0`, `hnsw_state="frozen"`,
`sparse_model="b13103bce7ae"`. And `anchorcheck`, against the published anchor:

| | original | repaired |
|---|---:|---:|
| indexed | 20 053 | 20 041 |
| **non_content** (bookkeeping) | 4 | **16** |
| **missing_content** (a hole) | **0** | **0** |
| over_claim / unaccounted | 0 | 0 |

The twelve flip from `Indexed` to `NonContent`, which is the verdict the tool
already has a name for — *"3GPP routinely lists a spec's Rel-N entry at the
Rel-(N-1) version, so this is bookkeeping, not a gap"*. **`missing_content` stays
0**, so the repair opens no hole and the repair-plan loop will never try to
re-acquire what it moved. That is the property the whole "move, keep the carrying
row" design exists for: deleting the carrying row instead would have left twelve
anchor keys with nothing to resolve to.

**Idempotence.** A second `--apply` reports `0 to move … corpus untouched` and leaves
the file identical by sha256; so does a dry run afterwards. The tool opens read-only
until it knows there is work. (Measured honestly: opening this corpus read-write and
closing it without a statement is byte-neutral on this DuckDB build, so the read-only
open is a guarantee rather than a repair of something observed.)

### The real server, on the repaired copy

| call | before | after |
|---|---|---|
| `get_spec(26.510, Rel-18, 18.4.0)` | 364 clauses **cited Rel-20**, note "filed under Rel-20, not Rel-18" | 364 clauses **cited Rel-18** |
| `get_spec(26.510, Rel-18)` | 18.5.0, 364 clauses | unchanged |
| `get_spec(26.510, Rel-20)` | 364 clauses **cited Rel-20** | scoped to Rel-18: "the clauses this corpus holds for 26.510 18.4.0 are filed under Rel-18, not Rel-20" |
| lineage note on that answer | "12 element(s) existed in EARLIER releases but are **gone by Rel-20 (obsolete)**" | "Later releases **ADD** 12 element(s) ([Rel-19:+12])" |
| `get_spec(29.558, Rel-20)` | 1 097 clauses cited Rel-20 | scoped to Rel-19 |
| `get_spec(21.810, Rel-4)` | 57 clauses | unchanged |
| `get_spec(33.816, Rel-11)` | 215, folded, `filed_under [Rel-10, Rel-11]` | unchanged |

The false obsolescence in that fourth row is the one thing the move alone would not
have fixed, and it was **already wrong before the repair**: the lineage axis is
`spec_versions`, so a catalogue filing with no document behind it read as a removal.
Measured on the published corpus, unrepaired: `trace_clause(29.675, 4)` answered
`obsolete=true` for a clause nobody removed, and `trace_clause(26.532, 4.1)` the
same. Obsolescence is now measured against the newest release the spec has TEXT
under (`internal/store.newestReleaseWithText`), on the release axis only. The axis is
untouched — `axis_values` still reports what the catalogue files.

### Price

The repair itself: **2.0 s**, and `data/3gpp.duckdb` changes, so the 3GPP corpus
layer is re-pushed once — 22 GB on the wire (`publish` 22m39, 2026-09-11). Nothing
re-derives: the attestation holds, so `paragraphs` keeps its 0.2 s no-op path,
`bodies`/`paragraphs`/`clause_sparse` are untouched so `sparse`, `compact` and
`index` have nothing to redo. Run it on the final corpus, immediately before
`publish`.

The code costs `build-go`, `test`, `build-serve`, `smoke` and `publish`: a new
`cmd/` package and two files of `internal/store`, which is in the Impl of `validate`
and `validate-etsi` (read-only re-validation, 2m32 + 17.6 s). **No data step replays
from the code change**, and no corpus byte moves because of it.
