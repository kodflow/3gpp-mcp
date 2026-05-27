# Axis 02 — Ingesting 5GC OpenAPI YAML (forge.3gpp.org) as first-class citable entities

Status: research / design (implementation-ready)
Date: 2026-05-26
Owner axis: #2 (OpenAPI 5GC)
Scope guard: this is a design doc only. Implementation touches `cmd/`, `internal/`,
`store/schema.sql` and is **out of scope** for this axis (see §10 file-by-file plan).

---

## 0. Why this axis exists

The current corpus is LibreOffice-converted HTML prose: clauses with headings and
text, cited as `{spec_id, release, version, clause, url}`. For the 5GC
service-based interfaces (TS 29.5xx series) 3GPP also publishes the **canonical,
machine-readable OpenAPI 3.0 YAML** on the 3GPP Forge GitLab. These files are a
*better* citation target than scraped headings because:

- They are normative, structured, and unambiguous: an operation is
  `{service, path, method, operationId}`; a data type is a named schema under
  `components/schemas`. No heading-parsing guesswork.
- They cross-reference each other with `$ref` (e.g. every NF service references
  `TS29571_CommonData.yaml#/components/schemas/...`), giving a real dependency
  graph for free.
- They carry their own provenance: `info.version` + `externalDocs` name the exact
  TS spec and version (e.g. *"3GPP TS 29.518 V18.13.0"*), so the citation is
  exact and self-contained — fully compatible with the project's "no
  hallucination, cite or stay silent" doctrine (CLAUDE.md §1).

This axis indexes API **operations** and **schemas** as new citable entity types,
cross-linked to the existing `clauses` rows of the same TS 29.5xx spec.

---

## 1. Source: the 3GPP Forge `5G_APIs` repository

- Web/UI: `https://forge.3gpp.org/rep/all/5G_APIs`
- It is a GitLab instance. Project path: `all/5G_APIs`.
- All 5GC + Network-Capability-Exposure API YAMLs for one Release live in the
  **same flat directory** at the branch root (no per-NF subdirectories).

### 1.1 Release → git ref mapping (the important part)

The forge uses **two** ref conventions; ingestion accepts both. Verified live
against the GitLab API on 2026-05-26 (`.../repository/branches`):

| Ref style | Examples (verified live) | Meaning |
|---|---|---|
| Stable per-release branch | `REL-15`, `REL-16`, `REL-17`, `REL-18`, `REL-19`, `REL-20` | Per-release content; `REL-20` is the repo **default branch** |
| Rolling per-release draft branch | `Rel15-draft-TSG112` … `Rel20-draft-TSG112` | Draft content, named by the latest TSG plenary (`TSG<NN>`) |

Both families exist for every release 15–20. The `REL-<NN>` branch is the
natural pin target (it is what the README raw URLs and the WebFetch examples in
this doc resolve against). The `RelNN-draft-TSGNNN` branches track in-flight
content and are most useful for the newest (not-yet-frozen) release.

**Determinism requirement (CLAUDE.md §1 "Reproductibilité d'ingestion").** A
branch name like `Rel18-draft-TSG112` is a *moving target*. To get a stable hash
we MUST pin to an immutable **commit SHA**, not a branch. The fetch plan (§6)
resolves a branch → its current head SHA once, records the SHA in
`schema_meta`, and fetches every file by that SHA. Re-running with the recorded
SHA reproduces the exact corpus.

Recommended release→ref policy table (config, not hardcoded). Prefer the stable
`REL-<NN>` branch; resolve it to its head SHA at ingest time and pin:

```
Rel-15 -> REL-15
Rel-16 -> REL-16
Rel-17 -> REL-17
Rel-18 -> REL-18    (verified: head SHA 867f0e81ced93aa72376ef97c5e8f9df63fbbf3b on 2026-05-26)
Rel-19 -> REL-19
Rel-20 -> REL-20    (repo default branch)
```

For the newest, still-evolving release, the `RelNN-draft-TSGNNN` branch can be
substituted when more current than `REL-<NN>`.

