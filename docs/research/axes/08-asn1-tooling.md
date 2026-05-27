# Axis 08 — Reuse Existing ASN.1 Tooling (offline batch → JSON → DuckDB)

> Research date: 2026-05-26. Scope: do **not** write an ASN.1 parser. Convert the
> ASN.1 modules embedded in 3GPP specs into structured JSON during the **offline ingest**
> path only, load that JSON into a DuckDB `asn1_types` table, and keep the query server
> pure‑Go / mono‑binary (`CLAUDE.md` §2, §13).
>
> This axis is the enabler explicitly referenced by Axis 6 (LI IRI/CC event catalogue) and
> Axis 1 (structured artefacts / IE lookups). It does **not** add any runtime dependency.

---

## 0. TL;DR (the decision)

- **Primary tool: `eerimoq/asn1tools` (Python, MIT).** Use its `parse` API to emit a JSON AST
  (`{module → {types → {SEQUENCE/CHOICE → members[]}}}`). It is tested against 3GPP **RRC**
  modules, is MIT‑licensed (vendorable), and its output dict maps almost 1:1 onto our target
  `asn1_types` schema. It handles the **clean ASN.1 dialect** (X.680 type assignments with
  `[n]` tags, `OPTIONAL`, `CHOICE`, `ENUMERATED`, nested SEQUENCE) — which is exactly what
  **TS 33.128** (the LI core vertical) ships.
- **Caveat that drives a second path: `asn1tools` does NOT support `CLASS` (X.681 Information
  Object Classes), parameterization (X.683), `MACRO`, or `ANY DEFINED BY`.** Verified in the
  local corpus, **TS 29.002 (MAP)** uses `OPERATION`/`ERROR` macros and `::= CLASS` — so MAP
  cannot be parsed by `asn1tools` as‑is. For MAP and other macro‑heavy legacy modules, use
  **`proj3rd/asn3rd`** (TypeScript, ANTLR4 grammar built for *the ASN.1 as found in 3GPP
  specs*) or pre‑seeded JSON from **`proj3rd/3gpp-specs-in-json`** as a fallback / regression
  oracle. MAP/legacy is a **V2** concern; **V1 targets TS 33.128 only**, which `asn1tools`
  fully covers.
- **Isolation: a separate offline binary/container step** (`scripts/research/08-asn1-extract.sh`
  + a Python venv that exists *only* in the ingest image) produces `data/generated/asn1/*.json`.
  The Go ingester (`cmd/ingest`) loads that JSON with `encoding/json` and `COPY`s it into DuckDB.
  No Python, no ANTLR, no Node ever reach the query server. This honours `CLAUDE.md` §13
  ("offline batch is permitted, query path stays pure Go").

The single most valuable concrete outcome: parsing the `XIRIEvent ::= CHOICE` of TS 33.128
yields a **complete, citable catalogue of 201 LI IRI/CC events** (event name → originating NF
→ payload type → clause), directly feeding Axis 6 and the `resolve_term` / `find_cross_references`
tools — none of which prose parsing can produce reliably.

---

## 1. Where the ASN.1 actually lives (corpus evidence)

Inspected the local corpus (`data/sources/origin/Rel-19/`) directly. Two distinct packaging
models, which dictate the tool choice:

| Spec | Packaging | ASN.1 dialect | Tool |
|---|---|---|---|
| **TS 33.128** (LI 5G) | **Ships `.asn` files as electronic attachments** inside `33128-j60-attachmens.zip` (`TS33128Payloads.asn`, `TS33128IdentityAssociation.asn`) + `.xsd` X1/X2/X3 schema | Clean X.680: `SEQUENCE`/`CHOICE`/`ENUMERATED`, `[n]` tags, `OPTIONAL`, `EXTENSIBILITY IMPLIED`. **No `CLASS`, no parameterization, no macros.** | **`asn1tools`** ✅ |
| **TS 38.331** (NR RRC) | ASN.1 embedded in DOCX prose (no `.asn` attachment) | Clean X.680 (`AUTOMATIC TAGS`) | `asn1tools` ✅ (tested) or `asn3rd` |
| **TS 29.002** (MAP) | ASN.1 embedded in DOCX prose; ships `SDL` diagram zips, **no `.asn`** | **Macro dialect**: 153× `OPERATION`, 56× `ERROR ::=`, `::= CLASS` (3×), `WITH SYNTAX`, `MACRO` | **`asn3rd`** or pre‑seeded JSON; `asn1tools` will fail |

