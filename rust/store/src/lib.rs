//! store-rs — the Rust DuckDB write layer for the 3GPP corpus.
//!
//! The write-side→Rust migration has Rust OWN ingestion/embedding/indexing and Go keep
//! read-only serve. This crate is the persistence half of that: it opens a `.duckdb`
//! read-write (duckdb-rs, bundled DuckDB 1.4.x — storage-compatible with the go-duckdb
//! v2.4.3 the serve side uses, proven by the rust-go-roundtrip CI), bootstraps the
//! SAME schema Go embeds (`internal/store/schema.sql`, single-sourced via include_str!),
//! and exposes the write + worklist methods the embedder and ingest need.
//!
//! It deliberately mirrors the Go `internal/store` write surface method-for-method so a
//! Rust-built corpus is byte-compatible with what Go would have built.

use anyhow::{Context, Result};
use duckdb::Connection;

/// BGE-M3 dense dimensionality — must match clauses.embedding FLOAT[1024].
pub const DENSE_DIM: usize = 1024;

/// The canonical schema, single-sourced from the Go-embedded DDL so the two runtimes
/// can never declare different tables. Idempotent (CREATE TABLE IF NOT EXISTS).
const SCHEMA_SQL: &str = include_str!("../../../internal/store/schema.sql");

/// The embeddable-text predicate: a clause with no real body (heading-only / void /
/// table-stripped) is skipped by the embedder, so it must not be counted as "needs a
/// vector". Mirrors Go store's embeddableTextSQL.
const EMBEDDABLE_TEXT_SQL: &str = "length(trim(text)) > 0";

/// Store wraps a read-write DuckDB connection over a corpus shard.
pub struct Store {
    conn: Connection,
}

/// A clause that still needs a dense vector: its id + the text inputs the embedder hashes.
pub struct WorkItem {
    pub chunk_id: u64,
    pub heading: String,
    pub text: String,
}

/// A clause to ingest (no vector yet — embedding/embedding_hash default NULL). Mirrors
/// the columns Go's InsertClauses writes; the Rust ingest builds these from the parser.
pub struct ClauseIn {
    pub chunk_id: u64,
    pub spec_id: String,
    pub release: String,
    pub version: String,
    pub clause_path: String,
    pub heading: String,
    pub text: String,
    pub is_normative: bool,
}

impl Store {
    /// open_rw opens (creating if absent) the DuckDB file read-write and bootstraps the
    /// schema idempotently.
    pub fn open_rw(path: &str) -> Result<Self> {
        let conn = Connection::open(path).with_context(|| format!("open duckdb rw {path}"))?;
        conn.execute_batch(SCHEMA_SQL).context("bootstrap schema")?;
        Ok(Self { conn })
    }

    /// in_memory opens a transient DB (tests).
    pub fn in_memory() -> Result<Self> {
        let conn = Connection::open_in_memory().context("open in-memory duckdb")?;
        conn.execute_batch(SCHEMA_SQL).context("bootstrap schema")?;
        Ok(Self { conn })
    }

    /// raw exposes the underlying connection for ad-hoc queries (read paths/tests).
    pub fn raw(&self) -> &Connection {
        &self.conn
    }

    /// set_meta upserts a schema_meta key (model id, hnsw_state, sparse_model, …).
    pub fn set_meta(&self, key: &str, value: &str) -> Result<()> {
        self.conn
            .execute(
                "INSERT INTO schema_meta(key, value) VALUES (?, ?)
                 ON CONFLICT (key) DO UPDATE SET value = excluded.value",
                duckdb::params![key, value],
            )
            .with_context(|| format!("set_meta {key}"))?;
        Ok(())
    }

    /// count_null_embeddings counts embeddable clauses still missing a vector — the
    /// completeness gate (== Go CountNullEmbeddings). 0 ⇒ this shard is fully embedded.
    pub fn count_null_embeddings(&self) -> Result<i64> {
        let n: i64 = self
            .conn
            .query_row(
                &format!(
                    "SELECT count(*) FROM clauses WHERE embedding IS NULL AND {EMBEDDABLE_TEXT_SQL}"
                ),
                [],
                |r| r.get(0),
            )
            .context("count_null_embeddings")?;
        Ok(n)
    }

    /// count_clauses returns the total clause count (== Go CountClauses).
    pub fn count_clauses(&self) -> Result<i64> {
        let n: i64 = self
            .conn
            .query_row("SELECT count(*) FROM clauses", [], |r| r.get(0))
            .context("count_clauses")?;
        Ok(n)
    }