MVP per CLAUDE.md §7 targets Rel-17/18/19; the 29-series is already in the MVP
series set, so this axis slots straight into the V1 scope.

### 1.2 Deterministic raw-fetch URLs

Two equivalent forms (both observed working):

```
# by commit SHA (preferred — immutable, reproducible)
https://forge.3gpp.org/rep/all/5G_APIs/-/raw/<SHA>/<FILE>.yaml

# by branch (resolve to SHA first, then pin)
https://forge.3gpp.org/rep/all/5G_APIs/-/raw/<BRANCH>/<FILE>.yaml
```

GitLab REST API (no auth needed for this public project). **Important:** this
GitLab instance is mounted under `/rep`, so the API base is
`https://forge.3gpp.org/rep/api/v4/` — **not** `/api/v4/` (that path 404s). All
endpoints below verified live 2026-05-26:

```
PROJ=https://forge.3gpp.org/rep/api/v4/projects/all%2F5G_APIs

# project metadata (confirms default_branch = REL-20)
$PROJ
# list branches (offset pagination; ?per_page=100&page=N — keyset truncates here)
$PROJ/repository/branches?per_page=100&page=1
# list files at a ref (flat YAML dir); each item: {id(=blob SHA), name, type, path}
$PROJ/repository/tree?ref=<REF>&per_page=100
# resolve a branch to its commit SHA (the pin target for reproducibility)
$PROJ/repository/commits/<BRANCH>     # -> .id = commit SHA
```

(Project path `all/5G_APIs` is URL-encoded as `all%2F5G_APIs`.) The tree item
`id` is the **per-blob SHA**, which can pin an individual file even more tightly
than the commit SHA if desired. Raw fetch can use either the commit SHA or a
branch ref (both verified returning HTTP 200, e.g. `CommonData.yaml` = 220 KB).

A pure-Go offline-friendly mirror exists at `github.com/jdegre/5GC_APIs` with
`Rel-15..Rel-19` branches; useful as a fallback/cross-check source but the forge
is authoritative.

### 1.3 File naming convention → `spec_id`

Pattern: `TS<5digits>_N<nf>_<Service>.yaml` (plus a few non-`N...` commons files).

| Filename | spec_id | What it is |
|---|---|---|
| `TS29571_CommonData.yaml` | `29.571` | Common data types (shared schemas, almost no paths) |
| `TS29518_Namf_Communication.yaml` | `29.518` | Namf Communication service |
| `TS29518_Namf_EventExposure.yaml` | `29.518` | Namf Event Exposure service |
| `TS29510_Nnrf_NFManagement.yaml` | `29.510` | NRF NF management |
| `TS29503_Nudm_SDM.yaml` | `29.503` | UDM subscriber data management |
| `TS29502_Nsmf_PDUSession.yaml` | `29.502` | SMF PDU session |

Note: the directory is **not** 29-series only. Live `REL-18` also carries
exposure/SEAL APIs from other series (verified: `TS24549_ETC_Configuration.yaml`,
`TS24549_NSCE_SliceInfo.yaml`). The mapping rule below is series-agnostic; the
fetch filter (§6.1) should match `TS\d{5}_*.yaml` broadly, then the
`externalDocs` spec_id decides what each row belongs to. For the MVP an explicit
`29.*` filter is acceptable (CLAUDE.md §7 series scope) with the others deferred.

**Mapping rule:** strip the leading `TS`, take the first 5 digits, insert a dot
after the 2nd digit → `29571` ⇒ `29.571`. The middle token `N<nf>` (e.g.
`Namf`, `Nudm`, `Nnrf`) gives the **service family**; the trailing token gives
the **service** name. One TS spec ⇒ N files ⇒ N services (e.g. `29.518` ⇒
`Namf_Communication`, `Namf_EventExposure`, `Namf_Location`, `Namf_MT`).

**Authoritative provenance lives *inside* the file**, so we never guess version:

```yaml
# TS29518_Namf_Communication.yaml (REL-18 branch, 2026-05)
openapi: 3.0.0
info:
  title: Namf_Communication
  version: 1.3.4                      # <-- API doc version (NOT the TS version)
externalDocs:
  description: '3GPP TS 29.518 V18.13.0; 5G System; Access and Mobility Management Services'
  url: 'https://www.3gpp.org/ftp/Specs/archive/29_series/29.518/'
servers:
  - url: '{apiRoot}/namf-comm/v1'
```

Parse `externalDocs.description` with `T[SR]\s?(\d\d\.\d{3})\s+V(\d+\.\d+\.\d+)`
to recover **spec_id** (`29.518`) and the **TS version** (`18.13.0`) — this is
the same dotted version the existing `spec_versions` table already keys on, so
OpenAPI entities align with HTML clauses by `(spec_id, version)` *for free*.
`info.version` (`1.3.4`) is the API-document revision and is stored separately
(`api_doc_version`) for completeness, never used as the citation version.

---

## 2. OpenAPI structure we extract

For each YAML file:

- `openapi`, `info.title`, `info.version`
- `externalDocs.{description,url}` → spec_id, TS version, archive URL
- `servers[].url` → API root + version segment (e.g. `namf-comm/v1`)
- `paths.<path>.<method>` → one **operation** per (path, method):
  `operationId`, `summary`, `tags[]`, plus the `$ref`s in
  `requestBody`/`responses` (the schemas it consumes/produces).
- `components/schemas.<Name>` → one **schema** entity per named type:
  `name`, `type`/`enum`/`properties` summary, and the set of `$ref`s it points
  to (its dependencies).

### 2.1 Real fetched example (verbatim, REL-18, 2026-05)

`TS29518_Namf_Communication.yaml`, one operation:

```yaml
paths:
  /ue-contexts/{ueContextId}:
    put:
      operationId: CreateUEContext
      summary: Namf_Communication CreateUEContext service Operation
      tags:
        - Individual ueContext (Document)
      requestBody:
        content:
          multipart/related:           # jsonData + N2 binary parts
            schema:
              properties:
                jsonData:
                  $ref: '#/components/schemas/UeContextCreateData'
```

Cross-file `$ref`s observed in the same file (the dependency edges we capture):

```yaml
$ref: 'TS29571_CommonData.yaml#/components/schemas/Uri'
$ref: 'TS29571_CommonData.yaml#/components/responses/307'
$ref: 'TS29571_CommonData.yaml#/components/schemas/Guami'
$ref: 'TS29502_Nsmf_PDUSession.yaml#/components/schemas/EbiArpMapping'
```

So `CreateUEContext` resolves to:
- citation: `{spec_id: 29.518, release: Rel-18, version: 18.13.0,
  clause: "API:namf-comm/v1 PUT /ue-contexts/{ueContextId}", url: <forge raw SHA URL>}`
- schema used: `UeContextCreateData` (same file), which transitively depends on
  `29.571:Guami`, `29.571:Uri`, `29.502:EbiArpMapping`.

---

## 3. Proposed DuckDB schema (additive — appended to `store/schema.sql`)

Two new tables mirror the two entity kinds. Both are keyed so that re-ingest of
the same SHA is idempotent, and both link to `spec_versions(spec_id, release,
version)` exactly like `clauses` do. `forge_sha`/`forge_url` make every row
self-citing and reproducible.