Verified facts on `TS33128Payloads.asn` (Rel‑19 v6, 9 042 lines):

- **958** top‑level type assignments.
- **`XIRIEvent ::= CHOICE`** with **201 arms**, each an LI event tagged `[n]` and annotated with
  the originating NF + clause in a `--` comment (e.g. `registration [1] AMFRegistration, -- AMF events, see clause 6.2.2.2`).
- Module OID header encodes spec + release: `{… ts33128(19) r19(19) version6(6)}` — a built‑in,
  citable version anchor.

This is why "enumerate IRI events rigorously" (Axis 6) is an **ASN.1 parsing problem, not a prose
problem**: the authoritative list is the CHOICE arms, not scattered headings.

---

## 2. Tool landscape — evaluation

| Tool | Lang / engine | License | 3GPP dialect coverage | JSON/AST output | Maturity | Verdict |
|---|---|---|---|---|---|---|
| **`eerimoq/asn1tools`** | Python (PLY parser) | **MIT** | RRC tested in‑repo (`tests/files/3gpp/rrc_*.asn`); handles TS 33.128 cleanly. **No CLASS/param/MACRO/ANY DEFINED BY.** | `parse` → Python dict (trivially `json.dump`); keys: `types`, `values`, `imports`, `object-classes`, `tags`, `extensibility-implied`; types carry `type` + `members[]` (`name`/`type`/`tag`/`optional`) | ~330★, broad commit history, "under development" but widely used | **Primary (V1).** MIT, clean output, covers the LI core vertical. |
| **`proj3rd/asn3rd`** | TypeScript, **ANTLR4** grammar | check repo (no SPDX surfaced) | Explicitly built for 3GPP RAN ASN.1: NR_RRC, NGAP, XnAP, E1AP, F1AP, LTE_RRC, S1AP, X2AP, UTRA_RRC, RANAP. Tolerates 3GPP quirks `asn1c` rejects. **MAP not listed.** | `parse()` → ANTLR parse tree (`moduleDefinitionsContext`); `extract()` pulls ASN.1 text out of spec text | Active; powers `tool3rd` + `3gpp-specs-in-json` | **Secondary / RAN+macro fallback.** Node toolchain = heavier offline step. |
| **`proj3rd/3gpp-specs-in-json`** | (data, generated by asn3rd) | "non‑commercial" — **not OSI**, do not vendor/redistribute | 36/37/38 series (RAN) as JSON. No 29‑series. | Pre‑rendered JSON AST | Community‑maintained | **Regression oracle only.** Licence forbids shipping; use to cross‑check our output for RAN modules. |
| **`atesgoral/asn1exp`** | JavaScript | MIT | Expanded‑module parser specifically exercised on **TS 29.002 (MAP)** | JS object | Niche | **MAP‑specific option** if we tackle MAP in V2. |
| **`lex-ibm/asn1-tools`** | (IBM fork/utilities) | check repo | General ASN.1; not 3GPP‑specialised | varies | Low | Not preferred; no 3GPP edge. |
| **`openssl dumpasn1` / `dumpasn1`** | C | (Peter Gutmann, free) | **DER value decoder, not a schema parser.** Decodes encoded bytes, cannot read `.asn` type definitions. | byte‑level dump | Mature | **Out of scope** — wrong tool class for our need (we parse schemas, not PDUs). |
| **Go: `chemikadze/asn1go`** | Go (goyacc, X.680 BNF) | check repo | Generic X.680; not validated on 3GPP macro dialect | AST in Go | Low activity | Tempting (pure Go) but **rejected for the parser role** — unproven on 3GPP, and parsing is *offline*, so Go is not required there. Keep Go for the loader only. |
| **`mitshell/libmich`** | Python | GPL | MAP/RAN encode‑decode semantics | n/a (codec) | Reference | Semantics reference only, not a schema→JSON tool. |