    /// get_meta reads a schema_meta value ("" if absent).
    pub fn get_meta(&self, key: &str) -> Result<String> {
        let v: String = self
            .conn
            .query_row(
                "SELECT COALESCE(MAX(value), '') FROM schema_meta WHERE key = ?",
                duckdb::params![key],
                |r| r.get(0),
            )
            .with_context(|| format!("get_meta {key}"))?;
        Ok(v)
    }

    /// clauses_needing_embedding streams the work-list (never-embedded, embeddable
    /// clauses), oldest chunk first, capped at `limit` (0 = all). Mirrors the Go
    /// ClausesNeedingEmbedding ResumeOnly path so the Rust embedder can read the
    /// work-list straight from the DB instead of a JSONL bridge.
    pub fn clauses_needing_embedding(&self, limit: usize) -> Result<Vec<WorkItem>> {
        let mut sql = format!(
            "SELECT chunk_id, COALESCE(heading,''), COALESCE(text,'') FROM clauses
             WHERE embedding IS NULL AND {EMBEDDABLE_TEXT_SQL} ORDER BY chunk_id"
        );
        if limit > 0 {
            sql.push_str(&format!(" LIMIT {limit}"));
        }
        let mut stmt = self.conn.prepare(&sql).context("prepare worklist")?;
        let rows = stmt
            .query_map([], |r| {
                Ok(WorkItem {
                    chunk_id: r.get(0)?,
                    heading: r.get(1)?,
                    text: r.get(2)?,
                })
            })
            .context("query worklist")?;
        let mut out = Vec::new();
        for r in rows {
            out.push(r.context("scan worklist row")?);
        }
        Ok(out)
    }

    /// set_embeddings_batch writes (vector, hash) onto each clause in ONE transaction —
    /// the scalable write path (== Go SetEmbeddingsBatch). ids/vecs/hashes are parallel.
    pub fn set_embeddings_batch(
        &self,
        ids: &[u64],
        vecs: &[Vec<f32>],
        hashes: &[String],
    ) -> Result<()> {
        if ids.len() != vecs.len() || ids.len() != hashes.len() {
            anyhow::bail!(
                "set_embeddings_batch: parallel slice length mismatch {}/{}/{}",
                ids.len(),
                vecs.len(),
                hashes.len()
            );
        }
        if ids.is_empty() {
            return Ok(());
        }
        let mut sql = String::from("BEGIN;");
        for ((id, vec), hash) in ids.iter().zip(vecs).zip(hashes) {
            if vec.len() != DENSE_DIM {
                anyhow::bail!("clause {id}: vec dim {}, want {DENSE_DIM}", vec.len());
            }
            let list: String = vec
                .iter()
                .map(|x| x.to_string())
                .collect::<Vec<_>>()
                .join(",");
            // hash is 16 hex chars (sha256 prefix) — no escaping hazard, but quote-double
            // defensively in case a future identity contains a quote.
            let h = hash.replace('\'', "''");
            sql.push_str(&format!(
                "UPDATE clauses SET embedding = [{list}]::FLOAT[{DENSE_DIM}], embedding_hash = '{h}' WHERE chunk_id = {id};"
            ));
        }
        sql.push_str("COMMIT;");
        self.conn
            .execute_batch(&sql)
            .context("set_embeddings_batch")?;
        Ok(())
    }

    /// set_sparse replaces the sparse posting for one clause (idempotent: stale terms
    /// deleted first). `terms` are (term_id, weight) pairs. == Go SetSparse.
    pub fn set_sparse(&self, chunk_id: u64, terms: &[(u32, f32)]) -> Result<()> {
        let mut sql = format!("BEGIN; DELETE FROM clause_sparse WHERE chunk_id = {chunk_id};");
        for (term_id, weight) in terms {
            sql.push_str(&format!(
                "INSERT INTO clause_sparse(chunk_id, term_id, weight) VALUES ({chunk_id}, {term_id}, {weight});"
            ));
        }
        sql.push_str("COMMIT;");
        self.conn.execute_batch(&sql).context("set_sparse")?;
        Ok(())
    }

    /// enable_fts builds the BM25 FTS index over heading+text (best-effort; the caller
    /// degrades to LIKE if the extension is unavailable). == Go EnableFTS.
    pub fn enable_fts(&self) -> Result<()> {
        self.conn
            .execute_batch(
                "INSTALL fts; LOAD fts;
                 PRAGMA create_fts_index('clauses', 'chunk_id', 'heading', 'text', overwrite=1);",
            )
            .context("enable_fts")?;
        Ok(())
    }