```sql
-- API operations (one row per path+method). The "clause" of an operation is a
-- synthetic, stable locator: "<apiRoot> <METHOD> <path>" (CLAUDE.md §1 citation).
CREATE TABLE IF NOT EXISTS api_operations (
    op_id          UBIGINT PRIMARY KEY,   -- synthetic, assigned at ingest
    spec_id        VARCHAR,               -- '29.518'  (from externalDocs)
    release        VARCHAR,               -- 'Rel-18'  (from the resolved ref)
    version        VARCHAR,               -- '18.13.0' (TS version, links to spec_versions)
    api_doc_version VARCHAR,              -- '1.3.4'   (info.version, informative)
    service        VARCHAR,               -- 'Namf_Communication'  (filename token)
    service_family VARCHAR,               -- 'Namf'                (N<nf> token)
    api_root       VARCHAR,               -- 'namf-comm/v1'        (servers[].url tail)
    path           VARCHAR,               -- '/ue-contexts/{ueContextId}'
    method         VARCHAR,               -- 'PUT' (upper-cased)
    operation_id   VARCHAR,               -- 'CreateUEContext'
    summary        VARCHAR,               -- 'Namf_Communication CreateUEContext ...'
    tags           VARCHAR[],             -- ['Individual ueContext (Document)']
    request_schema VARCHAR,               -- top-level request schema name (best-effort)
    response_codes VARCHAR[],             -- ['200','201','307','400',...]
    yaml_file      VARCHAR,               -- 'TS29518_Namf_Communication.yaml'
    forge_sha      VARCHAR,               -- pinned commit SHA (reproducibility)
    forge_url      VARCHAR                -- raw URL by SHA (citation .url)
);
CREATE INDEX IF NOT EXISTS api_ops_spec    ON api_operations (spec_id, release);
CREATE INDEX IF NOT EXISTS api_ops_service ON api_operations (service);
CREATE INDEX IF NOT EXISTS api_ops_opid    ON api_operations (operation_id);

-- API schemas / IEs (one row per components/schemas entry).
CREATE TABLE IF NOT EXISTS api_schemas (
    schema_id      UBIGINT PRIMARY KEY,
    spec_id        VARCHAR,               -- '29.571'
    release        VARCHAR,               -- 'Rel-18'
    version        VARCHAR,               -- '18.11.0'
    service        VARCHAR,               -- 'CommonData' (or NF service)
    schema_name    VARCHAR,               -- 'Guami'
    kind           VARCHAR,               -- 'object'|'enum'|'string'|'array'|...
    description    VARCHAR,               -- schema.description (verbatim, may be '')
    properties     VARCHAR[],             -- property names (object kinds)
    enum_values    VARCHAR[],             -- enum literals (enum kinds)
    refs_out       VARCHAR[],             -- ['29.518:UeContextCreateData', '29.571:Uri'] dependency edges
    yaml_file      VARCHAR,
    forge_sha      VARCHAR,
    forge_url      VARCHAR                -- raw URL by SHA, optionally with #/components/schemas/<name> fragment
);
CREATE INDEX IF NOT EXISTS api_sch_spec ON api_schemas (spec_id, release);
CREATE INDEX IF NOT EXISTS api_sch_name ON api_schemas (schema_name);
```

Notes:
- `op_id`/`schema_id` continue the same UBIGINT counter style used for
  `clauses.chunk_id` (assigned in the ingest loop), so all entity ids are unique
  per snapshot.
- `refs_out` stores resolved cross-file references as `"<spec_id>:<SchemaName>"`
  (file token mapped to spec_id via §1.3). Same-file `$ref` (`#/...`) stores the
  current spec_id. This is the API dependency graph — the relational stand-in
  for the V2 KuzuDB layer, exactly like `evolutions` is for NE↔NF.
- `VARCHAR[]` arrays are written with the existing `string_split(?, '\x1f')`
  trick already used by `InsertChanges` (store.go) — no native array binding.
- Optional `api_doc` provenance can also be recorded as a synthetic
  `spec_versions` row variant; not required for V1 (the TS version already maps
  the OpenAPI file to an existing `spec_versions` row).

### 3.1 Citation shape

`model.Citation` is reused unchanged. For an operation:

```json
{
  "spec_id": "29.518",
  "release": "Rel-18",
  "version": "18.13.0",
  "clause": "API namf-comm/v1 PUT /ue-contexts/{ueContextId} (CreateUEContext)",
  "url": "https://forge.3gpp.org/rep/all/5G_APIs/-/raw/<SHA>/TS29518_Namf_Communication.yaml"
}
```

The `clause` field is overloaded as a human-readable, stable operation locator.
This keeps the universal citation contract (CLAUDE.md §1, §5) intact: every API
answer still cites `{spec_id, release, version, clause, url}`. `url` points at
the immutable SHA raw file — the most precise, reproducible source possible.

