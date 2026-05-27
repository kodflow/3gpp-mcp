# 3gpp-mcp — Concrete Axes of Improvement (Research)

> Research date: 2026-05-26. Scope: ingestion accuracy, parsing, embeddings/retrieval,
> MCP design, existing telecom tooling, and Lawful Interception specifics.
> Each axis: **what / why it matters here / concrete next step / sources**.

The current architecture (Go 1.25 + CGO, DuckDB FTS+VSS, LibreOffice→HTML ingestion,
~858k clauses, BM25 primary with BGE-M3/ONNX deferred, LI domain focus) is sound. The
findings below are *additive* — they sharpen accuracy, retrieval quality and the LI
vertical without violating the frozen architecture in `CLAUDE.md`.

---

## Axis 1 — Ingest 3GPP machine-readable artefacts alongside DOCX/HTML

**What.** 3GPP publishes large bodies of *structured, machine-readable* content that today
we are re-deriving (lossily) from prose via LibreOffice→HTML:

- **OpenAPI YAML (5GC SBA)** — over 200 OpenAPI files extracted from the annexes of the
  TS 29.5xx series, hosted in a GitLab repo on 3GPP Forge:
  `https://forge.3gpp.org/rep/all/5G_APIs`. Naming is deterministic:
  `TS29510_Nnrf_NFManagement.yaml`, `TS29518_Namf_Communication.yaml`,
  `TS29503_Nudm_SDM.yaml`, etc. — i.e. `TS<spec><service>.yaml`. Branches map to releases
  (e.g. `REL-15` … `REL-18`), 13 branches / 106 tags. These are git-versioned and
  CI-syntax-checked by 3GPP.
- **ASN.1 / XML schema (LI handover)** — TS 33.128 ships its X1/X2/X3 definitions as ASN.1
  modules and XML schema *electronic attachments* (see Axis 6).
- **Status/metadata** — the DynaReport status report
  (`https://www.3gpp.org/DynaReport/status-report.htm`) and `WiVsSpec` / `SpecVsWI` pages
  give spec↔release↔version↔freeze-date↔WG mappings; the CR database is downloadable from
  `https://www.3gpp.org/ftp/Information/Databases/Change_Request/`.

**Why it matters.** These give us *ground-truth structured data* for the very things prose
parsing gets wrong: API operation/IE definitions, data types, cross-references, and the
spec catalogue (`specs` / `spec_versions` tables in our schema). An OpenAPI operation or an
ASN.1 PDU is far more reliable as a citation target than a heading scraped from converted
HTML. It also directly populates `find_cross_references` and `list_specs`/`list_releases`
with authoritative data instead of best-effort regex.

**Concrete next step.**
1. Add a `cmd/ingest` sub-pipeline that clones `5G_APIs` per release branch and parses each
   YAML into a `sba_apis` / `sba_operations` side-table keyed by `(spec_id, release,
   operation_id, path)`, linked back to `clauses` by clause_path where the annex anchors it.
2. Seed `specs` + `spec_versions` from the DynaReport status report rather than from DOCX
   filename heuristics (keeps version-ordering correct — see `CLAUDE.md` §8 piège 3).
3. Treat OpenAPI/ASN.1 as a *first-class chunk type* (`chunk_kind ∈ {prose, openapi, asn1}`)
   so `search_spec` can return an exact `operationId` with citation.

**Sources.**
- https://www.3gpp.org/technologies/openapis-for-the-service-based-architecture
- https://forge.3gpp.org/rep/all/5G_APIs
- https://forge.3gpp.org/rep/all/5G_APIs/raw/4e19b028fdfcdb026bc93640bad2064d3e9daa90/README.md
- https://www.3gpp.org/DynaReport/status-report.htm
- https://www.3gpp.org/ftp/Specs/archive/

---

## Axis 2 — Harden 3GPP .doc/.docx parsing (tables, Change History, ASN.1 blocks)

**What.** The hard cases in 3GPP DOCX are (a) merged-cell tables (`w:gridSpan` column-span,
`w:vMerge` row-span with `restart`/`continue`), (b) the Change History annex table at the
end of every spec, and (c) ASN.1 code blocks (paragraph style `PL`). Going via
LibreOffice→HTML flattens cell merges and can mangle reading order; parsing the OOXML
directly preserves the grid.

