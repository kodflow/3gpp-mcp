# Axis 01 — Authoritative `li_events` from TS 33.128 ASN.1 / XML schema

> Research date: 2026-05-26. Status: implementation-ready design.
> Scope: replace prose-mined LI events with a machine-parsed, citable catalogue
> built from the **formal ASN.1 module + XML schema** that TS 33.128 ships as
> electronic attachments. Highest-leverage move for the LI vertical.
>
> **Key result of this research:** the authoritative artefact is already in our
> local corpus. Every `data/sources/origin/<Rel>/33128-*.zip` contains a nested
> `33128-<code>-attachments.zip` whose `TS33128Payloads.asn` enumerates **every**
> LI event as a tagged `CHOICE` member, grouped by originating NF with an inline
> `-- <NF> events, see clause <X>` comment, each event resolving to a `SEQUENCE`
> that lists its fields/IEs. This is ground truth — no prose mining, no LLM.

---

## 1. Problem

`internal/li` today derives the LI event list two ways, both interpretive:

- `Extract128` (catalog.go): scrapes TS 33.128 clause-6 HTML headings, matching
  `"Generation of xIRI/xCC over LI_X2/X3"` sections and treating leaf sub-clauses
  as events, then guessing the NF from the heading via `reNFin` regex. Source tag
  `"33.128/clause"`.
- `33.108/prose`: best-effort trigger-bullet mining under BEGIN/CONTINUE/END
  record clauses, with an explicit domain→NE mapping. Low confidence by design.

We have **proven this is unreliable**: `docs/generated/li_audit.md` shows that of
218 oracle events, 10 were attributed to the wrong spec, 48 only matched a parent
ref, and 5 were not found at all — because heading text and prose are lossy,
release-drifting, and never an exhaustive enumeration. The headings say *where the
spec talks about* an event; they are not the event registry.

TS 33.128, by contrast, **ships a formal registry**: an ASN.1 module that is the
normative on-the-wire definition of X1/X2/X3/HI2/HI3/HI4 payloads. The event list
is a closed `CHOICE`; the field list is a `SEQUENCE`; the NF is encoded in both the
grouping comment and the per-event type-name prefix; the release is encoded in the
module OID. Parsing it yields a complete, exact, citable `li_events` table.

---

## 2. Why ASN.1 beats prose (point by point)

| Dimension | Prose / HTML headings (current) | ASN.1 module (proposed) |
|---|---|---|
| **Completeness** | Only events that happen to be sub-clause headings; misses inline ones | Closed `CHOICE` — every X2/X3/HI2/HI4 event is a member, by construction |
| **Event identity** | Free-text heading, varies by release/editor | Stable lowerCamel member name + numeric tag (a registry key) |
| **Originating NF** | Regex-guessed from heading words (`reNFin`, `nfStop` denylist) | Encoded twice: `-- <NF> events` group comment **and** type-name prefix (`AMFRegistration`, `SMFPDUSessionEstablishment`, `MMEAttach`) |
| **Fields / IEs** | Not extracted at all | Full `SEQUENCE` with field name, type, OPTIONAL flag |
| **Interface (X2/X3/HI2)** | Heading substring `LI_X2`/`LI_X3` | Structural: member lives in `XIRIEvent` (X2), `CCPDU` (X3), `IRIEvent` (HI2), `LINotificationMessage` (HI4) |
| **Release** | Filename version code only | Module OID `... ts33128(19) r18(18) version14(14)` + per-member release comments |
| **Citation** | Clause path (often a parent) | Inline `see clause 6.2.2.2` comment per NF group → exact normative clause |
| **Determinism** | LibreOffice→HTML conversion noise | Plain-text grammar, stable across rebuilds |
| **Drift detection** | None | Diff `CHOICE` members between `r17/` and `r18/` = exact release delta |

Concrete proof of the drift point (measured from the local zips, reproducible via
`scripts/research/01-li-asn1-extract.sh`):