**Why not a Go parser even though the project is Go‑first?** The constraint in `CLAUDE.md` is
that the *query server* is pure Go. The ingest path already shells out to `soffice` (LibreOffice)
for `.doc→.docx`/HTML, so a Python/Node ASN.1 step is consistent with the existing offline model
and adds **zero** runtime weight. Writing a 3GPP‑robust ASN.1 parser in Go (handling MAP macros,
information object classes, extension markers) is precisely the multi‑month effort this axis exists
to avoid.

---

## 3. Offline batch pipeline

```
                         OFFLINE INGEST IMAGE (Python venv + asn1tools; optional Node+asn3rd)
                         ─────────────────────────────────────────────────────────────────────
  data/sources/origin/<Rel>/<spec>.zip
            │
            │  (a) extract attachments + DOCX            scripts/research/08-asn1-extract.sh
            ▼
   ┌───────────────────────────┐
   │ ASN.1 BLOCK EXTRACTION     │   .asn attachments → use as‑is (33.128)
   │                           │   DOCX prose       → pull runs with w:pStyle="PL" (Axis 2),
   │                           │                       or asn3rd extract() for RAN/MAP
   └───────────────────────────┘
            │  *.asn (per module)
            ▼
   ┌───────────────────────────┐   clean dialect (33.128, RRC)  → asn1tools parse → dict
   │ PARSE → JSON AST           │   macro dialect (MAP 29.002)   → asn3rd parse  → tree → JSON
   │  (Python / Node, OFFLINE)  │                                  (V2)
   └───────────────────────────┘
            │  data/generated/asn1/<spec>_<rel>_<module>.json   (normalised schema, §4)
            ▼
   ┌───────────────────────────┐
   │ NORMALISE → rows           │   flatten module→type→member into asn1_types rows,
   │  (small Python emitter)    │   attach citation {spec_id, release, version, clause}
   └───────────────────────────┘
            │  data/generated/asn1/asn1_types.ndjson  (or parquet)
            ▼
─────────────────────────── boundary: pure Go from here ───────────────────────────
   ┌───────────────────────────┐
   │ cmd/ingest (Go)            │   encoding/json reads NDJSON → DuckDB COPY into asn1_types
   │  loads, never parses ASN.1 │   (+ derive li_events view for Axis 6)
   └───────────────────────────┘
            │
            ▼
        3gpp.duckdb  (read‑only at query time; query server is pure Go)
```

Determinism (`CLAUDE.md` §1): the extractor is pinned (`asn1tools==0.166.0`), keyed by the
module OID (which carries release+version), and emits sorted JSON → stable hash. The Go side is a
pure loader, so re‑running ingest on the same corpus reproduces the same `asn1_types` rows.

### Isolation guarantees

- The Python/Node toolchain lives **only** in the ingest container/image (or a throwaway venv the
  helper script creates and deletes). It is **not** in `go.mod`, not in `cmd/server`, not in the
  shipped binary.
- The contract between the two worlds is a **JSON/NDJSON file on disk**, not a process call —
  so the offline step can run on a build box and the artefact be shipped; the query host needs
  nothing but the DuckDB file.
- A `.py` in `cmd/`/`internal/` is already forbidden by the project pre‑commit hook (`CLAUDE.md`
  §11); the extractor lives under `scripts/research/`, outside that boundary.

---

## 4. DuckDB schema: `asn1_types`