---

## 4. Cross-linking to existing `clauses`

Three linkage mechanisms, in increasing precision:

1. **By `(spec_id, version)` — automatic.** The TS version parsed from
   `externalDocs` is the same dotted version `spec_versions`/`clauses` use, so a
   query for spec `29.518` at `Rel-18` naturally returns both prose clauses and
   API operations of the same version. No join table needed.

2. **By service ↔ clause heading — heuristic, best-effort.** TS 29.5xx prose
   clauses describe each service operation (e.g. a clause titled "CreateUEContext"
   or "5.2.2.2.1"). At ingest we can attach the nearest matching `clause_path` to
   an operation by matching `operation_id`/`summary` against clause headings of
   the same `(spec_id, version)`. Stored as an optional `clause_path` column on
   `api_operations` (nullable; only set on confident match). Never fabricated —
   left NULL when no match (doctrine: cite or stay silent).

3. **By `$ref` dependency graph — exact, within the API layer.** `refs_out`
   links operations/schemas across specs (e.g. `29.518` op → `29.571` schema),
   powering "what data types does Namf Communication consume" and feeding
   `find_cross_references` with API-level edges in addition to the existing
   `T[SR] dd.ddd` prose-mention regex.

The existing `find_cross_references` MCP tool (server.go) regexes prose for
`TS dd.ddd` mentions. This axis augments it: when a spec has API rows, also emit
the distinct `spec_id`s found in `api_*.refs_out` for that spec — turning a
fuzzy prose signal into an exact, machine-derived one.

---

## 5. MCP exposure

Recommendation: **one new tool** (keeps within the "8 tools, pas plus" spirit by
being an API-specific sibling, clearly scoped), plus enrichment of two existing
tools. Avoid bloating `search_spec`.

### 5.1 New tool: `search_api`

```
search_api(query, release?, service?, service_family?, spec_id?, method?,
           kind='operation'|'schema'|'any', top_k=10)
```

- Lexical match over `operation_id`, `summary`, `path`, `tags`, `service`
  (operations) and `schema_name`, `description`, `properties` (schemas), reusing
  the same BM25/LIKE fallback pattern as `SearchClauses`.
- Returns hits with the API citation of §3.1 and, for operations, the linked
  prose `clause_path` when known (§4.2).
- Honours the server `baseline` release the same way the other tools do
  (`r.GetString("release", h.baseline)`).

Example call/return:

```
search_api(query="create UE context", service_family="Namf", release="Rel-18")
-> hits: [{
     service: "Namf_Communication", method: "PUT",
     path: "/ue-contexts/{ueContextId}", operation_id: "CreateUEContext",
     summary: "Namf_Communication CreateUEContext service Operation",
     citation: { spec_id:"29.518", release:"Rel-18", version:"18.13.0",
                 clause:"API namf-comm/v1 PUT /ue-contexts/{ueContextId} (CreateUEContext)",
                 url:"https://forge.3gpp.org/.../raw/<SHA>/TS29518_Namf_Communication.yaml" } }]
```

### 5.2 Enrich existing tools (no new surface)

- `get_spec(spec_id, release, clause?)`: when `spec_id` is a 29.5xx spec with API
  rows and `clause` is omitted or starts with `API`, include an `api` block
  (operations + schemas) alongside prose clauses. Backward compatible.
- `find_cross_references`: add API-derived edges from `refs_out` (§4.3).
- `list_specs`: 29.5xx specs gain an `has_api: true` flag so a client knows to
  call `search_api`.

### 5.3 Alternative: MCP **resource** per YAML

MCP resources could expose each YAML file (`resource://5g-api/<spec>/<service>`)
for verbatim fetch. Lower priority than `search_api` for V1 because the value is
in *searchable, cited fragments*, not whole-file blobs; keep as a V2 note. If
added, the resource URI is just the pinned forge raw URL — zero extra storage.

---

## 6. Fetch + parse plan