| Release | `XIRIEvent` (X2) | `IRIEvent` (HI2) | `CCPDU` (X3) | `AMFRegistration` fields |
|---|---|---|---|---|
| Rel-16 | 65 | 66 | 4 | 11 |
| Rel-17 | 116 | 115 | 6 | 19 |
| Rel-18 | 173 | 171 | 7 | 32 |

The 65→116→173 progression is the exact per-release LI event addition set; the
11→19→32 growth of a single event's field list is the exact per-release IE
addition set. Both are computable as a `CHOICE`/`SEQUENCE` member diff — and both
are impossible to derive reliably from prose. This is precisely why `li_events` and
`li_event_fields` are keyed per release and never merged across releases.

---

## 3. Exact source artefacts

### 3.1 Already in our corpus (primary, offline)

Every TS 33.128 spec zip nests an attachments zip:

```
data/sources/origin/Rel-18/33128-if0.zip
  └─ 33128-if0-attachments.zip
       ├─ TS33128Payloads.asn               (267 KB) ← THE event registry
       ├─ TS33128IdentityAssociation.asn    (IEF / identity-association records)
       ├─ TS33128Dictionaries.xml           (controlled vocab: ServiceType, …)
       ├─ urn_3GPP_ns_li_3GPPX1Extensions.xsd        (X1 provisioning target IDs)
       ├─ urn_3GPP_ns_li_3GPPLIQueryExtensions.xsd
       ├─ urn_3GPP_ns_li_3GPPIdentityExtensions.xsd
       ├─ urn_3GPP_ns_li_3GPPStateTransfer.xsd
       ├─ urn_3GPP_ns_li_3GPPXLAExtensions.xsd
       └─ CHANGELOG.md
```

Confirmed present (and re-verified by `scripts/research/01-li-asn1-extract.sh`,
2026-05-26) in our local origin mirror for **Rel-16 (`33128-gm0`), Rel-17
(`33128-hk0`), Rel-18 (`33128-if0`)**. The module OID second line carries the
release:

```
Rel-16: {… threeGPP(4) ts33128(19) r16(16) version17(17)}
Rel-17: {… threeGPP(4) ts33128(19) r17(17) version15(15)}
Rel-18: {… threeGPP(4) ts33128(19) r18(18) version14(14)}
```

**Caveat (measured):** our locally-mirrored **Rel-15 (`33128-fd0`)** and **Rel-19
(`33128-j60`)** zips do **not** carry an `*-attachments.zip` — the Rel-15 mirror
predates the attachment practice, and the Rel-19 mirror is an early/draft snapshot.
The forge repo (§3.2) still has those modules, so the ingest path must (a) handle a
missing attachments zip by falling back to the clause/prose path (degrade, don't
block) and (b) optionally fetch the module from forge for releases the mirror lacks.
Do **not** assume every 33.128 zip ships the ASN.1.

For the releases that do ship it, this is the cleanest possible ingestion input: it
is **inside the very zip we already download and convert**, so no new network fetch
is required.

### 3.2 Canonical upstream (provenance / verification)

- **3GPP Forge (git, CI-syntax-checked) — preferred citation home:**
  `https://forge.3gpp.org/rep/sa3/li` , per-release path
  `33128/r17/TS33128Payloads.asn`, `33128/r18/TS33128Payloads.asn`,
  `33128/r19/TS33128Payloads.asn`. This is the SA3-LI working repo; the zip
  attachments are snapshots of these files.
- **ETSI deliver (PDF + the same attachments):**
  `https://www.etsi.org/deliver/etsi_ts/133100_133199/133128/` (per-version
  subdirs, e.g. `15.05.00_60/ts_133128v150500p.pdf`).
- **3GPP archive (what we mirror):**
  `https://www.3gpp.org/ftp/Specs/archive/33_series/33.128/` — same zips as
  `model.ArchiveURL("33.128", version)` already builds.

---

