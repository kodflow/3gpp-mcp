-- 3gpp-mcp DuckDB schema (CLAUDE.md §4). Idempotent: safe to re-apply.
-- FTS (BM25) and VSS (HNSW) indexes are NOT created here — they need optional
-- extensions and are built post-load, best-effort, by the store (degrade,
-- don't block: lexical SQL still works if an extension can't load).

CREATE TABLE IF NOT EXISTS specs (
    spec_id       VARCHAR PRIMARY KEY,   -- '23.501'
    series        VARCHAR,               -- '23'
    title         VARCHAR,
    doc_type      VARCHAR,               -- 'TS' | 'TR'
    working_group VARCHAR                -- 'SA2'
);

CREATE TABLE IF NOT EXISTS spec_versions (
    spec_id      VARCHAR,
    release      VARCHAR,                -- 'Rel-18'
    version      VARCHAR,                -- '18.6.0'
    freeze_date  DATE,
    docx_url     VARCHAR,
    status          VARCHAR,             -- release status: Open|Frozen|Closed (axis #3)
    metadata_source VARCHAR,             -- 'dynareport' | 'filename' (provenance, axis #3)
    PRIMARY KEY (spec_id, release, version)
);

-- Authoritative release calendar from portal Releases.aspx (axis #3). freeze_date
-- is the functional-freeze "End date"; non-monotonic version ordering keys on it
-- (CLAUDE.md §8 piège #3). NULL freeze_date = Open/planned release.
CREATE TABLE IF NOT EXISTS releases (
    code           VARCHAR PRIMARY KEY,  -- 'Rel-19'
    name           VARCHAR,              -- 'Release 19'
    status         VARCHAR,              -- 'Open' | 'Frozen' | 'Closed'
    start_date     DATE,
    freeze_date    DATE,                 -- Releases.aspx 'End date' (functional freeze)
    freeze_meeting VARCHAR               -- 'SA#110'
);

CREATE TABLE IF NOT EXISTS clauses (
    chunk_id     UBIGINT PRIMARY KEY,
    spec_id      VARCHAR,
    release      VARCHAR,
    version      VARCHAR,
    clause_path  VARCHAR,                -- '6.2.3.1'
    heading      VARCHAR,
    text         VARCHAR,
    is_normative BOOLEAN,
    embedding    FLOAT[1024],            -- NULL until embeddings (Phase 4)
    embedding_hash VARCHAR               -- sha(heading+text+model); NULL until embedded.
                                         -- Lets the decoupled embed step re-embed ONLY
                                         -- clauses whose text or model changed (micro-granular).
);
CREATE INDEX IF NOT EXISTS clauses_spec   ON clauses (spec_id);
CREATE INDEX IF NOT EXISTS clauses_rel    ON clauses (release);
CREATE INDEX IF NOT EXISTS clauses_path   ON clauses (spec_id, clause_path);

-- BGE-M3 SPARSE (learned-lexical) weights: one row per (clause, vocab token id).
-- The sparse arm scores a query's term weights against these as an inverted-index
-- dot product (SQL GROUP BY on term_id) — DuckDB has no native sparse-vector index,
-- so the term_id index below IS the inverted index. Populated only when a
-- sparse-capable embedder runs; absent → the sparse arm is simply not offered
-- (degrade, never block — CLAUDE.md §1). Weights are raw ReLU(linear) floats, never
-- normalized; score(clause) = Σ query_weight · clause_weight over shared term_id.
CREATE TABLE IF NOT EXISTS clause_sparse (
    chunk_id  UBIGINT,
    term_id   UINTEGER,   -- XLM-RoBERTa vocab token id (bge-m3 tokenizer)
    weight    FLOAT,
    PRIMARY KEY (chunk_id, term_id)
);
CREATE INDEX IF NOT EXISTS clause_sparse_term ON clause_sparse (term_id);

CREATE TABLE IF NOT EXISTS changes (
    cr_number    VARCHAR,
    cr_revision  INTEGER,
    spec_id      VARCHAR,
    from_version VARCHAR,
    to_version   VARCHAR,
    meeting      VARCHAR,
    category     VARCHAR,                -- A|B|C|D|F
    clauses      VARCHAR[],
    summary      VARCHAR,
    tdoc_url     VARCHAR
);
CREATE INDEX IF NOT EXISTS changes_spec ON changes (spec_id);
CREATE INDEX IF NOT EXISTS changes_cr   ON changes (cr_number);

-- One term can have several valid expansions (e.g. AMF = Access and Mobility
-- Management Function in 5GC, but Authentication Management Field in security);
-- we keep them ALL and let the client disambiguate by context (CLAUDE.md §8.5).
CREATE TABLE IF NOT EXISTS acronyms (
    term          VARCHAR,
    expansion     VARCHAR,
    domain        VARCHAR,               -- '5GC'|'EPC'|'IMS'|'RAN'|''
    first_release VARCHAR,
    last_release  VARCHAR,
    source_series VARCHAR,               -- owning subject's 3GPP series (e.g. '21' for glossary)
    PRIMARY KEY (term, expansion, domain)
);

CREATE TABLE IF NOT EXISTS evolutions (
    from_term            VARCHAR,
    to_term              VARCHAR,
    evolution_type       VARCHAR,        -- SPLIT|MERGE|RENAME|REPLACED_BY|EXTENDED_BY
    justification_spec   VARCHAR,
    justification_clause VARCHAR,
    confidence           FLOAT
);

-- Authoritative LI event registry, parsed from TS 33.128 ASN.1
-- (TS33128Payloads.asn) per release — supersedes prose/heading mining.
CREATE TABLE IF NOT EXISTS li_events (
    spec_id        VARCHAR,            -- always '33.128'
    release        VARCHAR,            -- 'Rel-18' (from module OID)
    module_version VARCHAR,            -- 'r18 version14' (provenance)
    interface      VARCHAR,            -- 'X2'|'X3'|'HI2'|'HI4'
    event_name     VARCHAR,            -- CHOICE member, 'registration'
    asn1_type      VARCHAR,            -- referenced type, 'AMFRegistration'
    asn1_tag       INTEGER,
    originating_nf VARCHAR,            -- 'AMF'
    domain         VARCHAR,            -- '5GC'|'EPC'|'IMS'
    spec_clause    VARCHAR,            -- '6.2.2.2'
    field_count    INTEGER,
    PRIMARY KEY (spec_id, release, interface, event_name)
);
CREATE INDEX IF NOT EXISTS li_events_nf    ON li_events (originating_nf);
CREATE INDEX IF NOT EXISTS li_events_rel   ON li_events (release);

CREATE TABLE IF NOT EXISTS li_event_fields (
    spec_id     VARCHAR,
    release     VARCHAR,
    interface   VARCHAR,
    event_name  VARCHAR,
    field_name  VARCHAR,
    asn1_type   VARCHAR,
    asn1_tag    INTEGER,
    is_optional BOOLEAN,
    ordinal     INTEGER,
    PRIMARY KEY (spec_id, release, interface, event_name, field_name)
);

CREATE TABLE IF NOT EXISTS li_nf_clauses (
    spec_id        VARCHAR,
    release        VARCHAR,
    originating_nf VARCHAR,
    interface      VARCHAR,
    spec_clause    VARCHAR,
    PRIMARY KEY (spec_id, release, originating_nf, interface)
);

-- 5GC OpenAPI operations, parsed from the canonical YAML on 3GPP Forge
-- (axis #2). The synthetic "clause" of an operation is its Locator() string.
CREATE TABLE IF NOT EXISTS api_operations (
    op_id           UBIGINT PRIMARY KEY,
    spec_id         VARCHAR,            -- '29.518' (from externalDocs)
    release         VARCHAR,            -- 'Rel-18' (resolved ref)
    version         VARCHAR,            -- '18.13.0' (TS version)
    api_doc_version VARCHAR,            -- info.version (informative)
    service         VARCHAR,            -- 'Namf_Communication'
    service_family  VARCHAR,            -- 'Namf'
    api_root        VARCHAR,            -- 'namf-comm/v1'
    path            VARCHAR,            -- '/ue-contexts/{ueContextId}'
    method          VARCHAR,            -- 'PUT'
    operation_id    VARCHAR,            -- 'CreateUEContext'
    summary         VARCHAR,
    tags            VARCHAR[],
    request_schema  VARCHAR,            -- best-effort, may be ''
    response_codes  VARCHAR[],
    yaml_file       VARCHAR,
    forge_sha       VARCHAR,
    forge_url       VARCHAR
);
CREATE INDEX IF NOT EXISTS api_ops_spec    ON api_operations (spec_id, release);
CREATE INDEX IF NOT EXISTS api_ops_service ON api_operations (service);
CREATE INDEX IF NOT EXISTS api_ops_opid    ON api_operations (operation_id);

-- 5GC OpenAPI schemas / IEs (one row per components/schemas entry).
CREATE TABLE IF NOT EXISTS api_schemas (
    schema_id    UBIGINT PRIMARY KEY,
    spec_id      VARCHAR,
    release      VARCHAR,
    version      VARCHAR,
    service      VARCHAR,
    schema_name  VARCHAR,
    kind         VARCHAR,               -- object|enum|string|array|...
    description  VARCHAR,
    properties   VARCHAR[],
    enum_values  VARCHAR[],
    refs_out     VARCHAR[],             -- ['29.518:UeContextCreateData', ...]
    yaml_file    VARCHAR,
    forge_sha    VARCHAR,
    forge_url    VARCHAR
);
CREATE INDEX IF NOT EXISTS api_sch_spec ON api_schemas (spec_id, release);
CREATE INDEX IF NOT EXISTS api_sch_name ON api_schemas (schema_name);

-- Structured ASN.1 type catalogue parsed (pure-Go, no Python — CLAUDE.md §13)
-- from TS 33.128's TS33128Payloads.asn (axis #8). Every IE (SEQUENCE/CHOICE/
-- ENUMERATED/…) is a citable object; members keep the recursive structure as
-- JSON so search/resolve can inspect individual fields without N tables.
CREATE TABLE IF NOT EXISTS asn1_types (
    spec_id    VARCHAR,                  -- '33.128'
    release    VARCHAR,                  -- 'Rel-18' (from module OID)
    type_name  VARCHAR,                  -- 'AMFRegistration'
    kind       VARCHAR,                  -- 'SEQUENCE'|'CHOICE'|'ENUMERATED'|...
    members    VARCHAR,                  -- JSON [{name,type,tag,optional}]
    PRIMARY KEY (spec_id, release, type_name)
);
CREATE INDEX IF NOT EXISTS asn1_types_name ON asn1_types (type_name);

-- bookkeeping for migrations + ingest provenance
CREATE TABLE IF NOT EXISTS schema_meta (
    key   VARCHAR PRIMARY KEY,
    value VARCHAR
);

-- Resume checkpoint (axis #15: --resume). One row per (spec, version) tuple
-- the ingest has touched. status='started' means a row was inserted but the
-- spec wasn't fully ingested (the runner died mid-spec); status='done' means
-- every step for that spec finished. On resume, the ingest skips 'done' rows
-- and PURGES + re-ingests 'started' ones so a half-written spec can't poison
-- the merged corpus. pipeline_version stamps every row so an algorithm change
-- (new chunker / new embedder) invalidates the whole log and forces a rebuild.
CREATE TABLE IF NOT EXISTS ingest_log (
    spec_id          VARCHAR,
    version          VARCHAR,
    status           VARCHAR,   -- 'started' | 'done'
    pipeline_version VARCHAR,
    started_at       TIMESTAMP,
    completed_at     TIMESTAMP,
    PRIMARY KEY (spec_id, version)
);
