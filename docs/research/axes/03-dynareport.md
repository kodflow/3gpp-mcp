# Axis 03 — Seeding `specs` / `spec_versions` from the 3GPP DynaReport (not filenames)

> Research axis #3. Goal: replace filename-code-derived metadata (`spec_id`,
> `release`, `version`, TS/TR, working-group) with authoritative metadata from
> the **3GPP DynaReport** + **portal Releases** report, and add the missing
> **`freeze_date`** so version ordering is correct (versions are NON-MONOTONIC).
>
> All HTTP examples below were fetched live on **2026-05-25/26**.
> Constraint honoured: this axis touches **only this file** (plus an optional
> probe script `scripts/research/03-dynareport-probe.sh`). No Go / schema /
> Makefile changes are proposed for execution here — this is design-only.

---

## 0. Problem recap (why filenames are not enough)

Current code (`internal/model/spec3gpp.go`) derives everything from the FTP
filename version code:

- `DecodeVersionCode("j60")` → `("Rel-19", "19.6.0")` — release + dotted version.
- `WorkingGroupForSeries(series)` → a **guess** (one WG per whole series).
- TS vs TR — not in the filename at all (guessed / inferred from series).
- `freeze_date` — **does not exist in the filename**.

Three concrete defects this axis fixes:

1. **Non-monotonic versions.** `23.501 v16.20.0` (a late Rel-16 maintenance
   release) is *published after* `v17.5.0`. Ordering by semver of the version
   string is wrong; ordering must key on the **release freeze date** plus the
   per-version upload date. Neither is in the filename.
2. **WG is per-spec, not per-series.** Series 24 alone spans C1/C2/C3; series 33
   maps to S3 (SA3) but `WorkingGroupForSeries("33")` cannot express that a
   *specific* spec belongs to S3. Live proof from the status report:
   `TS 33.128 → S3`, `TS 24.501 → C1`, `TS 29.501 → C4`, `TS 38.331 → R2`,
   `TS 23.501 → S2`. A single series→WG map is unreliable; DynaReport gives the
   true WG per spec.
3. **TS/TR + title are authoritative in DynaReport**, not guessable from a code.

DynaReport is the upstream source of truth for *metadata*; the FTP archive
stays the source of truth for *content* (the DOCX/DOC bytes we parse).

---

## 1. The two authoritative DynaReport endpoints

There are **two** machine-readable HTML reports that together cover every field
in our `specs` + `spec_versions` schema. Both are public (no login), both are
plain server-rendered HTML tables (no JS needed to read the data), and both
require a **browser `User-Agent`** (a bare `curl` UA gets a 302 loop / 403).

### 1.1 Spec catalogue + per-version history — `status-report.htm`

```
Canonical URL: https://www.3gpp.org/dynareport?code=status-report.htm
(the legacy /DynaReport/status-report.htm 302-redirects to the above)
```

- One ~5 MB HTML document containing **36 tables**, one per release section
  (anchors `#activeRel-19`, `#deadRel-16`, …) plus an "all specs" catalogue.
- Catalogue table columns (verbatim `<th>`): **`type`**, **`spec num`**,
  **`title`**, **`vers`** (latest version *in that release section*), **`WG`**.
- Live sample rows (verbatim cell values, fetched 2026-05-25):

  | type | spec num | title | vers | WG |
  |------|----------|-------|------|----|
  | TS | 23.501 | System architecture for the 5G System (5GS) | 19.7.0 | S2 |
  | TS | 24.501 | Non-Access-Stratum (NAS) protocol for 5G System (5GS); Stage 3 | 19.6.2 | C1 |
  | TS | 29.501 | 5G System; Principles and Guidelines for Services Definition; Stage 3 | 19.4.0 | C4 |
  | TS | 33.128 | Security; Protocol and procedures for Lawful Interception (LI); Stage 3 | 19.6.0 | S3 |
  | TS | 38.331 | NR; Radio Resource Control (RRC); Protocol specification | 19.2.0 | R2 |
  | TR | 21.900 | Technical Specification Group working methods | 20.0.0 | SP |

  → gives us `doc_type` (TS/TR), `title`, `working_group` (real WG code), and
  the **latest version per release** for every spec, in one fetch.