## 4. Module structure (the four payload trees)

`TS33128Payloads.asn` (`DEFINITIONS IMPLICIT TAGS EXTENSIBILITY IMPLIED`) defines
one container `SEQUENCE` per interface, each wrapping an event `CHOICE`:

| Interface | Container `SEQUENCE` | Event `CHOICE` | Direction | Carries |
|---|---|---|---|---|
| **X2** | `XIRIPayload` | `XIRIEvent` (173 members, Rel-18) | POI → MDF2 | xIRI events |
| **X3** | `CCPayload` | `CCPDU` (7 members) | POI → MDF3 | xCC content PDUs |
| **HI2** | `IRIPayload` | `IRIEvent` (171 members, Rel-18) | MDF2 → LEMF | IRI records |
| **HI4** | `LINotificationPayload` | `LINotificationMessage` | LICF/MDF → LEMF | LI notifications |

Relative-OID anchors confirm the split:

```asn1
xIRIPayloadOID  RELATIVE-OID ::= {tS33128PayloadsOID xIRI(1)}
xCCPayloadOID   RELATIVE-OID ::= {tS33128PayloadsOID xCC(2)}
iRIPayloadOID   RELATIVE-OID ::= {tS33128PayloadsOID iRI(3)}
cCPayloadOID    RELATIVE-OID ::= {tS33128PayloadsOID cC(4)}
lINotificationPayloadOID RELATIVE-OID ::= {tS33128PayloadsOID lINotification(5)}
```

### 4.1 Event → NF → clause mapping (the real `XIRIEvent` head, Rel-18)

```asn1
XIRIEvent ::= CHOICE
{
    -- AMF events, see clause 6.2.2.2
    registration                                 [1] AMFRegistration,
    deregistration                               [2] AMFDeregistration,
    locationUpdate                               [3] AMFLocationUpdate,
    startOfInterceptionWithRegisteredUE          [4] AMFStartOfInterceptionWithRegisteredUE,
    unsuccessfulAMProcedure                      [5] AMFUnsuccessfulProcedure,
    -- SMF events, see clause 6.2.3.2
    pDUSessionEstablishment                      [6] SMFPDUSessionEstablishment,
    pDUSessionModification                       [7] SMFPDUSessionModification,
    pDUSessionRelease                            [8] SMFPDUSessionRelease,
    startOfInterceptionWithEstablishedPDUSession [9] SMFStartOfInterceptionWithEstablishedPDUSession,
    unsuccessfulSMProcedure                      [10] SMFUnsuccessfulProcedure,
    -- UDM events, see clause 7.2.2.3
    servingSystemMessage                         [11] UDMServingSystemMessage,
    -- SMS events, see clause 6.2.5.2
    sMSMessage                                   [12] SMSMessage,
    -- MME events, see clause 6.3.2.2
    mMEAttach                                    [87] MMEAttach,
    mMEDetach                                    [88] MMEDetach,
    ...
}
```

Two independent NF signals per event:
1. **Group comment** `-- AMF events, see clause 6.2.2.2` — gives NF + exact clause.
2. **Type-name prefix** of the referenced `SEQUENCE` (`AMFRegistration` → `AMF`,
   `SMFPDUSessionEstablishment` → `SMF`, `MMEAttach` → `MME`). Robust because
   3GPP names every event type `<NF><EventName>`.

> **The type-name prefix is the primary signal, the comment is the cross-check —
> not the other way round.** Measured: the group-comment phrasing *evolved across
> releases*. Rel-16/17 use functional descriptions (`-- Access and mobility related
> events, see clause 6.2.2`, `-- PDU session-related events, see clause 6.2.3`) that
> do **not** name the NF; Rel-18 switched to explicit NF names (`-- AMF events …`,
> `-- SMF events …`). So for full release coverage we must derive the NF from the
> stable type prefix and use the comment only for the clause anchor (and as an NF
> confirmation when it does name one).