```sql
-- Structured ASN.1 type catalogue (populated offline; read‑only at query time).
CREATE TABLE asn1_types (
    asn1_id      UBIGINT PRIMARY KEY,    -- surrogate, assigned at load
    spec_id      VARCHAR NOT NULL,       -- '33.128'
    release      VARCHAR NOT NULL,       -- 'Rel-19'
    version      VARCHAR,                -- '19.6.0' (decoded from module OID / filename)
    module       VARCHAR NOT NULL,       -- 'TS33128Payloads'
    type_name    VARCHAR NOT NULL,       -- 'AMFRegistration'
    kind         VARCHAR NOT NULL,       -- 'SEQUENCE'|'CHOICE'|'ENUMERATED'|'SET'|'INTEGER'|...
    members      JSON,                   -- [{name,type,tag,optional,clause}], NULL for leaf types
    parent_type  VARCHAR,                -- enclosing type for nested defs (NULL at top level)
    clause       VARCHAR,                -- spec clause this type is defined under (best‑effort)
    asn1_text    VARCHAR,                -- verbatim ASN.1 block (citation‑exact, chunk_kind='asn1')
    docx_url     VARCHAR,                -- link back to the source spec
    UNIQUE(spec_id, release, module, type_name, parent_type)
);

CREATE INDEX asn1_types_name ON asn1_types(type_name);
CREATE INDEX asn1_types_spec ON asn1_types(spec_id, release);

-- LI event catalogue: a VIEW derived from the XIRIEvent / XCCPayload CHOICEs (Axis 6).
CREATE VIEW li_events AS
SELECT
    spec_id, release, version,
    m.value->>'name'              AS event_name,        -- 'registration'
    m.value->>'type'              AS payload_type,      -- 'AMFRegistration'
    m.value->>'tag'               AS event_tag,         -- '1'
    m.value->>'clause'            AS spec_clause,       -- '6.2.2.2' (from -- comment)
    regexp_extract(m.value->>'type','^[A-Z]+') AS originating_nf  -- 'AMF' heuristic; refine via map table
FROM asn1_types t,
     LATERAL json_each(t.members) AS m
WHERE t.type_name IN ('XIRIEvent','XCCPayload') AND t.kind = 'CHOICE';
```

`members` stays JSON (DuckDB has first‑class `JSON` + `json_each`), so we keep the recursive
SEQUENCE/CHOICE nesting without exploding into N tables, while still letting `search_spec`/
`resolve_term` query individual IEs. `asn1_text` makes every row a **citation‑exact** retrieval
target (`chunk_kind='asn1'`, Axis 2).

---

## 5. How it feeds the product

### 5.1 Axis 1 — LI events (core vertical)
Parsing `XIRIEvent` (X2/IRI) and `XCCPayload` (X3/CC) CHOICEs gives the authoritative
**201‑event** catalogue. `li_events` answers "which NF emits `AMFRegistration` and on what
interface, in which release" with an exact `{spec_id=33.128, release, clause}` citation — no
hallucination, no prose scraping. Release‑awareness comes free: re‑parse each release's module
(OID carries `r19(19) version6(6)`), diff the CHOICE arms → "events added in Rel‑19 vs Rel‑18".

### 5.2 IE lookups (`search_spec`, `resolve_term`, `get_spec`)
`asn1_types` makes every IE (e.g. `FiveGGUTI`, `SUPI`, `Location`) a structured, citable object:
its `kind`, its `members`, the modules that reference it, and verbatim `asn1_text`. `resolve_term`
can resolve an acronym to its ASN.1 definition; `search_spec` can return an exact type with
citation instead of a fuzzy heading.

### 5.3 `find_cross_references`
The module `IMPORTS` block (e.g. 33.128 imports `IPIRIPacketReport` `FROM IPAccessPDU` of ETSI
TS 102 232‑3) is parsed into edges → authoritative cross‑references between specs, far more
reliable than regex over prose.

---

## 6. Concrete example — TS 33.128 `AMFRegistration` → JSON

**Input** (verbatim from `TS33128Payloads.asn`, Rel‑19 v6, abridged):