    /// build_and_freeze_hnsw runs the non-negotiable build sequence (== Go
    /// BuildAndFreezeHNSW): CHECKPOINT → enable VSS → CREATE INDEX → CHECKPOINT →
    /// verify → freeze markers. `model` is stamped as embedding_model.
    pub fn build_and_freeze_hnsw(&self, model: &str) -> Result<()> {
        let count: i64 = self
            .conn
            .query_row(
                "SELECT count(*) FROM clauses WHERE embedding IS NOT NULL",
                [],
                |r| r.get(0),
            )
            .context("embedding count")?;
        if count == 0 {
            anyhow::bail!("no embeddings to index");
        }
        self.set_meta("hnsw_state", "building")?;
        self.conn
            .execute_batch(
                "CHECKPOINT;
                 INSTALL vss; LOAD vss; SET hnsw_enable_experimental_persistence = true;
                 CREATE INDEX IF NOT EXISTS clauses_hnsw ON clauses USING HNSW (embedding) WITH (metric = 'cosine');
                 CHECKPOINT;",
            )
            .context("build hnsw")?;
        for (k, v) in [
            ("hnsw_metric", "cosine"),
            ("embedding_dim", "1024"),
            ("embedding_count", &count.to_string()),
            ("embedding_model", model),
        ] {
            self.set_meta(k, v)?;
        }
        self.set_meta("hnsw_state", "frozen")?;
        Ok(())
    }

    /// checkpoint flushes the WAL into the file (so a separate reader sees a clean DB).
    pub fn checkpoint(&self) -> Result<()> {
        self.conn
            .execute_batch("CHECKPOINT;")
            .context("checkpoint")?;
        Ok(())
    }

    /// max_chunk_id returns the largest clauses.chunk_id (0 if empty) — used to offset a
    /// folded shard's synthetic PKs so two disjoint shards never collide on merge.
    pub fn max_chunk_id(&self) -> Result<u64> {
        let n: i64 = self
            .conn
            .query_row("SELECT COALESCE(max(chunk_id), 0) FROM clauses", [], |r| {
                r.get(0)
            })
            .context("max_chunk_id")?;
        Ok(n as u64)
    }

    /// fold_shard ATTACHes a per-series/-release shard DB read-only and folds its rows
    /// into this (merged) DB, offsetting clause chunk_ids by `offset` so disjoint shards
    /// keep distinct synthetic PKs (== Go cmd/merge fold). clause_sparse rides the same
    /// offset; catalogue rows (specs/versions/releases/acronyms) dedup on their PKs.
    /// Vectors are copied as-is; the caller rebuilds FTS + HNSW once on the merged DB
    /// (per-shard HNSW indexes are not concatenable — internal/store/CLAUDE.md).
    pub fn fold_shard(&self, shard_path: &str, offset: u64) -> Result<()> {
        let sql = format!(
            "ATTACH '{shard_path}' AS s (READ_ONLY);
             INSERT INTO specs SELECT * FROM s.specs ON CONFLICT DO NOTHING;
             INSERT INTO spec_versions SELECT * FROM s.spec_versions ON CONFLICT DO NOTHING;
             INSERT INTO releases SELECT * FROM s.releases ON CONFLICT DO NOTHING;
             INSERT INTO acronyms SELECT * FROM s.acronyms ON CONFLICT DO NOTHING;
             INSERT INTO clauses
               SELECT chunk_id + {offset}, spec_id, release, version, clause_path, heading, text,
                      is_normative, embedding, embedding_hash
               FROM s.clauses;
             INSERT INTO clause_sparse SELECT chunk_id + {offset}, term_id, weight FROM s.clause_sparse;
             INSERT INTO changes SELECT * FROM s.changes;
             INSERT INTO evolutions SELECT * FROM s.evolutions;
             DETACH s;"
        );
        self.conn
            .execute_batch(&sql)
            .with_context(|| format!("fold_shard {shard_path}"))?;
        Ok(())
    }

    // ---- ingest write surface (== Go store catalogue/clause writes) -----------------

    /// upsert_spec inserts/updates a spec catalogue row (== Go UpsertSpec).
    pub fn upsert_spec(
        &self,
        spec_id: &str,
        series: &str,
        title: &str,
        doc_type: &str,
        working_group: &str,
    ) -> Result<()> {
        self.conn
            .execute(
                "INSERT INTO specs(spec_id, series, title, doc_type, working_group)
                 VALUES (?, ?, ?, ?, ?)
                 ON CONFLICT (spec_id) DO UPDATE SET
                   series = excluded.series, title = excluded.title,
                   doc_type = excluded.doc_type, working_group = excluded.working_group",
                duckdb::params![spec_id, series, title, doc_type, working_group],
            )
            .with_context(|| format!("upsert_spec {spec_id}"))?;
        Ok(())
    }

