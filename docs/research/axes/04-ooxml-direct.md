# Axis 4 — Direct OOXML parsing (replace HTML round-trip)

> **Status:** research / implementation-ready design. No code under `internal/`,
> `cmd/`, `Makefile`, or `go.mod` is changed by this document. It specifies the
> design; a follow-up MR implements it.
>
> **Touches CLAUDE.md §13 ("DOCX uniquement") and §2.** Re-aligns ingestion with
> the *original* DOCX-first decision after the 2026-05-25 HTML detour, so an MR
> implementing this carries the `arch-change` label.

## 0. TL;DR

Today ingestion goes `.doc/.docx → LibreOffice → HTML → internal/htmlparse`.
The HTML step is **lossy on exactly the high-value structures**: it flattens
`w:gridSpan`/`w:vMerge` merged cells, can reorder reading order, and forces a
fragile heading-text heuristic (`"6.2.3\tTitle"` split by tab). The proposal:

1. Keep LibreOffice **only** for the legacy `.doc → .docx` step (`--convert-to docx`,
   not `html`). All `.docx` (already ~45% native + the converted ~55%) are then
   parsed **directly in Go** from `word/document.xml`.
2. Parse with a **thin custom `encoding/xml` streaming parser** (no third-party
   docx lib — justified in §3), modelling paragraphs, runs, and tables with full
   `gridSpan`/`vMerge` reconstruction, plus `pStyle`-driven heading levels and
   ASN.1 (`PL`) block detection.
3. Replace `internal/htmlparse` with a new `internal/ooxml` package exposing the
   **same `ParsedSpec` shape** (`Spec`, `SpecVersion`, `[]Clause`, `[]Change`),
   so `internal/ingest` changes by one import line.

Every claim below is backed by **ground-truth probes of the actual local corpus**
(`data/sources/origin`, 18 781 spec zips), not just vendor docs. Probe specs:
TS 23.501 v18 (`23501-ic0`, 18 MB `document.xml`), TS 29.510 v18 (`29510-ib0`,
ASN.1/OpenAPI-heavy), and a 12-spec frequency sample.

---

## 1. Why the HTML round-trip loses fidelity

`internal/htmlparse/parse.go` consumes LibreOffice HTML. Observed losses:

| Loss | Mechanism in current pipeline | Evidence |
|---|---|---|
| **Merged-cell geometry** | LibreOffice emits `<td colspan>`/`<td rowspan>` *sometimes*, but `tableRows()` (parse.go:373) ignores spans entirely — it collects one string per `<td>`/`<th>`. A 2-col-spanned header becomes one cell; a vertically merged label disappears from continuation rows. | `23501` has 54 `w:gridSpan`; `29510` has 24 `w:vMerge` + 9 384 `PL`. The grid is in the source but `colspan`/`rowspan` are dropped on read. |
| **Reading order / figures** | HTML export crashes on embedded EMF/WMF (`convert.sh` strips them, tags `3GPP-MCP-DEGRADED`). Degraded files silently lose figure-adjacent context. | `convert.sh` attempt-2 path; `.degraded.tsv`. |
| **Heading robustness** | Headings arrive as `<h1>..<h6>` with text `"<clause>\t<title>"`; `classifyHeading` regex-splits. Heading7-9 collapse to `<h6>` (HTML has no `<h7>`), and the deep 3GPP custom `H6` style (outline level 9) is indistinguishable. | OOXML keeps `Heading1..Heading9` distinctly (styles.xml). |
| **ASN.1 blocks** | No detection at all — ASN.1/HTTP-method lines fold into surrounding `<p>` text, untyped. | `PL` style carries them verbatim (see §4.3). |
| **Determinism** | Output depends on the installed LibreOffice version's HTML filter (whitespace, entity, anchor differences) → the "stable hash" reproducibility goal (CLAUDE.md §1) is weakened. | LO filter is not version-pinned. |