```asn1
XIRIEvent ::= CHOICE
{
    -- AMF events, see clause 6.2.2.2
    registration        [1] AMFRegistration,
    deregistration      [2] AMFDeregistration,
    ...
}

AMFRegistration ::= SEQUENCE
{
    registrationType    [1] AMFRegistrationType,
    registrationResult  [2] AMFRegistrationResult,
    slice               [3] Slice OPTIONAL,
    sUPI                [4] SUPI,
    sUCI                [5] SUCI OPTIONAL,
    pEI                 [6] PEI OPTIONAL,
    gPSI                [7] GPSI OPTIONAL,
    gUTI                [8] FiveGGUTI,
    location            [9] Location OPTIONAL
    -- … 23 further OPTIONAL fields elided …
}
```

**`asn1tools parse` dict (→ `json.dump`)** — shape verified against the in‑repo RRC
`SPECIFICATION` dict:

```json
{
  "TS33128Payloads": {
    "extensibility-implied": true,
    "tags": "IMPLICIT",
    "imports": { "IPAccessPDU": ["IPIRIPacketReport"] },
    "object-classes": {},
    "values": { "tS33128PayloadsOID": { "type": "RELATIVE-OID", "value": [4,19,19,6] } },
    "types": {
      "XIRIEvent": {
        "type": "CHOICE",
        "members": [
          { "name": "registration",   "type": "AMFRegistration",   "tag": { "number": 1 } },
          { "name": "deregistration", "type": "AMFDeregistration", "tag": { "number": 2 } }
        ]
      },
      "AMFRegistration": {
        "type": "SEQUENCE",
        "members": [
          { "name": "registrationType",   "type": "AMFRegistrationType",   "tag": { "number": 1 } },
          { "name": "registrationResult", "type": "AMFRegistrationResult", "tag": { "number": 2 } },
          { "name": "slice",  "type": "Slice",       "optional": true, "tag": { "number": 3 } },
          { "name": "sUPI",   "type": "SUPI",                          "tag": { "number": 4 } },
          { "name": "sUCI",   "type": "SUCI",        "optional": true, "tag": { "number": 5 } },
          { "name": "gUTI",   "type": "FiveGGUTI",                     "tag": { "number": 8 } },
          { "name": "location","type": "Location",   "optional": true, "tag": { "number": 9 } }
        ]
      }
    }
  }
}
```

**Normalised `asn1_types` rows** (what the Go loader `COPY`s in — clause harvested from the
`-- see clause N` comment on the CHOICE arm):

```json
{"spec_id":"33.128","release":"Rel-19","version":"19.6.0","module":"TS33128Payloads",
 "type_name":"AMFRegistration","kind":"SEQUENCE","clause":"6.2.2.2",
 "members":[
   {"name":"registrationType","type":"AMFRegistrationType","tag":1,"optional":false},
   {"name":"slice","type":"Slice","tag":3,"optional":true},
   {"name":"sUPI","type":"SUPI","tag":4,"optional":false},
   {"name":"gUTI","type":"FiveGGUTI","tag":8,"optional":false}],
 "docx_url":"https://www.3gpp.org/ftp/Specs/archive/33_series/33.128/33128-j60.zip"}
```

**Resulting `li_events` view row:**

```
event_name="registration"  payload_type="AMFRegistration"  event_tag="1"
originating_nf="AMF"        spec_clause="6.2.2.2"           release="Rel-19"
```

---

## 7. Step‑by‑step plan

1. **V1 scope = TS 33.128 only.** It ships clean `.asn` attachments and is the LI core vertical;
   `asn1tools` covers it fully. Defer MAP/RRC‑from‑prose to V2.
2. **`scripts/research/08-asn1-extract.sh`** (offline helper, already drafted):
   unzip spec → unzip `*-attachmens.zip` → collect `*.asn` → create throwaway venv →
   `pip install asn1tools==0.166.0` → `asn1tools parse` each module → write JSON to
   `data/generated/asn1/`. Idempotent; pinned version for determinism.