    /// upsert_version inserts/ignores a (spec, release, version) row (== Go UpsertVersion).
    pub fn upsert_version(
        &self,
        spec_id: &str,
        release: &str,
        version: &str,
        docx_url: &str,
    ) -> Result<()> {
        self.conn
            .execute(
                "INSERT INTO spec_versions(spec_id, release, version, docx_url)
                 VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING",
                duckdb::params![spec_id, release, version, docx_url],
            )
            .with_context(|| format!("upsert_version {spec_id} {version}"))?;
        Ok(())
    }

    /// insert_clauses bulk-inserts vector-less clauses in one transaction (== Go
    /// InsertClauses). embedding + embedding_hash default NULL — the embed pass fills them.
    pub fn insert_clauses(&self, clauses: &[ClauseIn]) -> Result<()> {
        if clauses.is_empty() {
            return Ok(());
        }
        self.conn.execute_batch("BEGIN")?;
        {
            let mut stmt = self.conn.prepare(
                "INSERT INTO clauses(chunk_id, spec_id, release, version, clause_path, heading, text, is_normative)
                 VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
            )?;
            for c in clauses {
                stmt.execute(duckdb::params![
                    c.chunk_id,
                    c.spec_id,
                    c.release,
                    c.version,
                    c.clause_path,
                    c.heading,
                    c.text,
                    c.is_normative,
                ])
                .with_context(|| format!("insert clause {}", c.chunk_id))?;
            }
        }
        self.conn.execute_batch("COMMIT")?;
        Ok(())
    }

    /// ingest_done reports whether (spec, version) is already fully ingested under the SAME
    /// pipeline_version — the batch resume skip predicate (== Go ingest_log 'done' check).
    pub fn ingest_done(
        &self,
        spec_id: &str,
        version: &str,
        pipeline_version: &str,
    ) -> Result<bool> {
        let n: i64 = self
            .conn
            .query_row(
                "SELECT count(*) FROM ingest_log WHERE spec_id = ? AND version = ?
                 AND status = 'done' AND pipeline_version = ?",
                duckdb::params![spec_id, version, pipeline_version],
                |r| r.get(0),
            )
            .with_context(|| format!("ingest_done {spec_id} {version}"))?;
        Ok(n > 0)
    }

    /// log_ingest stamps the resume ledger (== Go ingest_log upsert). status is
    /// 'started' then 'done'; pipeline_version invalidates the log on an algorithm change.
    pub fn log_ingest(
        &self,
        spec_id: &str,
        version: &str,
        status: &str,
        pipeline_version: &str,
    ) -> Result<()> {
        self.conn
            .execute(
                "INSERT INTO ingest_log(spec_id, version, status, pipeline_version, started_at, completed_at)
                 VALUES (?, ?, ?, ?, now(), CASE WHEN ? = 'done' THEN now() ELSE NULL END)
                 ON CONFLICT (spec_id, version) DO UPDATE SET
                   status = excluded.status, pipeline_version = excluded.pipeline_version,
                   completed_at = CASE WHEN excluded.status = 'done' THEN now() ELSE ingest_log.completed_at END",
                duckdb::params![spec_id, version, status, pipeline_version, status],
            )
            .with_context(|| format!("log_ingest {spec_id} {version} {status}"))?;
        Ok(())
    }
}

/// A 5GC OpenAPI operation row (== Go model.APIOperation; op_id is assigned on insert).
pub struct ApiOperationIn {
    pub spec_id: String,
    pub release: String,
    pub version: String,
    pub api_doc_version: String,
    pub service: String,
    pub service_family: String,
    pub api_root: String,
    pub path: String,
    pub method: String,
    pub operation_id: String,
    pub summary: String,
    pub tags: Vec<String>,
    pub request_schema: String,
    pub response_codes: Vec<String>,
    pub yaml_file: String,
    pub forge_sha: String,
    pub forge_url: String,
}

/// A 5GC OpenAPI schema row (== Go model.APISchema; schema_id is assigned on insert).
pub struct ApiSchemaIn {
    pub spec_id: String,
    pub release: String,
    pub version: String,
    pub service: String,
    pub schema_name: String,
    pub kind: String,
    pub description: String,
    pub properties: Vec<String>,
    pub enum_values: Vec<String>,
    pub refs_out: Vec<String>,
    pub yaml_file: String,
    pub forge_sha: String,
    pub forge_url: String,
}