Relevant building blocks:
- Go DOCX libraries that *model* merged cells: `mmonterroca/docxgo` (v2.3.0, Feb 2026)
  advertises round-trip preservation of `w:gridSpan` and `w:vMerge`; `l0g1n/go-docx` and
  `yangge2333/go-docx` expose `GridSpan`/`VMerge` on cell properties.
- `harshankur/officeParser` (Node) documents practical handling of the same constructs.
- LlamaIndex's write-up on DOCX table parsing covers the reconstruction algorithm for
  merged cells (propagate `restart` content down `continue` rows; expand `gridSpan` into N
  logical columns) — directly portable to Go.

**Why it matters.** Tables in 23.501/29.5xx (IE tables, parameter tables) and the Change
History annex are *high-value citation targets*. The Change History annex is the cheapest
path to populating the `changes` table (`CLAUDE.md` §6 step 2) without the full CR pipeline,
and it is exactly where merged cells break naive HTML scraping. ASN.1 block detection lets
us tag `chunk_kind='asn1'` for precise LI/protocol retrieval (Axis 6).

**Concrete next step.**
1. For binary `.doc`, keep `soffice` but convert to **`.docx` (OOXML)**, not HTML, then
   parse `word/document.xml` directly — preserves `w:tbl` structure and `w:pStyle`.
2. Implement a merged-cell normaliser (expand `gridSpan`, fill `vMerge=continue` from the
   `restart` cell) so each logical cell maps to one (row,col) — write a focused unit test
   against a known 29.5xx IE table and a Change History annex.
3. Detect ASN.1 blocks by `w:pStyle="PL"` and contiguous monospace runs; store verbatim.

**Sources.**
- https://github.com/mmonterroca/docxgo
- https://pkg.go.dev/github.com/l0g1n/go-docx
- https://www.llamaindex.ai/blog/improving-table-parsing-for-word-docx-documents
- https://python-docx.readthedocs.io/en/latest/dev/analysis/features/table/cell-merge.html
- https://docx.js.org/api/classes/GridSpan.html

---

## Axis 3 — Embedding & retrieval quality for telecom text

**What.** Several concrete upgrades over the current "BM25 primary, BGE-M3 deferred" stance:

- **Model choice.** **Qwen3-Embedding-0.6B** (released Jun 2025, arXiv 2506.05176) is a
  strong CPU/ONNX-friendly alternative: 1024-dim output, **32K context**, Matryoshka
  (truncate to 256/512 to shrink the HNSW index), MTEB-multilingual ≈ 64.33 vs the 8B
  variant's #1 70.58. BGE-M3 remains attractive because it emits **dense + sparse (lexical)
  + ColBERT** in one pass — the sparse vector can *feed BM25-style scoring* and the dense
  vector the HNSW path, from a single model. Either is a defensible V1 default; Qwen3-0.6B
  wins on context length and MRL flexibility, BGE-M3 wins on built-in hybrid output.
- **Hybrid fusion.** Our planned RRF k=60 is the right call: Cormack et al. (TREC 2009)
  found the optimum flat across k∈[20,100], and RRF fuses *ranks* so BM25 tf-idf magnitudes
  need no calibration against cosine. Keep k=60; don't over-tune.
- **Reranker.** A cross-encoder (`bge-reranker-v2-m3`, max_len 512) on the top-20 → top-5
  gives the biggest precision win for technical queries, at ~80 ms/sidecar cost. This is
  already flagged "optional V2" in `CLAUDE.md`; the research confirms it's the highest-ROI
  V2 retrieval item.
- **Pattern.** retrieve broad (top-20 per backend) → RRF → rerank → return top-5.

**Why it matters.** Telecom prose is dense with near-duplicate clauses across releases and
acronym collisions (AMF 5GC vs IMS — `CLAUDE.md` §8 piège 5). Pure BM25 retrieves the right
*spec* but often the wrong *release/clause variant*; a reranker disambiguates. BGE-M3 sparse
output also lets us get hybrid behaviour without maintaining a separate sparse index.

**Concrete next step.**
1. Benchmark BGE-M3 vs Qwen3-Embedding-0.6B on a 30–50 query LI/5GC eval set (clause-hit@5).
   Decide the V1 default from data, not priors.