- Each `spec num` cell links to a **per-spec detail page**:
  `/DynaReport/<digits>.htm` (e.g. `23501.htm`), which 302-redirects to the
  portal `SpecificationDetails.aspx?specificationId=<id>`. That page carries the
  **full version history table** (one row per published version) with columns:
  **`Version`**, **`Upload date`**, plus meeting + comment. Live sample for
  23.501 (`SpecificationDetails.aspx?specificationId=3144`):

  | Meeting | Version | Upload date | Comment |
  |---------|---------|-------------|---------|
  | SA#111 | 20.1.0 | 2026-03-16 | Updated with CRs approved at … |
  | SA#110 | 20.0.0 | 2025-12-19 | Updated with Rel-20 CRs approved … |
  | SA#111 | 19.7.0 | 2026-03-16 | Updated with CRs approved at … |
  | SA#110 | 19.6.0 | 2025-12-19 | Updated with CRs approved at … |
  | SA#109 | 19.5.0 | 2025-09-24 | … |
  | SA#106 | 19.2.1 | 2025-01-07 | MCC Implementation correction … |

  → the per-version **`Upload date`** is exactly the disambiguator the
  CLAUDE.md §8 piège #3 demands: it orders versions *within and across*
  releases independently of the semver string. The `Version` major number is the
  release ordinal (19.x.x ⇒ Rel-19), so release attribution is exact here too.

### 1.2 Release-level freeze dates — `Releases.aspx`

```
URL: https://portal.3gpp.org/Releases.aspx
```

This is the authoritative **release** report and the **freeze_date source**.
Columns (verbatim `<th>`): **`Release Code`**, **`Name`**, **`Status`**,
**`Start date`**, **`End date`**, **`Closure date`**. Live sample (2026-05-26):