The HTML approach is *adequate for body text + BM25* but structurally cannot
satisfy "tables are high-value citation targets" (the Change-History annex and
29.5xx IE tables) without merged-cell geometry.

---

## 2. The `.docx` is directly machine-readable — proof from the corpus

A `.docx` is a ZIP of XML parts. The only part we need is `word/document.xml`
(styles live in `word/styles.xml`). Real parts in `23501-ic0.docx`:

```
word/document.xml   18 MB   ← body: paragraphs, runs, tables
word/styles.xml             ← styleId → name/basedOn/outlineLvl
word/numbering.xml          ← (not needed; clause numbers are literal runs)
word/header*.xml footer*.xml media/ embeddings/ ...
```

### 2.1 Headings: clause number and title are separate runs

Real `Heading3` paragraph from `23501-ic0` (linearized):

```xml
<w:p>
  <w:pPr><w:pStyle w:val="Heading3"/></w:pPr>
  <w:bookmarkStart w:id="81" w:name="_Toc20149631"/>      <!-- TOC anchor -->
  <w:r><w:t>4.2.1</w:t></w:r>                              <!-- clause number -->
  <w:r><w:tab/><w:t>General</w:t></w:r>                    <!-- TAB then title -->
  <w:bookmarkEnd w:id="81"/>
</w:p>
```

So `clause_path` = first run's text, `heading` = text after the `<w:tab/>`. This
is *more* reliable than the HTML `"4.2.1\tGeneral"` heuristic because the level
is explicit in `pStyle` (`Heading3` → depth 3) rather than inferred from `<h3>`.

### 2.2 Heading style inventory (from `styles.xml`)

3GPP's Word template defines:

- `Heading1` … `Heading9` (standard, `w:outlineLvl 0..8`) → clause depth 1–9.
- `H6` is a **custom** style `basedOn="Heading5"` with `w:outlineLvl=9` — a
  pseudo-level-6+ used for deep clauses. Must be mapped explicitly (HTML can't).
- Table paragraph styles: `TAH` (table heading, `basedOn=TAC`), `TAC` (centred),
  `TAL` (left), `TAN`, `TAR`, `TF` (footnote), `TH` — these mark *cell text*,
  not structure; structure comes from `w:tbl`/`w:tr`/`w:tc`.
- `PL` — **ASN.1 / protocol literal** block (monospace), `customStyle=1`.

A `headingDepth(styleID) int` lookup table encodes all of this (returns 0 for
non-heading styles). This is the single source of truth that HTML lacks.

---

## 3. Library choice — **thin custom `encoding/xml` parser** (no new dep)

I evaluated every actively-maintained Go docx library against three hard
constraints from CLAUDE.md: **(a)** distributable mono-binary (§1 "binaire
statique distribuable par scp"), **(b)** no new dependency without justification
(§10), **(c)** we only **read** and need `pStyle` + merge geometry.

| Library | License | Read API for our needs | Verdict |
|---|---|---|---|
| **`unidoc/unioffice`** | **Commercial** — requires a metered license **key at runtime** | Full, but key check defeats offline mono-binary | ❌ Disqualified: breaks "scp a static binary, no runtime to install". |
| **`fumiama/go-docx`** | **AGPL-3.0** | Reads paragraphs/tables; `WTableCellProperties{GridSpan, VMerge}` exist | ❌ AGPL is viral for a distributed binary; legal risk for a tool shipped to engineers. |
| **`mmonterroca/docxgo`** | MIT, pure Go, v2.4.0 (Apr 2026) | "Round-trip preservation of `w:gridSpan`/`w:vMerge`" is a **write/preserve** feature; pkg.go.dev shows **no read-side getter** for GridSpan/VMerge and **no `StyleID()`** on `domain.Paragraph`. "Complex tables in existing docs" listed as 🚧 in-development. | ❌ Can't *read* the two properties we need; heavy DDD object graph (slow on an 18 MB body, builds full tree). |
| **`gomutex/godocx`** | MIT | v0.1.4 (Jul 2024), read of merged-cell props undocumented/unconfirmed | ❌ Immature; same read-getter gap risk. |
| **`unidoc/unioffice` forks / `srinathh/gooxml`** | Apache-2.0 but **write-only / unmaintained** | No robust read path | ❌ |
| **Custom `encoding/xml`** | stdlib (already used by goal of CLAUDE.md §2 "encoding/xml + archive/zip") | We define exactly the structs we read | ✅ **Chosen.** |

**Decision: a ~300-line custom parser using `archive/zip` + `encoding/xml`**,
which is *precisely the stack CLAUDE.md §2 prescribed for DOCX parsing*
("Doc parsing : encoding/xml + archive/zip — parsing natif"). Rationale:

- **The grammar we need is tiny.** We consume ~10 element names: `w:body`,
  `w:p`, `w:pPr/w:pStyle`, `w:r`, `w:t`, `w:tab`, `w:tbl`, `w:tblGrid/w:gridCol`,
  `w:tr`, `w:tc`, `w:tcPr/w:gridSpan`, `w:tcPr/w:vMerge`. A general library
  models hundreds of elements we never touch.
- **No license/dep risk**, no CGO (the query binary stays pure-Go; the existing
  CGO surface is DuckDB only). Satisfies §10 "no dep without justification".
- **Streaming = bounded memory.** `xml.Decoder.Token()` streams the 18 MB body
  without building a DOM (the DDD libs allocate a full object tree → GC pressure
  ×N specs). We emit a `Clause` as soon as the next heading arrives — same
  flush model as today's walker.
- **We control reconstruction.** `gridSpan` expansion and `vMerge` fill are
  domain logic we want to own and unit-test against known 3GPP tables, not
  hope a third party implemented per the ECMA-376 default rules.

Tradeoff accepted: we own ~300 lines of XML plumbing (vs `import`). Given the
narrow grammar and the test corpus on disk, this is cheaper over the project's
life than tracking a third-party lib's read-API maturity and license.

---

## 4. Parse model (`internal/ooxml`)

Mirror the existing `htmlparse.ParsedSpec` so `ingest` is untouched downstream:

```go
package ooxml

type ParsedSpec struct {
    Spec     model.Spec
    Version  model.SpecVersion
    Clauses  []model.Clause
    Changes  []model.Change
    Degraded bool // a part failed to parse (e.g. embedded media); text still emitted
}

func ParseFile(path string) (*ParsedSpec, error) // path = .../<Rel>/<num>-<code>.docx
```

`metaFromFilename` is reused **verbatim** from htmlparse (filename → spec_id,
release, version via `model.DecodeVersionCode`) — only the extension changes
(`.html` → `.docx`). The regex `^([0-9]{5})-([0-9a-z]{3})(?:_.*)?$` is identical.

### 4.1 Streaming walk

```
open zip → find "word/document.xml" → xml.NewDecoder(rc)
state: cur *Clause, buf strings.Builder, informativeAnnex bool, inChangeHistory bool
for tok := dec.Token():
  StartElement "p":   read pPr/pStyle → style; gather child runs' <w:t> joined; first <w:tab/>
                      splits number|title when style is a heading
    if headingDepth(style) > 0:   flush(cur); start new Clause{ClausePath,Heading,depth}
    elif style == "PL":           append run text to buf, tagged as ASN.1 (see 4.3)
    else:                         append run text to buf
  StartElement "tbl": parse table (4.2) → serialize to buf as TSV rows (searchable);
                      if inChangeHistory: keep structured grid for parseChangeTable
flush(cur)
```

Heading-number/title split reuses the same regexes (`reNumeric`, `reAnnex`,
`reAnnexSub`) — but now applied to *clean run text*, not tab-joined HTML, so
they fire more reliably. `is_normative` annex tracking is ported as-is.

### 4.2 Tables with `gridSpan` (horizontal) + `vMerge` (vertical)

**Column model** comes from `<w:tblGrid>` (real, `23501`):

```xml
<w:tblGrid><w:gridCol w:w="4883"/><w:gridCol w:w="5540"/></w:tblGrid>
```

→ 2 logical columns. Each `<w:tc>` occupies `gridSpan` (default 1) consecutive
columns; `vMerge` ties a cell to the one above.

**Real `gridSpan` cell (`23501`):**

```xml
<w:tc>
  <w:tcPr>
    <w:tcW w:w="10423" w:type="dxa"/>
    <w:gridSpan w:val="2"/>            <!-- this cell spans 2 grid columns -->
    <w:shd w:val="clear" w:color="auto" w:fill="auto"/>
  </w:tcPr>
  <w:p><w:pPr><w:pStyle w:val="ZA"/></w:pPr> ... </w:p>
</w:tc>
```

**Real `vMerge` pair (`29510`):**

```xml
<!-- row N: the master cell that starts the vertical span -->
<w:tc>
  <w:tcPr>
    <w:tcW w:w="827" w:type="pct"/>
    <w:vMerge w:val="restart"/>        <!-- begins a vertical merge in this column -->
  </w:tcPr>
  <w:p>...content lives only here...</w:p>
</w:tc>

<!-- row N+1, same column: continuation (note bare element = default "continue") -->
<w:tc>
  <w:tcPr>
    <w:tcW w:w="827" w:type="pct"/>
    <w:vMerge/>                        <!-- no val ⇒ "continue": inherit the cell above -->
  </w:tcPr>
  <w:p/>                               <!-- empty: real text is in the restart cell -->
</w:tc>
```

**Reconstruction algorithm** (per ECMA-376; confirmed by the snippets above):

```
grid := len(tblGrid.gridCol)               // logical column count
colText[grid]                              // last seen text per column (for vMerge fill)
for each <w:tr>:
    col := 0
    out  := make([]string, grid)
    for each <w:tc>:
        span := gridSpan (default 1)
        switch vMerge:
          case "restart": colText[col] = cellText; place cellText at [col..col+span)
          case "continue"/bare: place colText[col] (inherited) at [col..col+span)
          default (absent): colText[col] = cellText; place cellText at [col..col+span)
        col += span
    emit row `out` (length == grid, every logical column filled)
```

Result: a **rectangular grid** where every `(row,col)` is populated — merged
labels propagate down, spanned headers occupy all their columns. This is what
HTML loses. Serialized to `buf` as `\t`-joined rows for BM25; the structured
grid is *also* handed to change-history parsing.

> Corpus frequency (12-spec sample + targeted scan): `gridSpan` appears in
> **every** spec with tables (7–2113 occurrences; e.g. `23008` = 2113);
> `vMerge` concentrates in CT/RAN specs (`38870`=2900, `38174`=2752, `29562`=46);
> `PL` concentrates in protocol specs (`29510`=9384, `29337`=5430). So gridSpan
> handling is mandatory for *all* specs; vMerge/PL matter for the CT/RAN subset.

### 4.3 ASN.1 (and HTTP-method) blocks via `pStyle="PL"`

Real `PL` paragraph (`29510`):

```xml
<w:p>
  <w:pPr><w:pStyle w:val="PL"/></w:pPr>
  <w:r><w:t>PATCH .../nf-instances/4947a69a-f61b-4bc1-b9da-47c9c5d14b64</w:t></w:r>
</w:p>
```

Contiguous `PL` paragraphs form one verbatim block. We **preserve whitespace and
line breaks** (no `collapse()`), concatenate the run text, and tag the span so a
future `chunk_kind='asn1'` (Axis 6) can be set. In V1 the text is appended to the
clause buffer *unmodified* (HTML currently squashes it). No schema change is
required for V1 — the `clauses.text` column carries it verbatim; a later MR can
add a `chunk_kind` column.

### 4.4 Change History annex

The annex heading "Change history" is detected on `pStyle ∈ Heading*` with text
== "Change history" (same `isChangeHistory` test). The **following table is parsed
with the §4.2 grid reconstruction**, then mapped by header keyword.

Real header + first data row (`23501`), proving the column mapping is intact:

```
header : Date | Meeting | TDoc | CR | Rev | Cat | Subject/Comment | New version
row    : 06-2017 | SP#76 | SP-170384 | - | - | - | MCC Editorial Update ... | 1.0.0
```

`parseChangeTable` in `htmlparse/parse.go:299` already maps exactly these headers
(`date`, `meeting`, `tdoc`, `cr`, `rev`, `cat`, `subject`, `new`). **It is reused
unchanged** — it takes `[][]string`, which our reconstruction produces. The only
improvement: rows with merged/spanned cells (common in older annexes) now align
to columns correctly, fixing CR rows that HTML mis-columned. Freeze-date proxy
(latest date in the annex) logic ports verbatim.

---

## 5. How it replaces / augments `internal/htmlparse`

| Concern | Today | After |
|---|---|---|
| Package | `internal/htmlparse` (`golang.org/x/net/html`) | `internal/ooxml` (`archive/zip` + `encoding/xml`, stdlib) |
| Input glob | `data/sources/convert/*/*.html` | `data/sources/convert/*/*.docx` (all docx; legacy .doc converted to .docx) |
| `ingest.Run` | `htmlparse.ParseFile(j.path)` + `*.html` glob | `ooxml.ParseFile(j.path)` + `*.docx` glob — **2-line diff** |
| `ParsedSpec` shape | `{Spec, Version, Clauses, Changes, Degraded}` | identical (drop-in) |
| `parseChangeTable`, `classifyHeading`, `metaFromFilename`, date/version helpers | in htmlparse | moved to `internal/ooxml` (or a shared `internal/specmeta`) — pure functions, no HTML dep |
| `golang.org/x/net` dependency | required by htmlparse | still required (search/li use it? check) — **not removed by this axis**; if unused elsewhere it can drop later |
| `internal/docx` (empty marker) | "superseded by htmlparse" doc | becomes the real home, or `internal/ooxml` supersedes the marker |

**Migration is additive then switch-over**, never a destructive delete (CLAUDE.md
§8 "move content, never delete logic"): `htmlparse` stays until `ooxml` passes the
same tests + a golden-file diff, then `ingest` flips the import and `htmlparse` is
removed in a separate commit.

---

## 6. `.doc → .docx` conversion step in `scripts/corpus.sh`

Only the **conversion target** changes: `html` → `docx`. The download,
enumeration, flock, release-floor, and on-the-fly worker logic are untouched.

### 6.1 `scripts/lib/convert.sh`

`_soffice_html` → add a sibling `_soffice_docx` (or parametrize the filter):

```bash
# was: --convert-to html
timeout -k "$CONV_KILL" "$CONV_TIMEOUT" soffice --headless --norestore \
  -env:UserInstallation="file://$prof" \
  --convert-to docx:"MS Word 2007 XML" --outdir "$outdir" "$doc"
```

Key consequences:
- **Native `.docx` need NO conversion** — they are copied/linked straight into
  `convert/<Rel>/` (or parsed in place from `origin/`), saving ~45% of soffice
  runs and avoiding a lossy LO re-export of already-clean OOXML.
- **Legacy `.doc` (~55%) convert to `.docx`**, preserving `w:tbl`/`w:gridSpan`/
  `w:vMerge`/`pStyle` — the structures the HTML filter destroyed.
- The **EMF/WMF crash** (`33.501` Signal 6 in `MetafilePrimitive2D`) is a
  *rasterisation-for-HTML* bug. The `docx` filter does **not** rasterise vector
  metafiles, so the crash should not occur on the docx path. The EMF-strip
  fallback is **kept as a safety net** but is expected to fire far less; degraded
  tagging (`3GPP-MCP-DEGRADED`, `.degraded.tsv`) is retained.
- The `process_spec` `find` already matches `*.doc -o *.docx`; only the target
  extension and the "native docx passes through" branch change.

`scripts/research/04-ooxml-probe.sh` (delivered alongside this doc) reproduces
the corpus probes: it extracts `word/document.xml` from a sample spec and counts
`gridSpan`/`vMerge`/`PL`/`tbl` so the frequency claims here are re-verifiable.

---

## 7. Fidelity comparison: HTML vs direct OOXML

| Dimension | LibreOffice→HTML (current) | Direct OOXML (proposed) |
|---|---|---|
| Horizontal merges (`gridSpan`) | Lost (`<td>` flattened, spans ignored) | **Preserved** — span expanded to N logical columns |
| Vertical merges (`vMerge`) | Lost (continuation rows drop the label) | **Preserved** — `restart` value filled down `continue` rows |
| Heading depth | `<h1>..<h6>` only; 7–9 + custom `H6` collapse | **Full `Heading1..9` + `H6`/outlineLvl** |
| Clause number vs title | tab-split heuristic on rendered text | **Separate runs**, explicit |
| ASN.1 / protocol blocks | folded into prose, untyped | **`pStyle=PL` detected**, verbatim, taggable |
| Determinism / reproducible hash | depends on LO HTML-filter version | **deterministic** (we own the parse; only `.doc→.docx` uses LO) |
| Figures (EMF/WMF) | crash → strip → degraded | out of scope either way; **no crash on docx filter** |
| Native `.docx` | needlessly re-exported through LO (lossy) | **parsed as-is** (zero LO round-trip) |
| Memory | HTML DOM (`html.Parse` builds full tree) | **streaming** `xml.Token()`, bounded |
| Body text / BM25 quality | good | **equal-or-better** (cleaner whitespace, no LO artefacts) |

Net: identical or better for everything htmlparse does, plus the structured
table + ASN.1 capabilities htmlparse structurally cannot provide.

---

## 8. Step-by-step migration plan

1. **Add `internal/ooxml`** (new package, no consumer yet): zip+xml reader,
   `headingDepth` table, run/paragraph extraction, table grid reconstruction
   (`gridSpan`+`vMerge`), `PL` block detection, `ParsedSpec`. Reuse htmlparse's
   pure helpers (`classifyHeading`, `parseChangeTable`, `metaFromFilename`,
   `parseDate`, version helpers) by moving them to a shared spot.
2. **Golden-file tests**: commit 3 trimmed fixtures under `internal/ooxml/testdata/`
   — an IE table with `gridSpan` (from 23.501), a `vMerge` table (29.510), and a
   Change-History annex. Assert reconstructed grid is rectangular and CR rows map
   to the right columns. Add a `PL` ASN.1 fixture asserting verbatim preservation.
3. **`scripts/lib/convert.sh`**: add `_soffice_docx`; native `.docx` pass-through;
   keep EMF-strip net. Re-run `corpus.sh --no-download` on a small `--series 29`
   slice to regenerate `convert/` as `.docx`.
4. **Switch `internal/ingest`**: glob `*.docx`, call `ooxml.ParseFile`. (2-line
   diff + import.) Run a full deterministic rebuild; compare clause/Change counts
   to the HTML baseline (expect ≥ counts, with previously-merged CR rows now split
   correctly).
5. **Verify** with existing `internal/ingest` + `internal/store` tests and a
   spot-check of `get_changelog` / `search_spec` citations on 23.501 & 29.510.
6. **Remove `internal/htmlparse`** in a separate commit once parity is proven;
   drop `golang.org/x/net` from `go.mod` *iff* no other package imports it.
7. **Docs/CLAUDE.md**: update §2/§13 wording back to DOCX-direct under an
   `arch-change`-labelled MR; note LO is used only for `.doc→.docx`.

---

## 9. Risks & mitigations

| Risk | Likelihood | Mitigation |
|---|---|---|
| **No-CGO / build**: parser must stay pure-Go | n/a (stdlib only) | `archive/zip`+`encoding/xml` are stdlib; no new CGO. Query binary CGO surface stays DuckDB-only. |
| **Lib maturity** (if we'd picked a 3rd-party) | — | Avoided entirely by the custom parser; the maturity/license table (§3) documents *why* each lib was rejected. |
| **`.doc→.docx` LO fidelity**: LO mis-converts an exotic legacy table | Low-Med | LO's binary-Word→OOXML filter is far more mature than its HTML filter (round-trips structure rather than rendering). Golden tests on converted `.doc` samples; degraded tagging catches failures. |
| **Malformed/huge `document.xml`** (23.501 = 18 MB) | Med | Streaming decoder = bounded memory; per-token; never builds a DOM. Wrap parse in a recover-free error path; on partial failure emit text + set `Degraded=true` (parity with HTML degraded behaviour). |
| **Namespace prefixes** (`w:` vs default ns) | Low | Match on `xml.Name.Local` (`p`,`tbl`,`tc`,`gridSpan`,`vMerge`,`pStyle`), ignore prefix — robust to LO's namespace declarations. |
| **`vMerge` default = "continue"** mis-handled | Low | Explicitly treat bare `<w:vMerge/>` as continue (confirmed in 29.510 snippet & ECMA-376). Unit-tested. |
| **Multi-part specs** (`36213-j30_s10-s13`) | Low | Filename regex already handles the `_<section>` suffix; parts merge by (spec,version) exactly as today. |
| **Sub-document parts** (header/footer carrying clause text) | Very Low | 3GPP body text lives in `document.xml`; headers/footers are page furniture. Parse only `document.xml`. |
| **Style-name drift across old templates** | Low-Med | `headingDepth` keyed on both standard `HeadingN` and known custom (`H6`); fall back to `w:outlineLvl` in `pPr` when an unknown style sets one. |

---

## 10. Sources

Ground-truth probes (local corpus, this repo):
- `data/sources/origin/Rel-18/23501-ic0.zip` → `word/document.xml` (gridSpan, Heading*, Change-history).
- `data/sources/origin/Rel-18/29510-ib0.zip` → `word/document.xml` (vMerge restart/continue, `PL` ASN.1).
- 12-spec frequency sample (23.x/29.x/38.x series).

OOXML / WordprocessingML references:
- ECMA-376 `gridSpan`: https://c-rex.net/samples/ooxml/e1/part4/OOXML_P4_DOCX_gridSpan_topic_ID0EMEPQ.html
- ECMA-376 `ST_Merge` / `vMerge` (restart vs continue default): https://c-rex.net/samples/ooxml/e1/Part4/OOXML_P4_DOCX_ST_Merge_topic_ID0EXYA3.html
- OOXML primer, vertically merged cells: https://c-rex.net/samples/ooxml/e1/Part3/OOXML_P3_Primer_Vertically_topic_ID0E2HBG.html
- `w:CT_VMerge`: http://www.datypic.com/sc/ooxml/t-w_CT_VMerge.html
- MS OpenXML `VerticalMerge`: https://learn.microsoft.com/en-us/dotnet/api/documentformat.openxml.wordprocessing.verticalmerge

Go library evaluation:
- `unidoc/unioffice` (commercial, license key): https://github.com/unidoc/unioffice
- `fumiama/go-docx` (AGPL-3.0): https://github.com/fumiama/go-docx and https://pkg.go.dev/github.com/fumiama/go-docx
- `mmonterroca/docxgo` (MIT, write-side gridSpan/vMerge, no read getter): https://github.com/mmonterroca/docxgo and https://pkg.go.dev/github.com/mmonterroca/docxgo
- `gomutex/godocx` (MIT, v0.1.4): https://github.com/gomutex/godocx
- `srinathh/gooxml` (write-oriented): https://github.com/srinathh/gooxml
- DOCX merged-cell reconstruction write-up: https://www.llamaindex.ai/blog/improving-table-parsing-for-word-docx-documents
- python-docx cell-merge analysis (algorithm reference): https://python-docx.readthedocs.io/en/latest/dev/analysis/features/table/cell-merge.html
