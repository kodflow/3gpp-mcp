# The 3GPP-style ("SCP") change-history annex: decision report

**Decision (2026-09-12): no writer, because the corpus does not contain the table.** The 146 ETSI
deliverables whose change-history annex is the 3GPP-style grid (`Date | Meeting | Tdoc | CR | Rv |
Cat | Subject | Old | New`) — TS 102 221, TS 102 223, TS 102 226 and their family — were converted
with `pdftotext -layout`, which flattens the grid by vertical position and loses which cell belongs
to which row. Measured against the published PDF: of the records a reader could extract from that
flattened text, **half state a fact the document contradicts**, and the guard this was supposed to
be built with — validating every column against the deliverable's real version chain — **admits 29
records of which 4 are right**.

The table itself is perfectly readable. `pdftotext -table`, the same binary that produced the
corpus, reconstructs it: 214 rows against 35, and two independently typeset publications of TS 102
221 agree on **209 of 209** common CRs in every column. The missing piece is upstream, in
`scripts/lib/convert.sh`, not in a reader. Its price is costed at the end.

Everything below was measured read-only on the converted archive (`data/sources/convert-etsi`,
11 822 versions of 5 142 deliverables) and on four PDFs downloaded from `etsi.org/deliver` — a
handful of polite requests, spaced out.

## 1. What the 942 annexes actually are