2. If BGE-M3: wire its sparse vector into the lexical path so one model serves both arms.
3. Implement RRF k=60 fusion now; gate the cross-encoder reranker behind a `mode` flag.

**Sources.**
- https://arxiv.org/abs/2506.05176 (Qwen3 Embedding)
- https://qwenlm.github.io/blog/qwen3-embedding/
- https://huggingface.co/BAAI/bge-m3
- https://blog.serghei.pl/posts/reciprocal-rank-fusion-explained/
- https://bigdataboutique.com/blog/reciprocal-rank-fusion-how-it-works-and-when-to-use-it
- https://huggingface.co/spaces/mteb/leaderboard

---

## Axis 4 — DuckDB VSS / HNSW operational limits (act before scale bites)

**What.** DuckDB VSS HNSW has hard constraints that directly threaten our "local-first,
single-file, ~858k→10M chunks" plan:

- HNSW index **only creatable in in-memory DBs** unless
  `SET hnsw_enable_experimental_persistence = true`.
- **WAL recovery is not implemented for custom indexes** → an unclean shutdown with
  uncommitted writes to an HNSW-indexed table can **corrupt the index / lose data**. DuckDB
  explicitly recommends in-memory only for HNSW.
- The index **must fit entirely in RAM**, and its memory is allocated **outside**
  DuckDB's `memory_limit`.
- **No incremental persistence**: every checkpoint reserialises the whole index; deletes are
  only *marked* (index goes stale) until `PRAGMA hnsw_compact_index(...)` or a rebuild.