/// q quotes a string as a SQL literal (single-quote escaped).
fn q(s: &str) -> String {
    format!("'{}'", s.replace('\'', "''"))
}

/// lst renders a VARCHAR[] DuckDB list literal from strings.
fn lst(items: &[String]) -> String {
    let inner: Vec<String> = items.iter().map(|s| q(s)).collect();
    format!("[{}]", inner.join(", "))
}

impl Store {
    /// clear_api_tables removes the api_* rows only (additive overlay; never touches
    /// clauses — internal/openapi/CLAUDE.md). Run before a fresh OpenAPI ingest.
    pub fn clear_api_tables(&self) -> Result<()> {
        self.conn
            .execute_batch("DELETE FROM api_operations; DELETE FROM api_schemas;")
            .context("clear_api_tables")?;
        Ok(())
    }

    /// next_api_op_id / next_api_schema_id return the next free synthetic id.
    fn next_id(&self, table: &str, col: &str) -> Result<u64> {
        let n: i64 = self
            .conn
            .query_row(
                &format!("SELECT COALESCE(max({col}), 0) FROM {table}"),
                [],
                |r| r.get(0),
            )
            .with_context(|| format!("max {col}"))?;
        Ok(n as u64 + 1)
    }

    /// insert_api_operations writes operations in one transaction, assigning op_id from the
    /// current max+1 (== Go ingest-openapi).
    pub fn insert_api_operations(&self, ops: &[ApiOperationIn]) -> Result<()> {
        if ops.is_empty() {
            return Ok(());
        }
        let mut id = self.next_id("api_operations", "op_id")?;
        let mut sql = String::from("BEGIN;");
        for o in ops {
            sql.push_str(&format!(
                "INSERT INTO api_operations VALUES ({id}, {}, {}, {}, {}, {}, {}, {}, {}, {}, {}, {}, {}, {}, {}, {}, {}, {});",
                q(&o.spec_id), q(&o.release), q(&o.version), q(&o.api_doc_version), q(&o.service),
                q(&o.service_family), q(&o.api_root), q(&o.path), q(&o.method), q(&o.operation_id),
                q(&o.summary), lst(&o.tags), q(&o.request_schema), lst(&o.response_codes),
                q(&o.yaml_file), q(&o.forge_sha), q(&o.forge_url),
            ));
            id += 1;
        }
        sql.push_str("COMMIT;");
        self.conn
            .execute_batch(&sql)
            .context("insert_api_operations")?;
        Ok(())
    }

    /// insert_api_schemas writes schemas in one transaction, assigning schema_id from max+1.
    pub fn insert_api_schemas(&self, schemas: &[ApiSchemaIn]) -> Result<()> {
        if schemas.is_empty() {
            return Ok(());
        }
        let mut id = self.next_id("api_schemas", "schema_id")?;
        let mut sql = String::from("BEGIN;");
        for s in schemas {
            sql.push_str(&format!(
                "INSERT INTO api_schemas VALUES ({id}, {}, {}, {}, {}, {}, {}, {}, {}, {}, {}, {}, {}, {});",
                q(&s.spec_id), q(&s.release), q(&s.version), q(&s.service), q(&s.schema_name),
                q(&s.kind), q(&s.description), lst(&s.properties), lst(&s.enum_values),
                lst(&s.refs_out), q(&s.yaml_file), q(&s.forge_sha), q(&s.forge_url),
            ));
            id += 1;
        }
        sql.push_str("COMMIT;");
        self.conn
            .execute_batch(&sql)
            .context("insert_api_schemas")?;
        Ok(())
    }
}

/// A release-calendar row for the DynaReport overlay (== Go store.ReleaseRow). Dates are
/// "YYYY-MM-DD" or None.
pub struct ReleaseRow {
    pub code: String,
    pub name: String,
    pub status: String,
    pub start_date: Option<String>,
    pub freeze_date: Option<String>,
    pub freeze_meeting: String,
}

/// date_lit renders an "YYYY-MM-DD" option as a DuckDB DATE literal or NULL.
fn date_lit(d: &Option<String>) -> String {
    match d {
        Some(s) if !s.is_empty() => format!("DATE '{}'", s.replace('\'', "''")),
        _ => "NULL".to_string(),
    }
}