NF groups actually present in Rel-18 `XIRIEvent` (from the group comments):
**AMF, SMF, UDM, SMS, LALS, PDHR/PDSR, MMS, PTC, NEF, SCEF, MME, AKMA (AAnF/AF),
HR-LI (N9HR/S8HR), STIR/SHAKEN, IMS, EES, 5GMS-AF, HSS, RCS, SGW**, plus
`Identifier Association` records (`AMFIdentifierAssociation`, `MMEIdentifierAssociation`).
This is exactly the NE/NF list the product needs, sourced normatively.

### 4.2 Event → fields/IEs (the real `AMFRegistration` SEQUENCE, Rel-18)

```asn1
AMFRegistration ::= SEQUENCE
{
    registrationType   [1] AMFRegistrationType,
    registrationResult [2] AMFRegistrationResult,
    slice              [3] Slice OPTIONAL,
    sUPI               [4] SUPI,
    sUCI               [5] SUCI OPTIONAL,
    pEI                [6] PEI OPTIONAL,
    gPSI               [7] GPSI OPTIONAL,
    gUTI               [8] FiveGGUTI,
    location           [9] Location OPTIONAL,
    ... (32 fields total) ...
    alternativeNSSAI   [32] AlternativeNSSAIList OPTIONAL
}
```

Each field becomes a row in a child `li_event_fields` table: name, ASN.1 type,
tag, optional flag, order.

### 4.3 X3 / CC side (`CCPDU`)

```asn1
CCPDU ::= CHOICE
{
    uPFCCPDU         [1] UPFCCPDU,         -- UPF user-plane content
    extendedUPFCCPDU [2] ExtendedUPFCCPDU,
    mMSCCPDU         [3] MMSCCPDU,
    nIDDCCPDU        [4] NIDDCCPDU,
    pTCCCPDU         [5] PTCCCPDU,
    iMSCCPDU         [6] IMSCCPDU,
    rCSCCPDU         [7] RCSCCPDU
}
```

CC PDUs attribute to the content-producing NF (UPF for user plane, etc.) and the
X3 interface.

---

## 5. Proposed DuckDB schema (`li_events` + children)

Additive to `internal/store/schema.sql`; idempotent `CREATE TABLE IF NOT EXISTS`.
Keyed on `(spec_id, release, interface, event_name)` so the same logical event is
tracked across interfaces (X2 vs HI2) and across releases.

```sql
-- Authoritative LI event registry, parsed from TS 33.128 ASN.1 (per release).
CREATE TABLE IF NOT EXISTS li_events (
    spec_id        VARCHAR,            -- always '33.128'
    release        VARCHAR,            -- 'Rel-18' (from module OID)
    module_version VARCHAR,            -- 'r18 version14' (OID tail, provenance)
    interface      VARCHAR,            -- 'X2'|'X3'|'HI2'|'HI4'
    event_name     VARCHAR,            -- CHOICE member, 'registration'
    asn1_type      VARCHAR,            -- referenced type, 'AMFRegistration'
    asn1_tag       INTEGER,            -- CHOICE tag, e.g. 1 (registry key)
    originating_nf VARCHAR,            -- 'AMF' (group comment + type prefix)
    domain         VARCHAR,            -- '5GC'|'EPC'|'IMS'|… (derived from NF/clause)
    spec_clause    VARCHAR,            -- '6.2.2.2' (from group comment)
    field_count    INTEGER,
    PRIMARY KEY (spec_id, release, interface, event_name)
);
CREATE INDEX IF NOT EXISTS li_events_nf    ON li_events (originating_nf);
CREATE INDEX IF NOT EXISTS li_events_iface ON li_events (interface);
CREATE INDEX IF NOT EXISTS li_events_rel   ON li_events (release);

-- One row per IE inside an event SEQUENCE.
CREATE TABLE IF NOT EXISTS li_event_fields (
    spec_id    VARCHAR,
    release    VARCHAR,
    interface  VARCHAR,
    event_name VARCHAR,
    field_name VARCHAR,               -- 'sUPI'
    asn1_type  VARCHAR,               -- 'SUPI'
    asn1_tag   INTEGER,
    is_optional BOOLEAN,
    ordinal    INTEGER,               -- declaration order
    PRIMARY KEY (spec_id, release, interface, event_name, field_name)
);

-- NF interception clause anchors (LI at AMF -> 6.2.2.2), one per NF/release.
-- Lets get_spec / find_cross_references jump straight to the normative clause.
CREATE TABLE IF NOT EXISTS li_nf_clauses (
    spec_id     VARCHAR,
    release     VARCHAR,
    originating_nf VARCHAR,
    interface   VARCHAR,
    spec_clause VARCHAR,
    PRIMARY KEY (spec_id, release, originating_nf, interface)
);
```