3. **Normaliser** (small Python in the same script): flatten module→type→member to NDJSON rows,
   harvest the `-- clause N` comment off each `XIRIEvent`/`XCCPayload` arm into `clause`, attach
   `{spec_id, release, version, docx_url}` derived from the module OID + filename.
4. **DuckDB DDL**: add `asn1_types` table + `li_events` view (§4) to the ingest migration.
   (Implementation lands in `internal/store` / `cmd/ingest` under the normal `/do` workflow —
   *out of scope for this research file*.)
5. **Go loader** (`cmd/ingest`): `encoding/json` over the NDJSON → DuckDB `COPY`. No ASN.1 logic
   in Go. (Implementation, not this doc.)
6. **Regression oracle**: for RAN modules (when V2 adds RRC/NGAP), diff our JSON against
   `proj3rd/3gpp-specs-in-json` (read‑only, do not vendor) and tech‑invite renderings.
7. **V2**: add the `asn3rd` (or `asn1exp`) path for MAP/macro modules; extract ASN.1 from DOCX
   prose via the `w:pStyle="PL"` detector (shared with Axis 2).

---

## 8. Risks & mitigations

| Risk | Likelihood | Mitigation |
|---|---|---|
| `asn1tools` chokes on a 3GPP construct (extension markers, nested CHOICE) | Low for 33.128 (verified clean) | Pin version; CI test that parses the shipped `.asn` and asserts ≥201 `XIRIEvent` arms |
| MAP (29.002) uses `CLASS`/`OPERATION`/`MACRO` → `asn1tools` fails | **Confirmed** | Route MAP to `asn3rd`/`asn1exp` in V2; **exclude MAP from V1** |
| `3gpp-specs-in-json` is "non‑commercial", not OSI | Certain | Use as **oracle only**, never ship/redistribute; generate our own JSON |
| Python/Node leaking into runtime | Low | Hard boundary = JSON file on disk; venv is ephemeral & outside `cmd/`/`internal/`; pre‑commit blocks `.py` in source dirs |
| Clause attribution for a type is only in a `--` comment, not structured | Medium | Parse `-- see clause N` comments; fall back to NULL clause + cite module+type (still exact) |
| ASN.1 in DOCX prose (RRC/MAP) needs reliable extraction | Medium (V2) | Reuse Axis 2's `w:pStyle="PL"` detector or `asn3rd extract()`; golden set from tech‑invite |
| Determinism of generated JSON | Low | Pinned tool version + sorted keys + OID‑keyed naming → stable hash (`CLAUDE.md` §1) |
| `asn3rd` Node toolchain weight in offline image | Low | Only pulled in for V2 MAP/RAN; V1 needs Python only |

---

## 9. Sources

- proj3rd/asn3rd — https://github.com/proj3rd/asn3rd
- proj3rd/3gpp-specs-in-json — https://github.com/proj3rd/3gpp-specs-in-json
- eerimoq/asn1tools — https://github.com/eerimoq/asn1tools
- asn1tools docs (parse / unsupported CLASS & parameterization) — https://asn1tools.readthedocs.io/en/latest/
- asn1tools 3GPP RRC test module — https://github.com/eerimoq/asn1tools/blob/master/tests/files/3gpp/rrc_8_6_0.asn
- atesgoral/asn1exp (MAP / TS 29.002) — https://github.com/atesgoral/asn1exp
- chemikadze/asn1go (Go X.680 parser) — https://github.com/chemikadze/asn1go
- dumpasn1 / openssl asn1parse (DER decoders, out of scope) — https://www.cs.auckland.ac.nz/~pgut001/dumpasn1.c
- mitshell/libmich (MAP/RAN codec reference) — https://github.com/mitshell/libmich
- tech-invite (clause-addressable rendering, golden set) — https://www.tech-invite.com/3m33/tinv-3gpp-33-128.html
- Local corpus evidence: `data/sources/origin/Rel-19/33128-j60.zip` (`TS33128Payloads.asn`, 958 types, 201 `XIRIEvent` arms), `29002-j10.zip` (MAP: `OPERATION`/`ERROR`/`CLASS` macro dialect)
