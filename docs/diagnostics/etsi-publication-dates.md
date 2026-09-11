# ETSI publication dates → `spec_versions.freeze_date`: decision report

**Decision (2026-09-11): no writer.** The only in-corpus source is the "History" page at the end of each deliverable. It is **not good enough** to write into `freeze_date`:

- it gives a **month**, and the column is a `DATE`;
- it names **the document's own date**, which is sometimes not the publication date;
- it covers **two thirds** of the versions.

An authoritative source exists, the ETSI Work Programme. Using it needs a fetch step and a choice about the column. Both are costed below, for whoever takes that decision.

Everything below was measured read-only on the converted archive (`data/sources/convert-etsi`, 11 822 versions of 5 142 deliverables) and on the corpus served on 2026-09-11. It was cross-checked against two external sources. Only a few requests were made to each, spaced out to stay polite.

## What `freeze_date` holds today

| half | rows | `freeze_date` set | meaning |
|---|---:|---:|---|
| 3GPP | 20 163 | from the portal release calendar | the **functional freeze** of the release (portal `Releases.aspx`, "End date"): one date per release, not per version |
| ETSI | 11 822 | **0** | — |

So the column does not mean "publication date", even on the 3GPP half. An ETSI publication date stored there would make `list_releases` label two different facts with one name.

## The candidate sources, measured

### 1. The "History" page in each deliverable (in-corpus)

Example line: `V1.23.1  March 2026  Publication`. The page is read from the newest version of each deliverable, where it lists every earlier version.

| measure | value |
|---|---:|
| deliverables with a History page | 4 291 / 5 142 |
| versions with ONE unambiguous publication month | **7 995 / 11 822 (67.6 %)** |
| versions with two different "Publication" months (ES/TS double publication, re-publication) | 526 |
| versions with no date | 3 301 |
| — of which: in deliverables with no History page | 1 391 (852 deliverables) |
| — of which: not listed on a page that exists | 1 910 |
| precision | **month** (`March 2026`), never a day |

#### Cross-check 1: the cover stamp `V1.23.1 (2026-03)`

The cover date is found in 4 134 versions. The converter drops most page headers, so it is rarely available. Where both dates are present, they agree in **1 831 of 1 853 cases (98.8 %)**. Of the 22 disagreements, 12 are ENs. Across all 22, the History month comes before the cover month in 10 cases and after it in 12. Examples: EN 300 330 V1.2.2 (History 1997-01, cover 1999-05), EN 301 102 V1.1.1 (1997-12 / 1998-06), EN 301 087 V8.1.1 (2000-09 / 2000-08).

#### Cross-check 2: the ETSI Work Programme

The Work Programme (`portal.etsi.org/webapp/WorkProgram`, public, no login) records `Current Status: Publication (YYYY-MM-DD)` for each version. Two random samples:

| sample | deliverables | versions compared | same month | differ |
|---|---:|---:|---:|---:|
| seed 11 | 12 | 33 | 23 | 10: all of TS 101 348 V7.x, History one month BEFORE publication |
| seed 23 | 30 | 59 | 57 | 2: TR 101 640 V7.0.0 by 2 months, TS 100 925 V8.1.0 by 3 months |
| **total** | **42** | **92** | **80 (87.0 %)** | **12 (13.0 %)**, 1 to 3 months early |

In every disagreement the History month comes **before** the publication. It is the document's own date (approval or cover), not the day ETSI published it. So even the parsed value, converted to a date, would be off by one to three months for about one version in eight.

#### Cross-check 3: the `/deliver` folder date

The listing of `etsi.org/deliver/…/<spec>/` shows a date for each `VV.VV.VV_60` folder. On TS 103 221-1 it matches the Work Programme (01.23.01_60 is 5 March 2026, WP says "Publication (2026-03-05)"). Across a sample of 40 deliverables (80 versions), however, it is an **upload stamp**, not a publication date:

- documents from 1998–1999 all carry **2000-02-03**;
- all 20-odd versions of TS 103 383 (V12.1.0 from 2013 to V14.0.0 from 2018) carry **2022-12-14**.

It can be neither a reference nor a source.

## Why no writer

1. **Precision.** `freeze_date` is a `DATE`. A month would have to be written as the 1st of the month. `list_releases` would then answer `2026-03-01` for a publication of 5 March, which is a day the source never stated. The repository refuses a value it cannot cite.
2. **Accuracy.** The History month is not the publication month for 13 % of the sample. A value that is wrong one time in eight, and says nothing about which time, is the "confident answer that cannot be checked" the project exists to avoid.
3. **Coverage.** 3 301 versions (27.9 %) have no date at all, and 526 have two between which nothing lets us choose, so 3 827 (32.4 %) would stay NULL anyway.
4. **Meaning.** On the 3GPP half the column holds a release freeze. Adding an ETSI publication date would give one name to two meanings.

## If ETSI dates are wanted: a costed path

- **Source: the Work Programme**, the only one that gives the **day** and the status (`Publication`).
  - One listing request per deliverable (`Frame_WorkItemList.asp?qETSI_NUMBER=<n>&qETSI_ALL=TRUE`) returns every version with its publication date.
  - That is 5 142 requests. At one request every 1–2 s, this is a one-off of 1.5–3 h, then incremental (new deliverables only).
  - In the 42-deliverable sample, the WP lacks 2 of the 94 versions compared.
- **Acquisition:** a `scripts/fetch-etsi-wp.sh` script that writes a dated local snapshot. This is the pattern of `fetch-crdb.sh` / `CRDB_<date>.zip`: one file, stamped, re-read without the network.
- **Storage, two options:**
  - (a) Put it in `freeze_date` and **document** that on the ETSI half it holds the WP publication date. No schema change, but the column name now carries two meanings.
  - (b) A new `publication_date` column plus a provenance stamp. This touches `internal/store/schema.sql`, which `ingest`, `ingest-etsi`, `enrich-etsi` and `build-rust` declare. Rebuilds replay, and both corpora are rewritten once, about 42 GB pushed.
- **Writer:** a Rust binary in a separate file, run by `enrich-etsi`, idempotent on its **output**. It would compare the dates in the table before writing, as `replace_changes` does. `rust/store/src/lib.rs` would not be touched.
- **Price of option (a):** `enrich-etsi` replays, and `etsi.duckdb` is rewritten ONCE, so **~19.5 GB re-pushed**. After that nothing moves until the snapshot changes.

## Reproducing the measurements

The scripts are not part of the repository. They are read-only, rebuildable, and live in the agent's scratch area:

- `dates_measure.py`: History page and cover stamp, over the whole tree (≈ 3.5 min);
- `wp_sample.py`: Work Programme sample, one request every 2 s;
- `deliver_sample.py`: `/deliver` folder dates, one request per second.

The numbers above are their outputs from 2026-09-11.