Citation for any `li_events` row is built exactly like every other tool response:
`model.Citation{SpecID:"33.128", Release, Version, Clause: spec_clause, URL:
model.ArchiveURL("33.128", version)}` — reusing the existing `Clause.Cite()`
plumbing, so the no-hallucination contract (CLAUDE.md §1) holds automatically.

---

## 6. Parsing approach (ASN.1 → events)

The grammar fragment we need is tiny and regular — we do **not** need a full ASN.1
compiler. A focused, hand-written scanner over the plain-text module is the V1
choice (no new dependency, Go-only, CLAUDE.md §13 compliant):

1. **Locate attachments.** During ingest of `33128-*`, before/instead of the HTML
   path, read the nested `*-attachments.zip` from the origin zip (we already open
   these zips). Extract `TS33128Payloads.asn` bytes.
2. **Module release.** Parse line 2 OID: regex
   `ts33128\(19\)\s+r(\d+)\(\d+\)\s+version(\d+)\(\d+\)` → release `Rel-1<n>`,
   `module_version`.
3. **Strip conversion noise.** The LibreOffice-attached copy has spurious empty
   `{}` blocks after each definition; ignore lines matching `^\{$` / `^\}$` that
   are not the *first* brace following a `::=`. (The forge raw files are clean; we
   normalise either way.)
4. **Walk the four containers.** For each of `XIRIEvent`, `CCPDU`, `IRIEvent`,
   `LINotificationMessage`: read members until the matching close brace.
   - Group comment line `--\s*(?P<nf>[A-Z0-9/]+).*events.*clause\s+(?P<clause>[0-9.]+)`
     updates the current NF + clause context (sticky until the next comment).
   - Member line `(?P<name>[a-z][A-Za-z0-9]*)\s+\[(?P<tag>\d+)\]\s+(?P<type>[A-Za-z0-9]+)`
     yields one event. NF = group NF, cross-checked against the longest known-NF
     prefix of `type` (mismatch → log + prefer the type prefix, record both).
5. **Resolve fields.** For each event `type`, find `^<Type> ::= SEQUENCE` and read
   its members (same member regex, plus `OPTIONAL`) → `li_event_fields`.
6. **Domain.** Derive from NF via a small static map (AMF/SMF/UDM/UPF/NEF/AUSF…→5GC;
   MME/SGW/SCEF→EPC; IMS/RCS→IMS), consistent with `internal/li` `domainOf`.
7. **Emit** `[]LIEvent` + `[]LIEventField` + `[]LINFClause`, deterministic order
   (by interface, then tag).

Edge cases handled: `-- continued from tag N` comments (same NF, ignore as marker);
reserved tags (`-- Tag 16 is reserved …` → skip, no member line); release-gated
members (comment notes a tag means something else in r16 — record per-release as
parsed, the delta falls out naturally from per-release ingestion).

> Optional hardening (V2): validate our scanner output against a real ASN.1
> toolchain offline (e.g. `asn1c -EF` or `eerimoq/asn1tools`) as a CI check, not on
> the query path. Not required for V1 — the scanner is the source of truth for the
> table; the toolchain only audits it.