New ingest path, kept separate from the HTML pipeline so the two are
independently reproducible. Two viable shapes; **(A) a new subcommand** is
cleaner given the different source (network/git vs local HTML) and the existing
`cmd/ingest` is filesystem-only.

### 6.1 Source acquisition (offline-capable, deterministic)

`scripts/fetch-5g-apis.sh` (mirrors the existing `scripts/corpus.sh` fetch +
"degrade, don't block" doctrine):

1. For each target release, read the configured ref (§1.1).
2. Resolve ref → head **commit SHA** via the GitLab commits API; record it.
3. List the tree at that SHA, filter to `TS29*.yaml` (and any `TS28*/TS29*`
   commons), download each by SHA raw URL into
   `data/sources/5g-apis/<Rel>/<SHA>/<file>.yaml`.
4. Write a manifest `data/sources/5g-apis/<Rel>/manifest.json`
   `{release, ref, sha, files:[...], fetched_at}`.

This keeps the whole network step out of the Go binary (CLAUDE.md: internet only
for ingestion; query path is local). Ingest then reads local YAML.

### 6.2 Parse + load (`cmd/ingest-openapi` + `internal/openapi`)

- `internal/openapi/parse.go`: parse with `gopkg.in/yaml.v3` into a minimal
  struct (only the fields in §2 — not a full OpenAPI model). Pure Go, no CGO.
  - decode `externalDocs.description` → `(spec_id, ts_version)` via regex.
  - walk `paths` → emit `model.APIOperation`; collect `$ref`s in
    request/response bodies.
  - walk `components/schemas` → emit `model.APISchema`; collect `$ref`s →
    `refs_out` (file token → spec_id).
  - map filename → `(spec_id, service, service_family)` per §1.3 as a sanity
    cross-check against `externalDocs` (warn + prefer `externalDocs` on mismatch).
- `internal/openapi/ingest.go` (or extend `internal/ingest`): open the same
  DuckDB file, `INSERT` into `api_operations`/`api_schemas` in a transaction
  (reuse the store batch pattern), upsert a `spec_versions` row for the TS
  version if absent, set `schema_meta` keys `5g_apis_<Rel>_sha`.
- New store methods: `InsertAPIOperations`, `InsertAPISchemas`, `SearchAPI`,
  `GetAPIForSpec` — mirroring existing `InsertClauses`/`SearchClauses` exactly
  (including the `string_split` array trick and the FTS/LIKE dual path).

### 6.3 Determinism

A run is keyed by the recorded SHAs. Same SHAs ⇒ identical `api_*` rows ⇒ stable
DB hash, satisfying CLAUDE.md §1 "Reproductibilité d'ingestion". The SHA per
release is persisted in `schema_meta` and surfaced by the server for audit.

---

## 7. Step-by-step implementation plan (post-axis, for `/plan`→`/do`)

1. `scripts/fetch-5g-apis.sh`: branch→SHA resolution, tree listing, raw download,
   manifest. (Bash, GitLab API, no auth.)
2. `internal/model/types.go`: add `APIOperation`, `APISchema` structs + `Cite()`
   methods (citation §3.1). Add a `ForgeRawURL(file, sha)` helper alongside
   `ArchiveURL`.
3. `internal/store/schema.sql`: add the two tables (§3).
4. `internal/store/store.go`: add `InsertAPIOperations`, `InsertAPISchemas`,
   `SearchAPI`, `GetAPIForSpec`, extend `Reset()` to clear the two tables.
5. `internal/openapi/parse.go`: YAML → entities; externalDocs + filename mapping;
   `$ref` extraction → `refs_out`.
6. `internal/openapi/ingest.go`: load entities into the store; record SHAs.
7. `cmd/ingest-openapi/main.go`: flags `--src data/sources/5g-apis`,
   `--out data/3gpp.duckdb`, `--release`, `--fts`. (Mirror `cmd/ingest`.)
8. `internal/mcp/server.go`: register `search_api`; enrich `get_spec`,
   `find_cross_references`, `list_specs` (§5).