impl Store {
    /// upsert_releases replaces the release calendar (== Go UpsertReleases).
    pub fn upsert_releases(&self, releases: &[ReleaseRow]) -> Result<()> {
        if releases.is_empty() {
            return Ok(());
        }
        let mut sql = String::from("BEGIN;");
        for r in releases {
            sql.push_str(&format!(
                "INSERT INTO releases (code, name, status, start_date, freeze_date, freeze_meeting)
                 VALUES ({}, {}, {}, {}, {}, {})
                 ON CONFLICT (code) DO UPDATE SET name=excluded.name, status=excluded.status,
                   start_date=excluded.start_date, freeze_date=excluded.freeze_date,
                   freeze_meeting=excluded.freeze_meeting;",
                q(&r.code),
                q(&r.name),
                q(&r.status),
                date_lit(&r.start_date),
                date_lit(&r.freeze_date),
                q(&r.freeze_meeting),
            ));
        }
        sql.push_str("COMMIT;");
        self.conn.execute_batch(&sql).context("upsert_releases")?;
        Ok(())
    }

    /// apply_release_freeze stamps each spec_versions row with its release's freeze_date +
    /// status — the non-monotonic-ordering fix (== Go ApplyReleaseFreeze).
    pub fn apply_release_freeze(&self, releases: &[ReleaseRow]) -> Result<()> {
        let mut sql = String::from("BEGIN;");
        for r in releases {
            sql.push_str(&format!(
                "UPDATE spec_versions SET freeze_date = {}, status = {} WHERE release = {};",
                date_lit(&r.freeze_date),
                q(&r.status),
                q(&r.code),
            ));
        }
        sql.push_str("COMMIT;");
        self.conn
            .execute_batch(&sql)
            .context("apply_release_freeze")?;
        Ok(())
    }

    /// set_version_metadata_source tags spec_versions rows with their provenance (== Go).
    pub fn set_version_metadata_source(&self, source: &str) -> Result<()> {
        self.conn
            .execute(
                "UPDATE spec_versions SET metadata_source = ? WHERE metadata_source IS NULL",
                duckdb::params![source],
            )
            .context("set_version_metadata_source")?;
        Ok(())
    }

    /// update_spec_meta overlays authoritative title/doc_type/working_group onto an EXISTING
    /// spec row; an empty incoming value never clobbers a non-empty one (== Go UpdateSpecMeta).
    /// No-op if the spec is not on disk (cite-or-silent: never invents catalogue-only specs).
    pub fn update_spec_meta(
        &self,
        spec_id: &str,
        title: &str,
        doc_type: &str,
        wg: &str,
    ) -> Result<bool> {
        let n = self
            .conn
            .execute(
                "UPDATE specs SET
                   title         = CASE WHEN ? <> '' THEN ? ELSE title END,
                   doc_type      = CASE WHEN ? <> '' THEN ? ELSE doc_type END,
                   working_group = CASE WHEN ? <> '' THEN ? ELSE working_group END
                 WHERE spec_id = ?",
                duckdb::params![title, title, doc_type, doc_type, wg, wg, spec_id],
            )
            .with_context(|| format!("update_spec_meta {spec_id}"))?;
        Ok(n > 0)
    }

    /// upsert_acronym inserts/updates a glossary acronym (== Go UpsertAcronym). The PK is
    /// (term, expansion, domain) so the same term keeps every distinct expansion/domain.
    #[allow(clippy::too_many_arguments)]
    pub fn upsert_acronym(
        &self,
        term: &str,
        expansion: &str,
        domain: &str,
        first_release: &str,
        last_release: &str,
        source_series: &str,
    ) -> Result<()> {
        self.conn
            .execute(
                "INSERT INTO acronyms(term, expansion, domain, first_release, last_release, source_series)
                 VALUES (?, ?, ?, ?, ?, ?)
                 ON CONFLICT (term, expansion, domain) DO UPDATE SET
                   first_release = excluded.first_release, last_release = excluded.last_release,
                   source_series = excluded.source_series",
                duckdb::params![term, expansion, domain, first_release, last_release, source_series],
            )
            .with_context(|| format!("upsert_acronym {term}"))?;
        Ok(())
    }
}

/// LI registry rows for the asn1 subject (== Go li/asn1store). spec_id is always "33.128".
pub struct LiEventIn {
    pub interface: String,
    pub event_name: String,
    pub asn1_type: String,
    pub asn1_tag: i64,
    pub originating_nf: String,
    pub domain: String,
    pub spec_clause: String,
    pub field_count: i64,
}
pub struct LiFieldIn {
    pub interface: String,
    pub event_name: String,
    pub field_name: String,
    pub asn1_type: String,
    pub asn1_tag: i64,
    pub is_optional: bool,
    pub ordinal: i64,
}
pub struct LiNfClauseIn {
    pub originating_nf: String,
    pub interface: String,
    pub spec_clause: String,
}
pub struct Asn1TypeIn {
    pub type_name: String,
    pub kind: String,
    pub members_json: String,
}