---

## 7. NF / release attribution — how each column is sourced

| Column | Source of truth | Fallback / cross-check |
|---|---|---|
| `originating_nf` | event-type name prefix (`AMFRegistration`→AMF) | `-- <NF> events` group comment |
| `spec_clause` | `see clause <X>` in group comment | NF interception section heading in clause-6 HTML (existing `NFSections`) |
| `interface` | which container CHOICE the member is in | RELATIVE-OID anchor (xIRI/xCC/iRI) |
| `release` | module OID `r<n>` | origin-zip filename version code (existing decoder) |
| `domain` | static NF→domain map | clause-prefix heuristic (`domainOf` 6.2=5GC, 6.3=EPC) |
| fields | event `SEQUENCE` members | — |

The previously fragile step — guessing NF from a heading — is **eliminated**: NF is
now read from a stable type-name prefix and independently confirmed by the group
comment. Disagreements are logged, not silently resolved.

---

## 8. Integration points in this repo

- **New package `internal/li/asn1`** (Go-only, stdlib `archive/zip` + `regexp` +
  `bufio`): `ParseModule(r io.Reader) (*Module, error)` returning
  `Module{Release, ModuleVersion string; Events []Event; Fields []Field; NFClauses []NFClause}`.
  Keep it dependency-free; it parses one `.asn` text stream.
- **`internal/li`**: add `LIEvent`, `LIEventField`, `LINFClause` structs (mirror the
  schema; reuse `model.Citation`). The existing `Event`/`Extract128` stay as a
  *lower-confidence* fallback for specs without ASN.1 (33.108 legacy) — now clearly
  ranked below ASN.1 (`source = "33.128/asn1"` > `"33.128/clause"` > `"33.108/prose"`).
- **`internal/store`**: append the three tables to `schema.sql`; add
  `InsertLIEvents`, `InsertLIEventFields`, `InsertLINFClauses` (bulk, same pattern
  as `InsertClauses`); bump a `schema_meta` migration key.
- **`internal/ingest/ingest.go`**: in the per-job loop, when
  `ps.Spec.SpecID == "33.128"`, open the origin zip's nested `*-attachments.zip`,
  call `asn1.ParseModule`, attribute via §7, and insert. This runs **per release**,
  so the multi-release rows (and thus the deltas) populate naturally. Add
  `LIEvents` / `LIEventFields` counters to `Stats`.
  - Note: ingest currently globs converted HTML under `ConvertDir`. The ASN.1 lives
    in the **origin** zip, so either (a) thread the origin-zip path alongside the
    HTML job, or (b) have `scripts/corpus.sh`/convert step also copy
    `*-attachments.zip` next to the HTML. (a) is cleaner and keeps one source of
    truth.
- **`internal/mcp`**: ASN.1-backed answers slot into existing tools — `search_spec`
  (filter `series=33`, `spec_type=TS`), `get_spec` (clause from `spec_clause`),
  `find_cross_references` (event → IMS/EPC equivalents via Identifier-Association
  records), `get_changelog` (release delta = `CHOICE` member diff). Optionally a
  thin `list_li_events(nf?, interface?, release?)` view — but it can be expressed
  through `search_spec`/`list_specs` to honour the "8 tools, not more" rule
  (CLAUDE.md §5); recommend **no new MCP tool** in V1, just richer data behind the
  existing ones.

---

## 9. Step-by-step plan

1. **Spike (read-only):** `scripts/research/01-li-asn1-extract.sh` (added here)
   dumps event/NF/clause/field counts per release from the local zips — sanity
   baseline and a fixture generator. (Done — see that script.)