**Why it matters.** This is a correctness/repro risk against `CLAUDE.md` §1 ("DB
déterministe, hash stable") and the local-first promise. At 10M × 1024-float vectors the
in-RAM index is large; an unclean shutdown corrupting it would silently degrade retrieval —
unacceptable for a no-hallucination tool.

**Concrete next step.**
1. Adopt a **build-then-freeze** ingestion model: build vectors with HNSW in a controlled
   run, `CHECKPOINT`, then ship a read-only DB; never write to the indexed table at query
   time (we are read-mostly anyway).
2. Make HNSW **rebuildable from a stored `embedding FLOAT[1024]` column** so the index is a
   derived artefact, not the source of truth — keeps the determinism guarantee.
3. Consider Matryoshka-truncated dims (256/512) to cut RAM; measure recall trade-off.
4. Add an ingestion post-step that runs `hnsw_compact_index` and verifies index integrity.

**Sources.**
- https://duckdb.org/docs/current/core_extensions/vss
- https://duckdb.org/2024/05/03/vector-similarity-search-vss
- https://github.com/duckdb/duckdb-vss

---

## Axis 5 — MCP server design (tool surface, citations, large results)

**What.** Best-practice guidance maps cleanly onto our 8-tool surface:

- **Responses live in the model's context window** — avoid walls of low-signal text; return
  small structured JSON / Markdown fragments. Our `citations: [{spec_id, release, version,
  clause, url}]` block is exactly the recommended shape.
- **Pagination/cursors for browsing** large result sets — relevant to `search_spec` (top_k)
  and `get_changelog` which can return many CRs.
- **Resources vs Tools.** Tools = actions with schemas/side-effects; **Resources = readable
  data the client can fetch into context**. A full spec or clause body is a *resource*, not a
  tool payload — exposing `get_spec`/large clause text as an MCP **resource URI** (e.g.
  `3gpp://23.501/Rel-18/5.2.3.1`) lets the client pull it on demand instead of inflating
  every tool response.
- Validate all tool inputs with JSON Schema; one server = one clear purpose (we comply).

**Why it matters.** Our core risk is returning *too much* (whole clauses) and blowing the
context budget, or *too little* (no exact citation). Splitting "find" (tool, returns
citations + snippets) from "fetch full text" (resource) is the idiomatic MCP way to give
Claude exact provenance without dumping megabytes.

**Concrete next step.**
1. Keep `search_spec`/`get_changelog`/`resolve_term` as tools returning **citations +
   short snippets + a resource URI**; expose full clause/spec bodies as **MCP resources**.
2. Add an opaque `cursor` to `search_spec` and `get_changelog` for paging beyond top_k.
3. Enforce a per-response size budget; truncate snippet text with an explicit
   `truncated: true` + resource URI to fetch the rest.

**Sources.**
- https://modelcontextprotocol.io/specification/2025-06-18/server/tools
- https://github.com/cyanheads/model-context-protocol-resources/blob/main/guides/mcp-server-development-guide.md
- https://nearform.com/digital-community/implementing-model-context-protocol-mcp-tips-tricks-and-pitfalls/
- https://modelcontextprotocol.info/docs/best-practices/

---

## Axis 6 — Lawful Interception: rigorous IRI/CC event enumeration

**What.** The LI spec division of labour is clean and should drive our LI data model:

| Spec | Role |
|---|---|
| **TS 33.126** | LI **requirements** |
| **TS 33.127** | LI **architecture** — POI, MDF (MDF2/MDF3), ADMF, LICF, LIPF, X1/X2/X3 |
| **TS 33.128** | **Protocols & procedures (5G)** — defines the concrete **IRI events / CC PDUs**, ships **ASN.1 module + XML schema** as electronic attachments |
| **TS 33.108 / 33.107** | **Legacy** 2G/3G/4G LI (HI2/HI3, EPS/IMS) |
| **ETSI TS 102 232-x** | **Handover** (HI2/HI3 transport, buffering/encoding); 5G **reuses 102 232-7** so 3GPP focuses on the network side |
| **ETSI TS 103 221-1/-2** | **Internal** interfaces X1 (admin) and X2/X3 (IRI/CC) framing |

Interface semantics: **X1** = provisioning (ADMF→NF), **X2** = IRI events, **X3** = CC.
TS 33.128 IRI events are concrete records emitted by per-NF IRI-POIs, e.g. AMF emits
`AMFRegistration` on successful 5GS registration; SMF emits PDU-session events; MDF2/MDF3
produce HI2/HI3 toward the LEMF. Cross-references into **TS 29.002 (MAP)** and
**TS 29.228 (Cx)** matter for legacy/IMS event provenance.

**Why it matters.** "Enumerate IRI events rigorously" requires the *authoritative list*,
which lives in the **ASN.1/XML schema of 33.128**, not in prose. Parsing that schema yields
a complete, citable event catalogue (event name → fields → originating NF → release added).
This is the single highest-leverage move for the LI vertical and turns the product from
"search LI prose" into "authoritative LI event catalogue with exact citations."

**Concrete next step.**
1. Fetch the TS 33.128 ASN.1 module + XML schema attachments and parse them into an
   `li_events` table: `(event_name, originating_nf, interface ∈ {X2,X3}, fields[],
   first_release, spec_clause, asn1_type)`. Reuse an existing ASN.1 parser (Axis 5 tooling:
   `proj3rd/asn3rd`, `eerimoq/asn1tools` already test against 3GPP modules).
2. Build an explicit mapping table `li_event ↔ {33.127 architecture clause, 33.108 legacy
   equivalent, 29.002/29.228 reference}` so `trace_evolution`/`find_cross_references` can
   answer "what is the 5G equivalent of this EPS IRI event".
3. Make the LI catalogue release-aware (Rel-15→Rel-18 deltas), matching the product's
   "baseline release + annex of later additions" design.

**Sources.**
- https://www.3gpp.org/technologies/li
- https://www.tech-invite.com/3m33/tinv-3gpp-33-126.html
- https://www.etsi.org/deliver/etsi_ts/133100_133199/133128/15.05.00_60/ts_133128v150500p.pdf
- https://www.etsi.org/deliver/etsi_ts/133100_133199/133127/16.04.00_60/ts_133127v160400p.pdf
- https://www.etsi.org/deliver/etsi_ts/102200_102299/10223201/03.34.01_60/ts_10223201v033401p.pdf
- https://www.lawfulinterception.com/explains/5g-and-lawful-interception/
- https://www.lawfulinterception.com/explains/etsi-ts-103-221/

---

## Axis 7 — Reuse existing open-source 3GPP/telecom tooling

**What.** Mature projects we can learn from or vendor instead of reinventing:

- **`proj3rd/asn3rd`** — parses & validates ASN.1 *as found in 3GPP specs* (handles the
  3GPP-specific quirks asn1c chokes on). Ideal for Axis 6 LI ASN.1 and any protocol ASN.1.
- **`OPENAIRINTERFACE/oai-libngapcodec`** — includes tooling to **extract ASN.1 from the
  official 3GPP site**; a working reference for the scrape→extract→codegen flow.
- **`eerimoq/asn1tools`** — ships 3GPP test modules (e.g. RRC); good correctness oracle.
- **`atesgoral/asn1exp`** — expanded ASN.1 parser specifically for **TS 29.002 (MAP)** —
  directly relevant to our MAP cross-referencing.
- **`mitshell/libmich`** — encode/decode for UMTS/LTE RAN + MAP; reference for protocol
  semantics.
- **tech-invite.com** — renders 3GPP specs in a clause-addressable, machine-friendly HTML
  (e.g. `tinv-3gpp-33-126`); useful as a *secondary validation* of our clause hierarchy.

**Why it matters.** Avoids rebuilding ASN.1 parsing from scratch and gives us correctness
oracles + a scrape reference. tech-invite is a cheap way to QA our clause_path extraction
against a known-good rendering without touching their data at query time.

**Concrete next step.**
1. Evaluate `proj3rd/asn3rd` (TypeScript) and `eerimoq/asn1tools` (Python) for an offline
   batch step that converts 3GPP ASN.1 → JSON the Go ingester loads (keeps query path Go-only,
   honouring `CLAUDE.md` §13 — offline batch is permitted, query path stays pure Go).
2. Use tech-invite renderings of a few LI specs as a golden set for parser regression tests.

**Sources.**
- https://github.com/proj3rd/asn3rd
- https://github.com/OPENAIRINTERFACE/oai-libngapcodec
- https://github.com/eerimoq/asn1tools
- https://github.com/atesgoral/asn1exp
- https://github.com/mitshell/libmich
- https://www.tech-invite.com/3m33/tinv-3gpp-33-126.html

---

## Axis 8 — Authoritative spec catalogue & version ordering from DynaReport

**What.** Bootstrap `specs`, `spec_versions`, and spec↔WG mapping from the DynaReport
status report and `WiVsSpec`/`SpecVsWI` pages, and the CR DB download — rather than inferring
release/version from FTP filename codes (the `f=15 … j=19` heuristic in user memory, with its
legacy 00–13 offset caveat).

**Why it matters.** `CLAUDE.md` §8 piège 3 is explicit: versions are **non-monotonic**, so
`(release, version, freeze_date)` is mandatory to order correctly — and freeze_date is *not*
in the filename. DynaReport is the authoritative source for freeze_date and TS/TR
classification (piège 7: TS≠TR, filter TS by default). This makes `list_releases` and the
release-aware "baseline + later additions" answers correct by construction.

**Concrete next step.**
1. Add a `cmd/ingest catalogue` step that parses the DynaReport status report into
   `spec_versions(spec_id, release, version, freeze_date, doc_type, working_group, docx_url)`.
2. Cross-check FTP-derived versions against DynaReport; log discrepancies as ingestion warnings.
3. Pull the CR database dump to seed `changes` with authoritative `(cr_number, spec_id,
   from/to_version, meeting, category, clauses[])` — complements the Change History annex (Axis 2).

**Sources.**
- https://www.3gpp.org/DynaReport/status-report.htm
- https://www.3gpp.org/dynareport
- https://www.3gpp.org/ftp/Information/Databases/Change_Request/
- https://www.3gpp.org/dynareport?code=SpecVsWI--36401.htm

---

## Quick prioritisation

| # | Axis | Effort | Impact | When |
|---|------|--------|--------|------|
| 6 | LI ASN.1/XML → `li_events` catalogue | M | **Very high** (core vertical) | V1 |
| 4 | DuckDB HNSW build-then-freeze + rebuildable index | S | **High** (correctness) | V1 |
| 8 | DynaReport catalogue + version ordering | S | High | V1 |
| 2 | OOXML table / Change-History / ASN.1 parsing | M | High | V1 |
| 1 | OpenAPI/ASN.1 structured ingestion | M | High | V1→V2 |
| 5 | MCP resources + cursors + size budget | S | Medium-High | V1 |
| 3 | Embedding bench (BGE-M3 vs Qwen3) + RRF, reranker V2 | M | Medium-High | V1/V2 |
| 7 | Vendor `asn3rd`/`asn1tools` as offline batch | S | Medium (enabler for #6) | V1 |