`ingest-etsi-changes` (#335) reports a change-history annex in "at least six layouts" — 954
deliverables in its module docs, 943 in the line it prints at the end of a run. Classifying the
annex of every version of every deliverable gives 942 and reduces the six layouts to three:

| layout | versions | deliverables using it in ANY version | in their NEWEST version |
|---|---:|---:|---:|
| no change-history annex | 7 991 | 4 200 | 4 234 |
| **narrative** — `Date \| Version \| Information about changes` | 2 300 | 811 | 729 |
| **3GPP/SCP table** — `Date \| Meeting \| Tdoc \| CR \| Rv \| Cat \| Subject \| Old \| New` | 1 232 | 146 | 137 |
| **one CR per line** — `CR027r2 (cat B) Generic object mechanism` (what #335 reads) | 299 | 45 | 42 |

(A deliverable can change layout between versions, so the middle column counts it once per layout it
ever used and sums above 942.)

The **narrative** layout is the largest bucket and it carries **no CR number at all** — just
"2.2.1 Clarifications about tolerances and measurement uncertainties". `changes.cr_number` has
nothing to hold, so those 811 deliverables are out of reach of this table by construction, whatever
the conversion. They are a different feature (a per-version release note), not a changelog.

That leaves the **146 table deliverables** as the whole prize, and this report is about them.

## 2. Why `-layout` cannot read the table

Each cell of a table row is vertically **centred** in the row box. `pdftotext -layout` emits text
sorted by vertical position, so a row whose subject wraps over three lines has its short cells
printed on the row's *middle* line, above or below the cells of its own subject — and next to the
cells of its neighbours. TS 102 221 V18.4.0, last annex page, exactly as the corpus holds it
(`data/sources/convert-etsi/ETSI/TS_102_221_v18.4.0.html`):

```
Date Meeting    TC SCP Doc. CR Rv  Cat                     Subject/Comment                         Old New
2025-06 SET-118  SET(25)000043 360                                                                  18.2.0 18.3.0
F Clarification of LSI configuration procedure in clauses 7.5
2026-04 SET-121  SET(26)000016 361                                                                  18.3.0 18.4.0
and 11.1.25.1
F Clarification on receiving non-allowed command when
card is suspended
```

`and 11.1.25.1` is the tail of CR360's subject, printed *after* CR361's identity line. CR360 and
CR361 each have their category and their subject on a line that carries neither their CR number nor
their versions. The same page through `pdftotext -table` (blank separator lines removed):

```
Date     Meeting  TC SCP Doc.    CR   Rv  Cat                     Subject/Comment                           Old     New
2025-06  SET-118  SET(25)000043  360      F    Clarification of LSI configuration procedure in clauses 7.5  18.2.0  18.3.0
                                               and 11.1.25.1
2026-04  SET-121  SET(26)000016  361      F    Clarification on receiving non-allowed command when          18.3.0  18.4.0
                                               card is suspended
```

Two further facts close the door on reading the `-layout` text by column position:

- `scripts/lib/convert.sh:267` strips leading whitespace from every line before writing the HTML, so
  the absolute column offsets `-layout` does preserve are gone from the corpus;
- **restoring them would not be enough.** In the block above, the `F` is in the Cat column on both
  readings. Column position says *which column*; it never says *which row*, and the row is what is
  lost.

## 3. What a reader on the flattened text would actually write

The most favourable possible shape was used: a single line carrying **every** field of a row —
date, meeting, tdoc, CR number, optional revision, category, subject, old and new version. Across
the newest version of all 137 table deliverables, 8 708 annex lines yield **268** such lines.

Loosening the shape buys nothing. Making the subject and the category optional raises the CRs read
at least once on TS 102 221 from 81 to 104 — and the ones whose readings *contradict each other*
(§3.1) from 19 to 29. Coverage and error grow together, which is what "the row is lost" means.

### 3.1 The flattening contradicts itself

The annex is cumulative, so the row for CR 004 is re-typeset and re-flattened in every later
version. If `-layout` were merely lossy the surviving readings would agree. They do not:

| deliverable | versions held | CRs read at least once | agree on `old → new` | **disagree** |
|---|---:|---:|---:|---:|
| TS 102 221 | 126 | 81 | 62 | **19 (23 %)** |
| TS 102 223 | 121 | 78 | 49 | **29 (37 %)** |
| TS 102 226 | 70 | 85 | 55 | **30 (35 %)** |

And not as one outlier against a consensus: TS 102 221 CR078 is read `6.1.0 → 6.2.0` by ten versions
and `6.4.0 → 6.5.0` by four. A majority vote would still write a transition four published documents
deny.

### 3.2 Against the published PDF

Ground truth was built by running `pdftotext -table` over the annex pages of the PDF downloaded from
`etsi.org/deliver` and comparing, CR by CR, with what the corpus's `-layout` text says. Tdoc,
category and both versions are compared exactly; the subject is compared with all whitespace
removed, so neither reading is penalised for where it broke a line:

| deliverable | rows in the table | `-layout` full rows | coverage | tdoc | cat | `old → new` | subject | **all four right** |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| TS 102 221 V18.4.0 | 214 | 35 | 16 % | 30 | 15 | 11 | 6 | **5 (14 %)** |
| TS 102 223 V18.3.0 | 221 | 13 | 6 % | 2 | 4 | 1 | 0 | **0 (0 %)** |
| TS 102 226 V19.0.0 | 91 | 50 | 55 % | 45 | 49 | 48 | 50 | **44 (88 %)** |
| **total** | **526** | **98** | **19 %** | 77 | 68 | 60 | 56 | **49 (50 %)** |

Sample of what the corpus text claims, against the document it would cite:

```
CR004  -layout : tdoc 9-00-0293  cat F  4.1.0 -> 4.1.0  "Correction and clarification of the provision of Contact C6"
       truth   : tdoc 9-00-0293  cat F  0.0.0 -> 3.0.0  "Clarification of GET RESPONSE command usage"
CR103  -layout : tdoc SCP-030050 cat C  7.0.0 -> 7.1.0  "UICC presence detection procedure and usage modification"
       truth   : tdoc SCP-020210 cat F  5.1.0 -> 5.2.0  "Clarification of default SE and default Usage Qualifier"
```

TS 102 226 scoring 88 % while TS 102 223 scores 0 % is not a reason for hope: **nothing in the
flattened text distinguishes them.** A reader cannot know which document it is in.

### 3.3 The version-chain guard does not catch it

The guard specified for this work — accept a record only if `old` and `new` are two *consecutive*
published versions of the deliverable — was measured against the authoritative chain,
`etsi.org/deliver/etsi_ts/102200_102299/102221/` (126 folders, exactly the 126 versions the archive
holds), on TS 102 221 V18.4.0:

| | rows |
|---|---:|
| `-layout` full rows | 35 |
| **admitted** by the version-chain guard | **29** |
| of those, version pair actually right | 9 |
| of those, **every column right** | **4** |

**86 % of what the guard admits is wrong.** The reason is structural and worth writing down: the
flattening's errors are off-by-one-*row* shifts, and in a table whose rows walk the version chain
step by step, a shifted row is *another valid consecutive pair*.

```
CR245  guard says 7.11.0 -> 7.12.0   truth 7.12.0 -> 8.0.0
CR259  guard says  8.0.0 ->  8.1.0   truth  8.1.0 -> 8.2.0
CR290  guard says  9.0.0 ->  9.1.0   truth  9.1.0 -> 9.2.0
```

The guard also has false *rejects*: it refuses 56 of the 214 true rows, because the pre-publication
drafts are numbered `0.0.0` and because ETSI maintains several release streams at once
(`7.12.0 → 8.0.0` is real, and skips the `7.13.0` that the 7.x stream published later).

### 3.4 And the column-free fallback fails too

The last honest option was to drop the columns entirely and reuse #335's rule — a CR absent from
version P's annex and present in V's was included in V — over the bare CR numbers. On TS 102 221's
126 versions, against the same ground truth:

| | |
|---|---:|
| CRs dated | 188 |
| new version **correct** | 166 (88 %) |
| new version **wrong** | 22 |
| numbers dated that **are not a CR of this deliverable** | **111** |

In the one-CR-per-line layout the anchor is the literal string `CR`; in the SCP layout the cell is a
bare three-digit number, indistinguishable from a page number, a clause number or a fragment of a
tdoc id. 111 fabricated records for 188 real ones is the same failure as the ETSI leak of
2026-09-01: a corpus made false by containing too much.

## 4. What would work, and what it costs

`pdftotext -table` — the same xpdf 4.06 binary that produced this corpus, proved by running its
`-layout` on the freshly downloaded TS 102 221 V18.4.0 and getting page 205 back byte-identical to
what the archive holds — reads the grid. Validated the way §3.1 falsified
`-layout`, on two independently published PDFs of TS 102 221 whose annex is typeset on different
pages with different wrapping:

| measure | value |
|---|---:|
| rows read from V18.4.0 | 214 |
| rows read from V17.4.0 | 209 |
| CRs the two have in common | 209 |
| of those, agreeing on **tdoc** | 209 (100 %) |
| of those, agreeing on **cat** | 209 (100 %) |
| of those, agreeing on **old** | 209 (100 %) |
| of those, agreeing on **new** | 209 (100 %) |

Against `-layout`'s 23–37 % self-contradiction, on the same documents.

The price of switching:

- **the PDFs are gone.** `data/sources/etsi-origin` is empty — `scripts/etsi-fetch.sh` converts and
  deletes. Reading the annex again means downloading again: 1 232 versions for the table
  deliverables alone, 11 822 for the whole archive.
- **every converted file changes.** `-table` emits the same text — non-whitespace character counts
  are identical (432 580 for TS 102 221, 539 043 for TS 102 223, 109 588 for TS 102 226, both ways)
  — but breaks lines differently (13 115 → 16 333 lines for TS 102 221). Every ETSI HTML file moves,
  so `parse-etsi`, `chunk`, `embed` and `sparse` all replay and `data/etsi.duckdb` is rewritten:
  **~19.5 GB re-pushed**. The surgical variant — keep `-layout` for the body and append a `-table`
  rendering of the annex pages only — touches only the 1 232 versions that have a table, and is the
  one worth costing first.
- **`-table` is an xpdf flag.** `scripts/local/setup-wsl.sh` installs poppler-utils, whose
  `pdftotext` does not have it (it has `-tsv` and `-bbox-layout`, from which the same reconstruction
  is possible, at more effort). Not measured here: no poppler binary on this machine. Any change
  must pin which `pdftotext` the conversion is entitled to assume.

## 5. Deliberately left out

- **The 811 narrative annexes.** No CR number, nothing `changes` can store. A per-version release
  note is a different feature.
- **Majority voting across versions.** §3.1 shows the readings split into camps of ten against four,
  not one against many; a vote would put a number on a guess.
- **`ingest-etsi-changes` itself.** Its `annex_section` takes the *last* change-history heading, and
  the heading is a running page header repeated on each page of a multi-page annex — so it reads
  only the final page. Checked: of the 42 deliverables whose newest version is in the layout it
  reads, exactly one has a multi-page annex, and it loses no CR line to the truncation. Nothing to
  fix today — but 69 of the 137 table deliverables have one, so it will matter the day that reader
  is pointed at the table layout.

## 6. How to re-measure

```sh
# the corpus's own reading of the annex — one <p> per pdftotext line, leading
# blanks already stripped by convert.sh, so the excerpt in §2 is what is stored
sed -e 's/<[^>]*>//g' data/sources/convert-etsi/ETSI/TS_102_221_v18.4.0.html | sed -n '7946,7955p'

# the same annex, from the published PDF, read as a table
curl -fsSL -A "Mozilla/5.0" -H "Accept: application/pdf,*/*" \
  -o 221.pdf https://www.etsi.org/deliver/etsi_ts/102200_102299/102221/18.04.00_60/ts_102221v180400p.pdf
pdftotext -table  -f 201 -l 205 221.pdf -   # the grid, one row per row
pdftotext -layout -f 201 -l 205 221.pdf -   # what the corpus has

# the authoritative version chain
curl -fsSL -A "Mozilla/5.0" https://www.etsi.org/deliver/etsi_ts/102200_102299/102221/
```