2. **`internal/li/asn1` parser** + table-driven unit tests using the local modules
   (r16, r17, r18 — the releases whose mirror ships the attachment) as golden
   fixtures. Assert measured counts: `XIRIEvent` = 65 / 116 / 173 (r16/17/18),
   `IRIEvent` = 66 / 115 / 171, `CCPDU` = 4 / 6 / 7; `AMFRegistration` field_count =
   11 / 19 / 32; Rel-18 NF set includes AMF…SGW. Include a fixture with **no**
   attachments zip (r15/r19 mirror) to lock the degrade-don't-block fallback.
3. **Schema + store methods** (three tables, three bulk inserts, migration key).
4. **Ingest wiring** for `33.128`, per release; populate `Stats`.
5. **Confidence ranking** in `internal/li` so ASN.1 events supersede clause/prose
   for 33.128; regenerate `docs/generated/li_*.md` and re-run the
   `sentinel_r17_events.json` audit — expect WRONG_SPEC_REF/ NOT_FOUND for 5GC
   NF-native events to drop sharply.
6. **Release-delta view** (Rel-17→18→19) from member diffs; surface via
   `get_changelog`.
7. **(V2)** offline `asn1c`/`asn1tools` CI audit of the parser; pull from
   `forge.3gpp.org/rep/sa3/li` to detect upstream changes between archive snapshots.

---

## 10. Risks & mitigations

| Risk | Likelihood | Mitigation |
|---|---|---|
| LibreOffice-attached `.asn` has noise (`{}` blocks) | High (observed) | Normalise in step §6.3; prefer forge raw if ever fetched |
| Event type-name prefix ≠ NF for a few odd types (e.g. `N9HRPDUSessionInfo`) | Low | Dual-signal (group comment) + a small alias map (`N9HR`/`S8HR`→HR-LI; `AAnF`/`AF`→AKMA) |
| `IMPORTS` from ETSI TS 102 232-3 (`IPIRIPacketReport`) unresolved | Low | Record the type name as-is; we are cataloguing, not decoding bytes |
| Clause comment missing/ambiguous for a group | Low | Fall back to existing `NFSections` clause-6 HTML anchor |
| Schema churn between releases (tag reuse, `EXTENSIBILITY IMPLIED`) | Medium | Ingest **per release**; never merge across releases — deltas are first-class |
| Hand-written scanner drifts from real ASN.1 grammar | Medium | Golden-fixture tests on 5 releases; V2 toolchain audit |
| Attachments absent for some legacy 33.128 version | Low | Guard: if no `*-attachments.zip`, fall back to clause/prose path (degrade, don't block — matches ingest philosophy) |

---

## 11. One-line bottom line

The LI event registry we were trying to *reconstruct* from prose is **already a
formal, versioned, citable artefact inside every 33.128 zip we download**. Parse
`TS33128Payloads.asn` (173 X2 events in Rel-18, exact NF + clause per event, full
field lists, exact release deltas) into `li_events` and the LI vertical goes from
"search prose, hope" to "authoritative catalogue with exact citations."

---

## Sources

- 3GPP Forge SA3-LI repo (authoritative git, per release):
  - https://forge.3gpp.org/rep/sa3/li (path `33128/r17/`, `33128/r18/`, `33128/r19/TS33128Payloads.asn`)
- ETSI deliver TS 133 128: https://www.etsi.org/deliver/etsi_ts/133100_133199/133128/
- 3GPP archive (mirrored locally): https://www.3gpp.org/ftp/Specs/archive/33_series/33.128/
- 3GPP LI landing: https://www.3gpp.org/technologies/li
- Local artefacts (verified 2026-05-26): `data/sources/origin/{Rel-15..Rel-19}/33128-*.zip` → nested `33128-*-attachments.zip` → `TS33128Payloads.asn`, `TS33128IdentityAssociation.asn`, `urn_3GPP_ns_li_3GPP*.xsd`, `TS33128Dictionaries.xml`
- Existing repo context: `docs/research/improvement-axes.md` (Axis 6), `docs/generated/li_audit.md`, `internal/li/catalog.go`, `internal/store/schema.sql`