| Release Code | Name | Status | Start date | End date | Closure date |
|---|---|---|---|---|---|
| Rel-21 | Release 21 | Open   | 2025-11-04 | (empty) | (empty) |
| Rel-20 | Release 20 | Open   | 2024-03-14 | 2027-06-18 (SA#116) | (empty) |
| Rel-19 | Release 19 | Frozen | 2021-06-18 | 2025-12-12 (SA#110) | (empty) |
| Rel-18 | Release 18 | Frozen | 2019-09-16 | 2024-06-21 (SA#104) | (empty) |
| Rel-17 | Release 17 | Frozen | 2018-06-15 | 2022-06-10 (SA#96)  | (empty) |
| Rel-16 | Release 16 | Frozen | 2017-03-22 | 2020-07-03 (SA#88-e) | (empty) |

Interpretation:

- **`End date`** = the date the release was **frozen** (functional/protocol
  freeze), annotated with the TSG-SA plenary that froze it (`SA#110`). This is
  the value to store as **`freeze_date`** in `spec_versions` (per release) and
  to drive cross-release ordering. For "Open" releases `End date` is the
  *planned* freeze (e.g. Rel-20 → 2027-06-18) and `freeze_date` should be NULL
  (or stored with a `status='planned'` flag).
- **`Status`** (`Open` / `Frozen` / `Closed`) feeds a new `status` column and
  the CLAUDE.md §8 piège #8 distinction ("frozen ≠ stable").
- **`Start date`** is the release opening; useful context, not required.
- Each row links to `ReleaseDetails.aspx?releaseId=<n>` (stage-1/2/3 sub-freeze
  dates), but that ID is injected client-side — a naive
  `ReleaseDetails.aspx?releaseId=197` returns **404**. Do **not** depend on it
  for V1; the `Releases.aspx` `End date` column is sufficient and stable.

### 1.3 Endpoint summary

| Field needed | Endpoint | Column |
|---|---|---|
| `doc_type` (TS/TR) | status-report.htm | `type` |
| `title` | status-report.htm | `title` |
| `working_group` (real, per spec) | status-report.htm | `WG` |
| latest version per release | status-report.htm (per-section table) | `vers` |
| full version list + per-version date | `<spec>.htm` → SpecificationDetails.aspx | `Version`, `Upload date` |
| `release` of a version | derived from version major (19.x ⇒ Rel-19) | `Version` |
| **`freeze_date`** (release-level) | Releases.aspx | `End date` |
| `status` (Open/Frozen/Closed) | Releases.aspx | `Status` |
| `series` | derived from `spec num` prefix (already done) | — |

There is **no documented JSON/XML/Excel export**: the ASMX webservices probed
(`/webservices/3gppspecs.asmx` → 500, `/DesktopModules/.../Release.asmx` → 404)
are not usable anonymously. **HTML-table scraping of the two URLs above is the
machine-readable path.** The HTML is server-rendered and column-stable; parse it
with the project's existing `internal/htmlparse` package (Go `golang.org/x/net/html`),
the same dependency already used elsewhere — **no new dependency**.

---

## 2. Proposed design

### 2.1 New package: `internal/catalog`

A self-contained metadata seeder, separate from content ingestion:

```
internal/catalog/
  catalog.go     // types: SpecMeta, ReleaseMeta, VersionMeta
  fetch.go       // HTTP GET with browser UA + retry/backoff; offline-file mode
  parse.go       // parse status-report.htm, <spec>.htm, Releases.aspx tables
  seed.go        // build []SpecMeta / []spec_versions rows; merge logic
```

Types (illustrative — Go, not for execution under this axis):

```go
type ReleaseMeta struct {
    Code       string    // "Rel-19"
    Name       string    // "Release 19"
    Status     string    // "Open" | "Frozen" | "Closed"
    StartDate  *time.Time
    FreezeDate *time.Time // = Releases.aspx "End date"; nil while planned/Open
    FreezeMeeting string  // "SA#110"
}

type SpecMeta struct {
    SpecID       string   // "23.501"
    Series       string   // "23"
    DocType      string   // "TS" | "TR"
    Title        string
    WorkingGroup string   // "S2", "C1", "S3", "R2" ...
}

type VersionMeta struct {
    SpecID     string
    Release    string    // derived from Version major
    Version    string    // "19.6.0"
    UploadDate *time.Time // SpecificationDetails "Upload date"
    FreezeDate *time.Time // copied from ReleaseMeta.FreezeDate (release-level)
    Meeting    string
}
```

### 2.2 Where it plugs in

A new `cmd/ingest` sub-step (or a dedicated `cmd/ingest --catalog-only` flag)
runs **before** DOCX content ingestion:

```
1. catalog.FetchReleases()        -> []ReleaseMeta   (Releases.aspx)
2. catalog.FetchStatusReport()    -> []SpecMeta + latest-version index
3. for each spec in MVP scope (series 23/24/29/33/38, Rel-17/18/19):
     catalog.FetchSpecVersions()  -> []VersionMeta  (<spec>.htm detail page)
4. seed specs           (spec_id, series, title, doc_type, working_group)
5. seed spec_versions   (spec_id, release, version, freeze_date, docx_url, upload_date, status)
6. THEN run existing DOCX ingest, which now *looks up* metadata instead of guessing
```

This keeps the §1 "reproducibility of ingestion" guarantee: the catalog fetch is
a one-shot that produces deterministic rows; cache the raw HTML under
`data/catalog/<date>/` so a re-run hashes stable.

### 2.3 Merge strategy: DynaReport authoritative for metadata, FTP for content

| Attribute | Authority | Rule |
|---|---|---|
| `title`, `doc_type`, `working_group` | **DynaReport** | overwrite filename guess |
| `release` of a version | DynaReport version major | overwrite filename-code decode |
| `version` string | both agree | DynaReport canonical; FTP code only confirms |
| `freeze_date`, `status` | **Releases.aspx** | only source; filename has none |
| `docx_url` / content bytes | **FTP archive** | DynaReport never carries the file |
| existence of a local file | **FTP archive** | a spec_version row is "ingestable" only if the DOCX exists locally |

Concretely the merge is a **left-join keyed on `(spec_id, version)`**:

- The FTP/filename side enumerates which `(spec_id, version)` we actually
  *have on disk* (and the resolvable `docx_url`).
- The DynaReport side supplies the authoritative metadata + `freeze_date`.
- A row is written to `spec_versions` when the FTP file exists; its
  `freeze_date`/`release`/WG/title come from DynaReport. If DynaReport lacks a
  version we have on disk (rare — e.g. a withdrawn draft), fall back to
  `DecodeVersionCode` and tag `metadata_source='filename'` so it is auditable.
- Keep `DecodeVersionCode`/`EncodeVersionCode` — they remain the bridge between a
  DynaReport version string and the FTP `docx_url` (`EncodeVersionCode("19.6.0")
  → "j60"` builds the archive path). The functions stay; only the *trust*
  shifts: filename stops being the metadata source, becomes a URL builder.

### 2.4 The freeze_date ordering fix

Add `freeze_date DATE` (already present in schema as
`spec_versions.freeze_date` per CLAUDE.md §4) and populate it from
`Releases.aspx.End date` keyed by release. Then **correct ordering** of versions
for a spec is:

```sql
ORDER BY freeze_date NULLS LAST,   -- release-level freeze (cross-release)
         upload_date,              -- per-version publication (intra-release)
         version                   -- final tiebreak
```

This resolves piège #3 (`16.20.0` published after `17.5.0`): Rel-16 freeze
(2020-07-03) < Rel-17 freeze (2022-06-10), but `16.20.0`'s `upload_date`
(2024-xx) is later than `17.5.0`'s — so "latest content" vs "latest release" are
both answerable. Store **both** dates; never rely on the semver string alone.
(Recommend adding `upload_date DATE` and `status VARCHAR` to `spec_versions`,
and `status VARCHAR` to a `releases` lookup table — schema change to propose in
its own MR with the `arch-change` label per CLAUDE.md §13, since it touches the
frozen schema.)

---

## 3. Step-by-step implementation plan

1. **Probe script** (allowed by this axis): `scripts/research/03-dynareport-probe.sh`
   fetches the three URLs with a browser UA into `data/catalog/<date>/` for
   offline parser development and golden-file tests.
2. **Parser** in `internal/catalog/parse.go` using `golang.org/x/net/html`
   (existing dep): table-walker that finds `<table>` by header signature
   (`type/spec num/title/vers/WG` and `Release Code/Name/Status/Start date/End date`).
   Robust to column reordering by indexing on header text, not position.
3. **Releases parser** → `[]ReleaseMeta`; parse `End date` as
   `YYYY-MM-DD` stripping the trailing ` (SA#nnn)` into `FreezeMeeting`.
4. **Status-report parser** → `[]SpecMeta`; the per-release sections also yield
   the latest version per (spec, release) for a cheap path that skips the
   per-spec detail fetch when only the latest version is needed.
5. **Spec-detail parser** → `[]VersionMeta` (full history + `Upload date`) for
   the MVP-scope specs; map spec→`specificationId` by following the
   `<spec>.htm` redirect once and caching the ID.
6. **Seeder** `seed.go`: join with the on-disk FTP inventory, populate `specs`
   and `spec_versions`, set `metadata_source` provenance.
7. **Schema MR** (separate, `arch-change`): add `releases` table + `upload_date`,
   `status`, `metadata_source` to `spec_versions`; adjust `freeze_date`
   population.
8. **Ingest wiring**: `cmd/ingest` gains a catalog phase that runs first; DOCX
   ingest reads metadata from the seeded tables instead of `WorkingGroupForSeries`.
9. **Tests**: golden-file tests in `internal/catalog` against the cached HTML
   (status-report excerpt, one spec detail, Releases.aspx) asserting the sample
   rows in §1 parse exactly.
10. **Deprecate** `WorkingGroupForSeries` as a metadata source (keep only as a
    last-resort fallback with a logged warning).

---

## 4. Risks & mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| HTML layout/column change upstream | parser breaks silently | header-signature matching + golden tests + `metadata_source` audit column; CI canary fetch |
| 302/403 without browser UA | empty fetch | set `User-Agent: Mozilla/5.0 …`, follow redirects; documented in §1 |
| `status-report.htm` is ~5 MB | memory/time | stream-parse; or fetch per-release `SpecList` sub-pages (`?code=SpecList.htm&release=Rel-19`) to shard |
| `SpecificationDetails` values via AJAX | some labels empty in static HTML | the **version-history table is static** (proven in §1.1); only the top metadata block is JS — take title/type/WG from status-report instead, which is static |
| `ReleaseDetails.aspx?releaseId=` 404 | no stage-level freeze | use `Releases.aspx End date` (release-level) for V1; stage-level deferred to V2 |
| No JSON/XML export | scraping only | accepted; HTML is stable & server-rendered; isolate in `internal/catalog` so a future API swap is one-file |
| DynaReport vs FTP version mismatch | orphan rows | left-join on `(spec_id, version)`; FTP presence gates ingestion; fallback decode tagged as `filename` |
| Rate limiting on per-spec fetches (~150 specs × N) | throttle/ban | sequential with backoff; cache by date; only fetch detail pages for MVP-scope specs |

---

## 5. Real fetched example (the load-bearing evidence)

**Release freeze date (Releases.aspx, 2026-05-26):**

```
Release Code | Name       | Status | Start date | End date              | Closure date
Rel-19       | Release 19 | Frozen | 2021-06-18 | 2025-12-12 (SA#110)   | (empty)
```
⇒ `freeze_date('Rel-19') = 2025-12-12`, `status='Frozen'`, `freeze_meeting='SA#110'`.

**Per-version row (SpecificationDetails.aspx?specificationId=3144 = TS 23.501, 2026-05-25):**

```
Meeting | Version | Upload date | Comment
SA#110  | 19.6.0  | 2025-12-19  | Updated with CRs approved at SA#110
```
⇒ `spec_versions(spec_id='23.501', release='Rel-19', version='19.6.0',
   upload_date='2025-12-19', freeze_date='2025-12-12', docx_url=<from FTP, code j60>)`.

**Catalogue row (status-report.htm, 2026-05-25):**

```
type | spec num | title                                       | vers   | WG
TS   | 23.501   | System architecture for the 5G System (5GS) | 19.7.0 | S2
```
⇒ `specs(spec_id='23.501', series='23', doc_type='TS',
   title='System architecture for the 5G System (5GS)', working_group='S2')`.

These three rows, joined, produce a fully-sourced `specs` + `spec_versions`
record with a correct `freeze_date` — none of which the filename `j60` could
provide.

---

## 6. Sources

- 3GPP specification status report — https://www.3gpp.org/dynareport?code=status-report.htm
- Per-spec detail (example TS 23.501) — https://www.3gpp.org/dynareport?code=23501.htm → https://portal.3gpp.org/desktopmodules/Specifications/SpecificationDetails.aspx?specificationId=3144
- 3GPP Releases report (freeze dates) — https://portal.3gpp.org/Releases.aspx
- Per-release spec list (shardable) — https://www.3gpp.org/dynareport?code=SpecList.htm&release=Rel-19
- Releases narrative (context, no structured freeze) — https://www.3gpp.org/specifications-technologies/releases/release-19
- Freeze definition reference — 3GPP TR 21.900 (linked from status-report.htm)