impl Store {
    /// clear_li removes the LI registry rows for one (spec, release) — additive subject,
    /// idempotent re-ingest (== Go li store clear).
    pub fn clear_li(&self, spec_id: &str, release: &str) -> Result<()> {
        let w = format!(
            "WHERE spec_id = {} AND release = {}",
            q(spec_id),
            q(release)
        );
        self.conn
            .execute_batch(&format!(
                "DELETE FROM li_events {w}; DELETE FROM li_event_fields {w};
                 DELETE FROM li_nf_clauses {w}; DELETE FROM asn1_types {w};"
            ))
            .context("clear_li")?;
        Ok(())
    }

    /// write_li_registry writes the parsed TS 33.128 LI registry (events + fields +
    /// nf-clauses + the full asn1 type catalogue) for one (spec, release) in one transaction.
    pub fn write_li_registry(
        &self,
        spec_id: &str,
        release: &str,
        module_version: &str,
        events: &[LiEventIn],
        fields: &[LiFieldIn],
        nf_clauses: &[LiNfClauseIn],
        types: &[Asn1TypeIn],
    ) -> Result<()> {
        let mut sql = String::from("BEGIN;");
        for e in events {
            sql.push_str(&format!(
                "INSERT INTO li_events VALUES ({}, {}, {}, {}, {}, {}, {}, {}, {}, {}, {});",
                q(spec_id),
                q(release),
                q(module_version),
                q(&e.interface),
                q(&e.event_name),
                q(&e.asn1_type),
                e.asn1_tag,
                q(&e.originating_nf),
                q(&e.domain),
                q(&e.spec_clause),
                e.field_count,
            ));
        }
        for f in fields {
            sql.push_str(&format!(
                "INSERT INTO li_event_fields VALUES ({}, {}, {}, {}, {}, {}, {}, {}, {});",
                q(spec_id),
                q(release),
                q(&f.interface),
                q(&f.event_name),
                q(&f.field_name),
                q(&f.asn1_type),
                f.asn1_tag,
                f.is_optional,
                f.ordinal,
            ));
        }
        for c in nf_clauses {
            sql.push_str(&format!(
                "INSERT INTO li_nf_clauses VALUES ({}, {}, {}, {}, {});",
                q(spec_id),
                q(release),
                q(&c.originating_nf),
                q(&c.interface),
                q(&c.spec_clause),
            ));
        }
        for t in types {
            sql.push_str(&format!(
                "INSERT INTO asn1_types VALUES ({}, {}, {}, {}, {});",
                q(spec_id),
                q(release),
                q(&t.type_name),
                q(&t.kind),
                q(&t.members_json),
            ));
        }
        sql.push_str("COMMIT;");
        self.conn.execute_batch(&sql).context("write_li_registry")?;
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn schema_bootstraps_and_meta_roundtrips() {
        let s = Store::in_memory().unwrap();
        s.set_meta("embedding_model", "id-xyz").unwrap();
        let v: String = s
            .raw()
            .query_row(
                "SELECT value FROM schema_meta WHERE key='embedding_model'",
                [],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(v, "id-xyz");
    }

    #[test]
    fn embeddings_and_sparse_write_and_count() {
        let s = Store::in_memory().unwrap();
        s.raw()
            .execute_batch(
                "INSERT INTO clauses(chunk_id,spec_id,heading,text) VALUES
                 (1,'23.501','a','alpha'),(2,'23.501','b','beta'),(3,'23.501','c','');",
            )
            .unwrap();
        // clause 3 has empty text → not embeddable.
        assert_eq!(s.count_null_embeddings().unwrap(), 2);
        let wl = s.clauses_needing_embedding(0).unwrap();
        assert_eq!(wl.len(), 2);

        let v = vec![0.1f32; DENSE_DIM];
        s.set_embeddings_batch(
            &[1, 2],
            &[v.clone(), v.clone()],
            &["h1".into(), "h2".into()],
        )
        .unwrap();
        assert_eq!(s.count_null_embeddings().unwrap(), 0);

        s.set_sparse(1, &[(10, 0.5), (20, 1.5)]).unwrap();
        let n: i64 = s
            .raw()
            .query_row(
                "SELECT count(*) FROM clause_sparse WHERE chunk_id=1",
                [],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(n, 2);
    }

    #[test]
    fn fold_shard_offsets_chunk_ids_and_dedups_catalogue() {
        // Two disjoint shards on disk, each reusing chunk_ids 1..2 and the same spec.
        let base = std::env::temp_dir().join(format!("storers-fold-{}", std::process::id()));
        let mk = |suffix: &str, sp: &str| {
            let p = base.join(suffix);
            std::fs::create_dir_all(&base).unwrap();
            let _ = std::fs::remove_file(&p);
            let path = p.to_str().unwrap().to_string();
            let s = Store::open_rw(&path).unwrap();
            s.raw()
                .execute_batch(&format!(
                    "INSERT INTO specs(spec_id,series,doc_type) VALUES ('{sp}','23','TS');
                     INSERT INTO clauses(chunk_id,spec_id,heading,text) VALUES
                       (1,'{sp}','h1','t1'),(2,'{sp}','h2','t2');"
                ))
                .unwrap();
            s.checkpoint().unwrap();
            path
        };
        let a = mk("a.duckdb", "23.501");
        let b = mk("b.duckdb", "23.502");

        let merged = base.join("merged.duckdb").to_str().unwrap().to_string();
        let _ = std::fs::remove_file(&merged);
        let m = Store::open_rw(&merged).unwrap();
        for shard in [&a, &b] {
            let off = m.max_chunk_id().unwrap();
            m.fold_shard(shard, off).unwrap();
        }
        // 2 distinct specs, 4 clauses with no chunk_id collision.
        let specs: i64 = m
            .raw()
            .query_row("SELECT count(*) FROM specs", [], |r| r.get(0))
            .unwrap();
        let clauses: i64 = m
            .raw()
            .query_row("SELECT count(*) FROM clauses", [], |r| r.get(0))
            .unwrap();
        let distinct: i64 = m
            .raw()
            .query_row("SELECT count(DISTINCT chunk_id) FROM clauses", [], |r| {
                r.get(0)
            })
            .unwrap();
        assert_eq!(specs, 2, "specs deduped/kept");
        assert_eq!(clauses, 4, "all clauses folded");
        assert_eq!(distinct, 4, "chunk_ids offset, no collision");
        let _ = std::fs::remove_dir_all(&base);
    }

    #[test]
    fn ingest_write_surface_roundtrips() {
        let s = Store::in_memory().unwrap();
        s.upsert_spec("23.501", "23", "System architecture", "TS", "SA2")
            .unwrap();
        // idempotent upsert updates in place.
        s.upsert_spec(
            "23.501",
            "23",
            "System architecture and procedures",
            "TS",
            "SA2",
        )
        .unwrap();
        s.upsert_version("23.501", "Rel-19", "19.0.0", "http://x/23501.zip")
            .unwrap();
        s.insert_clauses(&[
            ClauseIn {
                chunk_id: 1,
                spec_id: "23.501".into(),
                release: "Rel-19".into(),
                version: "19.0.0".into(),
                clause_path: "5.1".into(),
                heading: "5.1".into(),
                text: "alpha".into(),
                is_normative: true,
            },
            ClauseIn {
                chunk_id: 2,
                spec_id: "23.501".into(),
                release: "Rel-19".into(),
                version: "19.0.0".into(),
                clause_path: "5.2".into(),
                heading: "5.2".into(),
                text: "beta".into(),
                is_normative: false,
            },
        ])
        .unwrap();
        s.log_ingest("23.501", "19.0.0", "started", "html-v2|clause-leaf-v1|1")
            .unwrap();
        s.log_ingest("23.501", "19.0.0", "done", "html-v2|clause-leaf-v1|1")
            .unwrap();

        let specs: i64 = s
            .raw()
            .query_row("SELECT count(*) FROM specs", [], |r| r.get(0))
            .unwrap();
        let title: String = s
            .raw()
            .query_row("SELECT title FROM specs WHERE spec_id='23.501'", [], |r| {
                r.get(0)
            })
            .unwrap();
        let clauses: i64 = s
            .raw()
            .query_row("SELECT count(*) FROM clauses", [], |r| r.get(0))
            .unwrap();
        let done: String = s
            .raw()
            .query_row(
                "SELECT status FROM ingest_log WHERE spec_id='23.501'",
                [],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(specs, 1);
        assert_eq!(
            title, "System architecture and procedures",
            "upsert updated in place"
        );
        assert_eq!(clauses, 2);
        assert_eq!(
            s.count_null_embeddings().unwrap(),
            2,
            "fresh clauses need vectors"
        );
        assert_eq!(done, "done");
    }
}