9. `Makefile`: `ingest-openapi` target; wire into the corpus build.
10. Tests: `internal/openapi/parse_test.go` against a committed fixture YAML
    (a trimmed `TS29518_Namf_Communication.yaml`), store round-trip test,
    server tool test (mirror `server_test.go`).

Each of these is small and isolated; steps 1–7 are independent of the MCP surface
and can land first behind a feature flag (no `search_api` until data exists).

---

## 8. Risks & mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Branch is a moving target (`Rel18-draft-TSGNN`) | non-reproducible DB | Pin to resolved commit SHA; store in `schema_meta` (§1.1, §6.3) |
| `$ref` to external file with non-`TS` token, relative paths, or `..` | broken edge | Resolve only `TS<digits>_*` filename refs; store unresolved refs verbatim with a `resolved=false` marker rather than dropping |
| Multipart/`oneOf`/`allOf` request bodies (e.g. CreateUEContext) | no single `request_schema` | `request_schema` is best-effort/nullable; capture the `jsonData` part when present; never fabricate |
| `externalDocs` missing/garbled on some files | can't derive version | Fall back to filename→spec_id; leave version empty and **skip** rather than guess (cite-or-silent doctrine) |
| forge unreachable / 403 (observed on `www.3gpp.org`) | fetch fails | Fetch step degrades visibly (corpus.sh policy); `jdegre/5GC_APIs` GitHub mirror as documented fallback |
| YAML size / count (~hundreds of files, large CommonData) | ingest time | Streaming parse, batch inserts; operations+schemas are far fewer rows than prose clauses — negligible vs the ~10M-clause projection |
| Tool-count creep vs "8 tools" rule (CLAUDE.md §5) | doctrine breach | One focused `search_api` + enrich existing tools; document the addition; could be folded into `search_spec` via `mode='api'` if the verdict requires staying at 8 |
| API doc version (`info.version` 1.3.4) confused with TS version (18.13.0) | wrong citation | Always cite the TS version from `externalDocs`; store `info.version` separately as `api_doc_version` |
| 29.5xx prose not in MVP series set on disk | no clause cross-link | 29 is already in the MVP series (CLAUDE.md §7); linkage §4.1 works once 29-series HTML is ingested, otherwise API rows stand alone (still fully citable) |

---

## 9. Doctrine compliance check

- Go-only, pure Go (yaml.v3), no CGO added, no Python, no Ollama on query path. OK.
- Local-first: network only in the offline fetch script; query path reads DuckDB. OK.
- Cite-or-silent: every API row is self-citing via `externalDocs` + forge SHA URL;
  unresolved/ambiguous data is skipped, never guessed. OK.
- Reproducible ingestion: pinned SHAs ⇒ stable hash. OK.
- Server returns fragments (operations/schemas), never summaries. OK.

---

## 10. This-axis scope statement

Deliverable for axis #2 is **this document only** (plus the optional companion
`scripts/research/02-fetch-5g-apis-probe.sh`). No changes to `go.mod`,
`internal/`, `cmd/`, `Makefile`, or `store/schema.sql` are made here; §3/§7 are
the proposal the implementation phase will follow.

---

## Sources

- 3GPP Forge 5G APIs repo: <https://forge.3gpp.org/rep/all/5G_APIs>
- README (raw): <https://forge.3gpp.org/rep/all/5G_APIs/raw/4e19b028fdfcdb026bc93640bad2064d3e9daa90/README.md>
- TS29571_CommonData.yaml (REL-18 raw): <https://forge.3gpp.org/rep/all/5G_APIs/-/raw/REL-18/TS29571_CommonData.yaml>
- TS29518_Namf_Communication.yaml (REL-18 raw): <https://forge.3gpp.org/rep/all/5G_APIs/-/raw/REL-18/TS29518_Namf_Communication.yaml>
- Branches view: <https://forge.3gpp.org/rep/all/5G_APIs/-/branches>
- 3GPP "OpenAPIs for the Service-Based Architecture": <https://www.3gpp.org/technologies/openapis-for-the-service-based-architecture>
- GitHub mirror (per-release branches, fallback): <https://github.com/jdegre/5GC_APIs>
