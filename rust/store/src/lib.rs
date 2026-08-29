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

// identity is now its own CGO-free crate (so discover can share these golden-matched
// digests without libduckdb); re-exported here so `store_rs::identity::*` and
// `crate::identity::*` paths stay valid.
use anyhow::{Context, Result};
use duckdb::Connection;
pub use identity3gpp as identity;

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
/// The three `CREATE INDEX ... ON clauses` statements in schema.sql are bracketed
/// by these markers. They are the only part of the schema that cannot be applied
/// to a converted corpus, and the markers live in the file both languages read,
/// so this and internal/store.schemaFor strip exactly the same statements.
const CLAUSE_INDEX_BEGIN: &str = "-- @clauses-indexes-begin";
const CLAUSE_INDEX_END: &str = "-- @clauses-indexes-end";

/// schema_for returns the schema to apply, minus the `clauses` indexes when that
/// name resolves to a VIEW.
///
/// After ADR 0004's --drop-clauses the corpus serves `clauses` as a view over the
/// occurrences, and DuckDB refuses to index a view:
///
/// ```text
/// Binder Error: can only create an index on a base table
/// ```
///
/// execute_batch is all-or-nothing, so without this EVERY write-side tool dies at
/// bootstrap on a converted corpus, before reading a row — `freeze-hnsw` included,
/// whose entire job is to index a corpus in exactly that state.
///
/// The other tools must not run on a converted corpus at all: the pipeline calls
/// `migrate-paragraphs --restore` first (see internal/goal). This is not that
/// guarantee, and does not try to be. It is the narrower rule that you cannot put
/// an index on a view.
fn schema_for(conn: &Connection) -> String {
    let is_view: bool = conn
        .query_row(
            "SELECT count(*) > 0 FROM duckdb_views()
              WHERE database_name = current_database() AND schema_name = 'main'
                AND view_name = 'clauses'",
            [],
            |r| r.get(0),
        )
        .unwrap_or(false);
    if !is_view {
        return SCHEMA_SQL.to_string();
    }
    match (
        SCHEMA_SQL.find(CLAUSE_INDEX_BEGIN),
        SCHEMA_SQL.find(CLAUSE_INDEX_END),
    ) {
        (Some(i), Some(j)) if j >= i => {
            format!(
                "{}{}",
                &SCHEMA_SQL[..i],
                &SCHEMA_SQL[j + CLAUSE_INDEX_END.len()..]
            )
        }
        _ => SCHEMA_SQL.to_string(),
    }
}

impl Store {
    /// open_rw opens (creating if absent) the DuckDB file read-write and bootstraps the
    /// schema idempotently.
    pub fn open_rw(path: &str) -> Result<Self> {
        let conn = Connection::open(path).with_context(|| format!("open duckdb rw {path}"))?;
        conn.execute_batch(&schema_for(&conn))
            .context("bootstrap schema")?;

        // A WRITER MUST BE ABLE TO BIND AN HNSW INDEX THAT IS ALREADY THERE.
        //
        // Once `clauses` carries a frozen HNSW, DuckDB refuses to modify the table
        // — or even to CHECKPOINT — unless the extension providing that index type
        // is loaded: "Cannot bind index 'clauses', unknown index type 'HNSW'. You
        // need to load the extension ... before table 'clauses' can be modified."
        //
        // A first run never sees this: the index does not exist yet. It appears on
        // the SECOND pass, which is exactly the incremental path this pipeline is
        // built for — ETSI re-ingesting into an already-indexed etsi.duckdb hit it.
        //
        // Best-effort on purpose: a build without vss must still open a corpus that
        // has no HNSW. If the index IS there and vss is missing, the later write
        // fails with DuckDB's own message, which says precisely what is wrong.
        let _ = conn.execute_batch(
            "INSTALL vss; LOAD vss; SET hnsw_enable_experimental_persistence = true;",
        );
        Ok(Self { conn })
    }

    /// copy_database_compact builds `dst` as a COMPACT copy of `src`.
    ///
    /// merge used to clone the base with std::fs::copy — a byte-for-byte copy, so it
    /// carried the previous run's dead space AND the stale HNSW index that the merge is
    /// about to rebuild anyway. A bucket replacement is delete-then-insert and DuckDB
    /// does not reclaim in place, so the file grew on every incremental run and, because
    /// each run started from the last run's file, the growth COMPOUNDED: 38.5 GB in,
    /// 133 GB out on the 2026-08-25 repair, and the next run would have started at 133.
    ///
    /// COPY FROM DATABASE rebuilds the storage rather than cloning it: no dead space
    /// carried, and custom indexes are not copied (internal/store/CLAUDE.md) — which is
    /// exactly right here, since merge rebuilds FTS and freeze-hnsw rebuilds the index.
    ///
    /// Both databases are attached under explicit aliases from a scratch in-memory
    /// connection, so neither catalog name has to be guessed from a file stem — "3gpp.
    /// duckdb.new" does not yield an identifier anyone should have to predict.
    pub fn copy_database_compact(src: &str, dst: &str) -> Result<()> {
        let conn = Connection::open_in_memory().context("scratch connection")?;
        // The source carries an HNSW index; without vss its catalog cannot even be bound.
        // Best-effort, exactly as open_rw does it.
        let _ = conn.execute_batch(
            "INSTALL vss; LOAD vss; SET hnsw_enable_experimental_persistence = true;",
        );

        // A MEMORY LIMIT WITHOUT A TEMP DIRECTORY IS A GUARANTEED OOM, NOT A GUARD.
        //
        // This connection is in-memory, and an in-memory DuckDB has NOWHERE to spill:
        // capping the buffer manager without giving it a temp directory does not make
        // the copy careful, it makes the copy die. That is exactly what happened —
        // 70 minutes of CPU, nothing written, then
        //
        //   Failed to create checkpoint: Out of Memory Error: could not allocate block
        //   of size 256.0 KiB (3.7 GiB/3.7 GiB used)
        //
        // against a 4 GB cap. So point temp_directory at the DESTINATION's own folder
        // (the volume that must have room for the result anyway) before capping
        // anything, and give the cap enough headroom that spilling is the exception.
        let tmp = std::path::Path::new(dst)
            .parent()
            .filter(|p| !p.as_os_str().is_empty())
            .map(|p| p.join("compact-copy.tmp"))
            .unwrap_or_else(|| std::path::PathBuf::from("compact-copy.tmp"));
        let buf = std::env::var("MERGE_COPY_MEMORY_LIMIT").unwrap_or_else(|_| "12GB".into());

        let esc = |p: &str| p.replace('\'', "''");

        // COPY FROM DATABASE WOULD BRING THE FTS INDEX ACROSS, AND MERGE REBUILDS IT.
        //
        // It copies every schema, including the six internal tables DuckDB's fts
        // extension keeps under fts_main_clauses — a BM25 index over 2.75 M clauses.
        // merge then calls enable_fts(), whose PRAGMA is overwrite=1, so all of that is
        // dropped and built again. The corpus pays for the same index twice and the
        // copy carries gigabytes it will throw away.
        //
        // So copy the MAIN schema table by table instead. The table list is read from
        // the source's own catalogue rather than hard-coded, so a new table cannot be
        // silently left behind the way a stale list would leave it.
        //
        // The secondary indexes come off for the duration: maintaining three ART
        // indexes across 2.75 M row-by-row inserts costs far more than building them
        // once at the end over settled data.
        {
            let d = Self::open_rw(dst).context("bootstrap destination schema")?;
            d.conn.execute_batch(
                "DROP INDEX IF EXISTS clauses_spec;
                 DROP INDEX IF EXISTS clauses_rel;
                 DROP INDEX IF EXISTS clauses_path;",
            )?;
            d.checkpoint()?;
        }

        let res = (|| -> Result<()> {
            conn.execute_batch(&format!(
                "SET temp_directory = '{}';
                 SET memory_limit = '{buf}';
                 SET preserve_insertion_order = false;
                 ATTACH '{}' AS copy_src (READ_ONLY);
                 ATTACH '{}' AS copy_dst;",
                esc(&tmp.to_string_lossy()),
                esc(src),
                esc(dst)
            ))
            .context("attach for compact copy")?;

            let tables: Vec<String> = {
                let mut st = conn.prepare(
                    "SELECT table_name FROM duckdb_tables()
                      WHERE database_name = 'copy_src' AND schema_name = 'main'
                      ORDER BY table_name",
                )?;
                let rows = st.query_map([], |r| r.get::<_, String>(0))?;
                rows.filter_map(std::result::Result::ok).collect()
            };
            for t in &tables {
                // Column order is identical — both sides were bootstrapped from the same
                // schema.sql — so SELECT * is the right shape, not a shortcut.
                conn.execute_batch(&format!(
                    "INSERT INTO copy_dst.main.\"{t}\" SELECT * FROM copy_src.main.\"{t}\";"
                ))
                .with_context(|| format!("copy table {t}"))?;
            }
            conn.execute_batch("DETACH copy_dst; DETACH copy_src;")?;
            Ok(())
        })();

        let _ = std::fs::remove_dir_all(&tmp);
        res.with_context(|| format!("compact copy {src} -> {dst}"))?;

        // Rebuild the secondary indexes once, over data that is now settled.
        let d = Self::open_rw(dst).context("reopen destination")?;
        d.conn.execute_batch(
            "CREATE INDEX IF NOT EXISTS clauses_spec ON clauses (spec_id);
             CREATE INDEX IF NOT EXISTS clauses_rel  ON clauses (release);
             CREATE INDEX IF NOT EXISTS clauses_path ON clauses (spec_id, clause_path);",
        )?;
        d.checkpoint()?;
        Ok(())
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

    /// series_in_db returns the DISTINCT series present (drives subject-footprint advance).
    pub fn series_in_db(&self) -> Result<Vec<String>> {
        let mut st = self
            .conn
            .prepare("SELECT DISTINCT series FROM specs WHERE series IS NOT NULL")?;
        let rows = st.query_map([], |r| r.get::<_, String>(0))?;
        Ok(rows.filter_map(std::result::Result::ok).collect())
    }

    /// spec_versions returns every (spec_id, release, version) row (drives corpus-index).
    pub fn spec_versions(&self) -> Result<Vec<(String, String, String)>> {
        let mut st = self
            .conn
            .prepare("SELECT spec_id, release, version FROM spec_versions")?;
        let rows = st.query_map([], |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?)))?;
        Ok(rows.filter_map(std::result::Result::ok).collect())
    }

    /// shard_spec_releases lists a shard's (spec_id, release) buckets — for --base, these
    /// REPLACE the base's copy (== Go merge incremental bucket replacement).
    pub fn shard_spec_releases(&self, shard_path: &str) -> Result<Vec<(String, String)>> {
        self.conn.execute_batch(&format!(
            "ATTACH '{}' AS shard (READ_ONLY)",
            shard_path.replace('\'', "''")
        ))?;
        let out = {
            let mut st = self
                .conn
                .prepare("SELECT DISTINCT spec_id, release FROM shard.spec_versions")?;
            let rows =
                st.query_map([], |r| Ok((r.get::<_, String>(0)?, r.get::<_, String>(1)?)))?;
            rows.filter_map(std::result::Result::ok).collect::<Vec<_>>()
        };
        self.conn.execute_batch("DETACH shard")?;
        Ok(out)
    }

    /// shard_series lists a shard's DISTINCT series (the "rebuilt" set for the subject
    /// footprint advance decision — a subject advances iff ALL its series were rebuilt).
    pub fn shard_series(&self, shard_path: &str) -> Result<Vec<String>> {
        self.conn.execute_batch(&format!(
            "ATTACH '{}' AS sh2 (READ_ONLY)",
            shard_path.replace('\'', "''")
        ))?;
        let out = {
            let mut st = self
                .conn
                .prepare("SELECT DISTINCT series FROM sh2.specs WHERE series IS NOT NULL")?;
            let rows = st.query_map([], |r| r.get::<_, String>(0))?;
            rows.filter_map(std::result::Result::ok).collect::<Vec<_>>()
        };
        self.conn.execute_batch("DETACH sh2")?;
        Ok(out)
    }

    /// delete_spec_release purges one (spec_id, release) bucket from the merged DB so a
    /// fresher shard can replace it (== Go merge --base bucket replacement).
    /// stash_bucket_vectors snapshots the vectors of the given (spec, release) buckets,
    /// keyed by the TEXT they describe, so a bucket replacement can hand them back to
    /// every clause whose wording did not change. Returns the number of distinct texts
    /// kept. Call it BEFORE delete_spec_release, and restore_stashed_vectors after the
    /// fold.
    ///
    /// Without it, re-ingesting a spec whose text is identical still throws its vectors
    /// away: the shard carries no embeddings, the delete takes the base's with it, and
    /// the next embed pass pays the GPU again for work already done. The 2026-08-25
    /// repair re-embedded 211 511 clauses that way, on a corpus where almost nothing had
    /// actually changed — the ledger normally hides this by matching content hashes from
    /// a 31 GB sidecar, which is a lot of disk to solve a problem merge can avoid making.
    ///
    /// embedding_hash is sha(heading+text+model), so a row keyed by identical heading+text
    /// keeps a hash that is still true — the model identity cannot change mid-merge.
    pub fn stash_bucket_vectors(&self, buckets: &[(String, String)]) -> Result<usize> {
        self.conn
            .execute_batch("DROP TABLE IF EXISTS carry_vecs;")
            .context("drop carry_vecs")?;
        if buckets.is_empty() {
            self.conn.execute_batch(
                "CREATE TEMP TABLE carry_vecs (heading VARCHAR, text VARCHAR,
                                               embedding FLOAT[1024], embedding_hash VARCHAR);",
            )?;
            return Ok(0);
        }
        // GROUP BY the key: one bucket can hold the same heading+text twice, and a
        // duplicated join key would multiply rows on the way back.
        let pairs = buckets
            .iter()
            .map(|(s, r)| format!("({}, {})", q(s), q(r)))
            .collect::<Vec<_>>()
            .join(", ");
        self.conn
            .execute_batch(&format!(
                "CREATE TEMP TABLE carry_vecs AS
                 SELECT heading, text,
                        any_value(embedding)      AS embedding,
                        any_value(embedding_hash) AS embedding_hash
                 FROM clauses
                 WHERE embedding IS NOT NULL AND (spec_id, release) IN ({pairs})
                 GROUP BY heading, text;"
            ))
            .context("stash_bucket_vectors")?;
        let n: i64 = self
            .conn
            .query_row("SELECT count(*) FROM carry_vecs", [], |r| r.get(0))?;
        Ok(n as usize)
    }

    /// restore_stashed_vectors gives the stashed vectors back to every clause that still
    /// lacks one and whose heading+text matches. Returns how many rows were revived.
    pub fn restore_stashed_vectors(&self) -> Result<usize> {
        let n = self
            .conn
            .execute(
                "UPDATE clauses
                    SET embedding      = c.embedding,
                        embedding_hash = c.embedding_hash
                   FROM carry_vecs c
                  WHERE clauses.embedding IS NULL
                    AND clauses.heading = c.heading
                    AND clauses.text    = c.text",
                [],
            )
            .context("restore_stashed_vectors")?;
        self.conn
            .execute_batch("DROP TABLE IF EXISTS carry_vecs;")?;
        Ok(n)
    }

    pub fn delete_spec_release(&self, spec_id: &str, release: &str) -> Result<()> {
        self.conn.execute(
            "DELETE FROM clauses WHERE spec_id = ? AND release = ?",
            duckdb::params![spec_id, release],
        )?;
        self.conn.execute(
            "DELETE FROM spec_versions WHERE spec_id = ? AND release = ?",
            duckdb::params![spec_id, release],
        )?;
        self.conn.execute(
            "DELETE FROM specs WHERE spec_id = ? AND NOT EXISTS (SELECT 1 FROM spec_versions WHERE spec_id = ?)",
            duckdb::params![spec_id, spec_id],
        )?;
        Ok(())
    }

    /// strip_embeddings NULLs every vector + purges the vector meta, yielding a lexical-only
    /// DB (== Go merge --strip-embeddings; keeps the lexical channel slim while embed runs).
    pub fn strip_embeddings(&self) -> Result<()> {
        self.conn.execute_batch(
            "UPDATE clauses SET embedding = NULL;
             DELETE FROM schema_meta WHERE key IN ('embedding_model','hnsw_state','hnsw_metric','embedding_dim');",
        )?;
        Ok(())
    }

    /// overlay writes vectors (+ sparse postings) from one or more vector shards onto this
    /// base by NATURAL identity (spec_id,release,clause_path,text — NOT the unstable
    /// chunk_id), optionally copies the catalogue tables from `catalogue_from`, enforces a
    /// single embedding_model across all sources, and stamps it (== Go cmd/overlay). Returns
    /// (clauses_vectorised, agreed_model).
    pub fn overlay(
        &self,
        vecs: &[String],
        catalogue_from: &str,
        model_flag: &str,
    ) -> Result<(i64, String)> {
        const CATALOGUE_TABLES: &[&str] = &[
            "specs",
            "spec_versions",
            "acronyms",
            "evolutions",
            "releases",
            "changes",
            "api_operations",
            "api_schemas",
            "li_events",
            "li_event_fields",
            "li_nf_clauses",
            "asn1_types",
        ];
        let esc = |p: &str| p.replace('\'', "''");
        if !catalogue_from.is_empty() {
            self.conn.execute_batch(&format!(
                "ATTACH '{}' AS cat (READ_ONLY)",
                esc(catalogue_from)
            ))?;
            for t in CATALOGUE_TABLES {
                // A table may be absent in either side (older schema) — skip, don't abort.
                let _ = self
                    .conn
                    .execute_batch(&format!("INSERT INTO {t} SELECT * FROM cat.{t}"));
            }
            self.conn.execute_batch("DETACH cat")?;
        }
        let mut model = model_flag.to_string();
        for (i, v) in vecs.iter().enumerate() {
            let alias = format!("v{i}");
            self.conn
                .execute_batch(&format!("ATTACH '{}' AS {alias} (READ_ONLY)", esc(v)))?;
            let m: String = self
                .conn
                .query_row(
                    &format!("SELECT COALESCE(MAX(value), '') FROM {alias}.schema_meta WHERE key = 'embedding_model'"),
                    [],
                    |r| r.get(0),
                )
                .unwrap_or_default();
            if !m.is_empty() {
                if model.is_empty() {
                    model = m;
                } else if m != model {
                    anyhow::bail!("shard {v} embedding_model={m:?} != {model:?} — refusing to fuse mixed models");
                }
            }
            self.conn.execute_batch(&format!(
                "UPDATE clauses SET embedding = s.embedding, embedding_hash = s.embedding_hash
                 FROM {alias}.clauses AS s
                 WHERE clauses.spec_id = s.spec_id AND clauses.release = s.release
                   AND clauses.clause_path = s.clause_path AND clauses.text = s.text
                   AND s.embedding IS NOT NULL",
            ))?;
            self.overlay_sparse(&alias)?;
            self.conn.execute_batch(&format!("DETACH {alias}"))?;
        }
        self.conn.execute_batch("CHECKPOINT")?;
        self.set_meta("pipeline_version", "overlay")?;
        if !model.is_empty() {
            self.set_meta("embedding_model", &model)?;
        }
        let after: i64 = self.conn.query_row(
            "SELECT count(*) FROM clauses WHERE embedding IS NOT NULL",
            [],
            |r| r.get(0),
        )?;
        Ok((after, model))
    }

    /// overlay_sparse carries an attached shard's clause_sparse postings onto the base,
    /// re-keyed by the SAME natural identity as the dense column (== Go overlaySparse).
    /// Best-effort: a shard with no clause_sparse table contributes nothing.
    fn overlay_sparse(&self, alias: &str) -> Result<()> {
        let id_match = "b.spec_id = s.spec_id AND b.release = s.release \
                        AND b.clause_path = s.clause_path AND b.text = s.text";
        // Probe the table; absence/empty is not an error.
        if self
            .conn
            .execute_batch(&format!(
                "CREATE TEMP TABLE _probe AS SELECT 1 FROM {alias}.clause_sparse LIMIT 0"
            ))
            .is_err()
        {
            return Ok(());
        }
        let _ = self.conn.execute_batch("DROP TABLE IF EXISTS _probe");
        self.conn.execute_batch(&format!(
            "DELETE FROM clause_sparse WHERE chunk_id IN (
               SELECT b.chunk_id FROM {alias}.clauses s
               JOIN clauses b ON {id_match}
               WHERE EXISTS (SELECT 1 FROM {alias}.clause_sparse ss WHERE ss.chunk_id = s.chunk_id));
             INSERT INTO clause_sparse (chunk_id, term_id, weight)
             SELECT b.chunk_id, ss.term_id, MAX(ss.weight)
             FROM {alias}.clause_sparse ss
             JOIN {alias}.clauses s ON s.chunk_id = ss.chunk_id
             JOIN clauses b ON {id_match}
             GROUP BY b.chunk_id, ss.term_id;",
        ))?;
        Ok(())
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

    /// clauses_needing_embedding streams the work-list, oldest chunk first, capped at
    /// `limit` (0 = all).
    ///
    /// WHAT "NEEDING" MEANS depends on the identity. Normally it is "has no vector yet".
    /// But `EmbedIdentity` folds in the model, precision, windowing and max_tokens, and a
    /// corpus embedded under one identity must never be queried or indexed under another —
    /// that is the whole reason the identity exists. So when the DB is stamped with a
    /// DIFFERENT identity than the one this run embeds under, every embeddable clause needs
    /// re-embedding, vector or not.
    ///
    /// This used to ask `embedding IS NULL` alone. The #208 switch from truncate to
    /// mean_pool therefore archived the ledger, exported an EMPTY work-list, and reported
    /// "every clause already carries a vector — nothing to do" — a re-embed that silently
    /// did not happen, on a corpus every vector of which was now stale. Same defect class
    /// as validate/check-data/LoadVSS: the gate asked a different question than the thing
    /// it gated.
    ///
    /// `want_identity` empty = ask the old question (callers that have no identity to hand).
    pub fn clauses_needing_embedding(
        &self,
        limit: usize,
        floor_ord: i64,
        want_identity: &str,
    ) -> Result<Vec<WorkItem>> {
        let stamped = self.get_meta("embedding_model").unwrap_or_default();
        let identity_changed =
            !want_identity.is_empty() && !stamped.is_empty() && stamped != want_identity;
        if identity_changed {
            eprintln!(
                "embed work-list: corpus is stamped {stamped}, this run embeds {want_identity} — every embeddable clause is stale and re-enters the work-list"
            );
        }
        let vector_filter = if identity_changed {
            ""
        } else {
            "embedding IS NULL AND "
        };
        // Carry `release` so the floor (release ordinal ≥ floor_ord) is applied in Rust — the
        // Rel-99→3 special makes a pure-SQL ordinal awkward (== Go ClausesNeedingEmbedding
        // FloorOrd). floor_ord ≤ 0 = no floor.
        let sql = format!(
            "SELECT chunk_id, COALESCE(release,''), COALESCE(heading,''), COALESCE(text,'') FROM clauses
             WHERE {vector_filter}{EMBEDDABLE_TEXT_SQL} ORDER BY chunk_id"
        );
        let mut stmt = self.conn.prepare(&sql).context("prepare worklist")?;
        let rows = stmt
            .query_map([], |r| {
                Ok((
                    r.get::<_, u64>(0)?,
                    r.get::<_, String>(1)?,
                    r.get::<_, String>(2)?,
                    r.get::<_, String>(3)?,
                ))
            })
            .context("query worklist")?;
        let mut out = Vec::new();
        for r in rows {
            let (chunk_id, release, heading, text) = r.context("scan worklist row")?;
            if floor_ord > 0
                && crate::identity::release_ordinal(&release).is_none_or(|o| o < floor_ord)
            {
                continue; // below the embed floor → leave lexical-only
            }
            out.push(WorkItem {
                chunk_id,
                heading,
                text,
            });
            if limit > 0 && out.len() >= limit {
                break;
            }
        }
        Ok(out)
    }

    /// clauses_needing_sparse streams the SPARSE work-list: embeddable clauses with no
    /// clause_sparse posting yet, oldest chunk first, capped at `limit` (0 = all). Mirrors
    /// clauses_needing_embedding but keys on the sparse table instead of `embedding`, so a
    /// `--sparse-only` pass is resumable and additive (== Go embed --sparse-only worklist).
    pub fn clauses_needing_sparse(&self, limit: usize, floor_ord: i64) -> Result<Vec<WorkItem>> {
        let sql = format!(
            "SELECT chunk_id, COALESCE(release,''), COALESCE(heading,''), COALESCE(text,'') FROM clauses
             WHERE chunk_id NOT IN (SELECT chunk_id FROM clause_sparse) AND {EMBEDDABLE_TEXT_SQL} ORDER BY chunk_id"
        );
        let mut stmt = self.conn.prepare(&sql).context("prepare sparse worklist")?;
        let rows = stmt
            .query_map([], |r| {
                Ok((
                    r.get::<_, u64>(0)?,
                    r.get::<_, String>(1)?,
                    r.get::<_, String>(2)?,
                    r.get::<_, String>(3)?,
                ))
            })
            .context("query sparse worklist")?;
        let mut out = Vec::new();
        for r in rows {
            let (chunk_id, release, heading, text) = r.context("scan sparse worklist row")?;
            if floor_ord > 0
                && crate::identity::release_ordinal(&release).is_none_or(|o| o < floor_ord)
            {
                continue;
            }
            out.push(WorkItem {
                chunk_id,
                heading,
                text,
            });
            if limit > 0 && out.len() >= limit {
                break;
            }
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

    /// import_ledger loads a whole JSONL vector ledger in ONE statement, letting
    /// DuckDB read the file itself.
    ///
    /// The row-by-row path above makes the same numbers cross the text boundary three
    /// times: serde parses the JSON into `Vec<f32>`, `set_embeddings_batch` turns each
    /// f32 back into decimal with `to_string()` and glues 1024 of them into an
    /// `UPDATE … SET embedding = [0.0076,…]`, and DuckDB then parses those decimals
    /// back into f32. Over a full corpus that is ~2.3 billion float↔text conversions
    /// and tens of gigabytes of generated SQL; measured, it took ~70 minutes to import
    /// 2.2 M vectors — longer than fetch's ingest and merge combined.
    ///
    /// `read_json` parses once, in DuckDB's own reader, straight into a FLOAT[] column,
    /// and the write becomes a single set-based join instead of one statement per row.
    ///
    /// Returns (staged, embedded_total). Malformed rows are counted out, not fatal: a
    /// killed embedder leaves a half-written final line, and one truncated line must
    /// never cost the whole ledger — `ignore_errors` skips them exactly as the
    /// hand-rolled loop did, and the width filter drops any vector that is not
    /// DENSE_DIM wide rather than letting one bad cast abort the import.
    /// clauses_is_view reports whether the corpus is content-addressed (ADR 0004): on a
    /// converted corpus `clauses` is a VIEW over clause_occ ⋈ bodies and the vectors live
    /// on `bodies`; on a raw one it is the table that holds them.
    pub fn clauses_is_view(&self) -> Result<bool> {
        let t: String = self
            .conn
            .query_row(
                "SELECT COALESCE(max(table_type),'') FROM information_schema.tables
                  WHERE table_name = 'clauses'",
                [],
                |r| r.get(0),
            )
            .context("inspect the shape of `clauses`")?;
        Ok(t.eq_ignore_ascii_case("VIEW"))
    }

    /// check_body_ledger_agreement guards the collapse the body-level write performs.
    ///
    /// Many occurrences share one body, and they share it precisely because their text is
    /// identical — so they carry the same vector and the same hash, and folding them onto
    /// one row is sound. That is an invariant, not a hope. A body whose occurrences
    /// disagree means the ledger and the corpus describe different text, and the write
    /// would pick one of them at random; refuse instead.
    fn check_body_ledger_agreement(&self) -> Result<()> {
        let conflicts: i64 = self
            .conn
            .query_row(
                "SELECT count(*) FROM (
                   SELECT o.body_id FROM _ledger l JOIN clause_occ o USING (chunk_id)
                    GROUP BY o.body_id HAVING count(DISTINCT l.embedding_hash) > 1)",
                [],
                |r| r.get(0),
            )
            .context("check that occurrences of a body agree on their vector")?;
        if conflicts > 0 {
            anyhow::bail!(
                "{conflicts} body/bodies whose occurrences carry different embedding hashes — \
                 the ledger and the corpus disagree about their text; refusing to write one at random"
            );
        }
        Ok(())
    }

    pub fn import_ledger(&self, path: &str) -> Result<(i64, i64)> {
        let p = path.replace('\\', "/").replace('\'', "''");
        self.conn
            .execute_batch(&format!(
                "BEGIN;
                 CREATE OR REPLACE TEMP TABLE _ledger AS
                   SELECT chunk_id, hash AS embedding_hash, vec
                   FROM read_json('{p}',
                                  format='newline_delimited',
                                  ignore_errors=true,
                                  columns={{chunk_id:'UBIGINT', hash:'VARCHAR', vec:'FLOAT[]'}})
                   WHERE vec IS NOT NULL AND len(vec) = {DENSE_DIM};"
            ))
            .with_context(|| format!("read ledger {path}"))?;

        let staged: i64 = self
            .conn
            .query_row("SELECT count(*) FROM _ledger", [], |r| r.get(0))
            .context("count staged ledger rows")?;

        // WHERE THE VECTORS ACTUALLY LIVE. On a content-addressed corpus (ADR 0004)
        // `clauses` is a view and DuckDB refuses to UPDATE it, which is why the embed step
        // used to run `migrate-paragraphs --restore` first — rematerialising every clause's
        // text and taking the corpus from 11.5 GB to 38.8 GB before a single vector was
        // computed, then needing a full re-compaction to give the space back. Writing to
        // `bodies` costs none of that and touches 821 146 rows instead of 2 752 688.
        //
        // The cast to the FIXED width happens only here: staging is FLOAT[] (variable),
        // the column is FLOAT[1024], and the filter above has already guaranteed every
        // surviving row is exactly that wide.
        let to_bodies = self.clauses_is_view()?;
        if to_bodies {
            self.check_body_ledger_agreement()?;
        }
        let apply = if to_bodies {
            format!(
                "UPDATE bodies SET embedding = d.vec::FLOAT[{DENSE_DIM}],
                                   embedding_hash = d.embedding_hash
                   FROM (SELECT o.body_id,
                                any_value(l.vec) AS vec,
                                any_value(l.embedding_hash) AS embedding_hash
                           FROM _ledger l JOIN clause_occ o USING (chunk_id)
                          GROUP BY o.body_id) AS d
                  WHERE bodies.body_id = d.body_id;"
            )
        } else {
            format!(
                "UPDATE clauses SET embedding = l.vec::FLOAT[{DENSE_DIM}],
                                    embedding_hash = l.embedding_hash
                   FROM _ledger AS l WHERE clauses.chunk_id = l.chunk_id;"
            )
        };
        self.conn
            .execute_batch(&format!("{apply} DROP TABLE _ledger; COMMIT;"))
            .with_context(|| {
                if to_bodies {
                    "apply ledger to bodies"
                } else {
                    "apply ledger to clauses"
                }
            })?;
        let embedded: i64 = self
            .conn
            .query_row(
                "SELECT count(*) FROM clauses WHERE embedding IS NOT NULL",
                [],
                |r| r.get(0),
            )
            .context("count embedded clauses")?;
        Ok((staged, embedded))
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

        // CAP THE BUFFER MANAGER FOR THE DURATION OF THE BUILD.
        //
        // A vss HNSW index lives OUTSIDE DuckDB's buffer manager: it is held whole
        // in RAM for the entire build and never spills. At DENSE_DIM float32 that
        // is 4 KB per row for the vectors alone — 2 443 844 rows is ~10 GB before
        // the graph. Meanwhile memory_limit defaults to ~80% of physical RAM, so
        // the buffer manager and the index each believe they may take most of the
        // machine, and nothing arbitrates between them.
        //
        // On a 28 GB host that ended at 46.7 GB of committed virtual memory with
        // 1.0 GB left, the pagefile eating the last 5 GB of free disk, and the
        // build still unfinished after 1 h 54 of CPU — thrashing, not progressing.
        //
        // So give the buffer manager a fixed, modest budget and leave the rest of
        // the machine to the structure that has nowhere else to go. What the buffer
        // manager does NOT fit, it spills to temp_directory — the build materialises
        // the whole embedding column, so the spill is measured in GB, not MB. That
        // spill is the second ceiling, and it is a DISK one: see below.
        let buf = std::env::var("HNSW_BUILD_MEMORY_LIMIT").unwrap_or_else(|_| "4GB".into());

        // Operator overrides, unset by default so DuckDB keeps its own defaults
        // wherever they are sane. HNSW_BUILD_TEMP_LIMIT is the escape hatch for the
        // disk ceiling documented in the error path below; HNSW_BUILD_THREADS trades
        // build speed for a smaller concurrent materialisation.
        let mut knobs = String::new();
        if let Ok(t) = std::env::var("HNSW_BUILD_TEMP_LIMIT") {
            knobs.push_str(&format!("SET max_temp_directory_size = '{t}';"));
        }
        if let Ok(n) = std::env::var("HNSW_BUILD_THREADS") {
            knobs.push_str(&format!("SET threads = {n};"));
        }

        // preserve_insertion_order is pure cost here: HNSW is an unordered structure
        // and the scan feeding it has no ORDER BY, so holding the row order only
        // widens the materialisation that has to spill.
        // MUST match internal/store/hnsw.go's hnswM/hnswEfConstruction: two builders that
        // disagree produce two different graphs over the same vectors, and nothing downstream
        // can tell which one it is querying. Same env overrides, same defaults.
        let m = std::env::var("HNSW_M").unwrap_or_else(|_| "32".into());
        let efc = std::env::var("HNSW_EF_CONSTRUCTION").unwrap_or_else(|_| "128".into());
        let sql = format!(
            "CHECKPOINT;
             SET memory_limit = '{buf}';
             SET preserve_insertion_order = false;
             {knobs}
             INSTALL vss; LOAD vss; SET hnsw_enable_experimental_persistence = true;
             CREATE INDEX IF NOT EXISTS clauses_hnsw ON clauses USING HNSW (embedding) WITH (metric = 'cosine', M = {m}, ef_construction = {efc});
             CHECKPOINT;"
        );
        self.conn.execute_batch(&sql).map_err(|e| {
            let cause = e.to_string();
            // DuckDB reports the spill ceiling as "Out of Memory Error", which sends
            // every reader after the RAM. It is not RAM. max_temp_directory_size
            // defaults to 90% of the FREE SPACE on the temp drive, so a nearly-full
            // disk silently caps the spill at a few GiB and the build dies against
            // that cap twenty minutes in. Raising memory_limit only changes how fast
            // it gets there. Say so, in the error, where it costs nothing to read.
            if cause.contains("max_temp_directory_size") {
                anyhow::anyhow!(
                    "build hnsw ({count} vectors, buffer budget {buf}): out of TEMP DISK, not RAM. \
                     'max_temp_directory_size' defaults to 90% of the free space on the drive \
                     holding DuckDB's temp_directory, so a full disk caps the spill. Free space on \
                     that drive, or set HNSW_BUILD_TEMP_LIMIT to pick the cap yourself.\n\ncaused by: {cause}"
                )
            } else {
                anyhow::anyhow!("build hnsw (buffer budget {buf}, {count} vectors)\n\ncaused by: {cause}")
            }
        })?;
        for (k, v) in [
            ("hnsw_metric", "cosine"),
            ("embedding_dim", "1024"),
            ("embedding_count", &count.to_string()),
            ("embedding_model", model),
            ("hnsw_m", &m),
            ("hnsw_ef_construction", &efc),
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
        self.fold_shard_buckets(shard_path, offset, None)
    }

    /// changed_buckets returns the shard's (spec, release) pairs this database does not
    /// already hold in full: a different version, a spec it has never seen, OR a bucket
    /// whose catalogue row is present while its TEXT is missing.
    ///
    /// A shard is rebuilt from every converted HTML of its series, so after one full
    /// pass it carries the whole series whether or not anything moved. Replaying all of
    /// it through delete-then-insert is not merely wasted time: DuckDB does not reclaim
    /// the deleted blocks, so re-folding 745 unchanged specs to bring in 5 changed ones
    /// grew a 26 GB corpus past 43 GB on a single shard, and the run died on disk.
    ///
    /// The version alone is NOT enough, and assuming it was skipped every shard on the
    /// 2026-08-25 repair — including the one carrying the 6 209 clauses the repair
    /// existed to acquire. A corpus hole is exactly the case where `spec_versions` holds
    /// the right version and `clauses` holds nothing: that is what `anchorcheck` calls
    /// missing_content, and skipping it makes the hole permanent. So the clause side is
    /// checked too, which is also what makes this safe to use as the repair path.
    ///
    /// And the clause COUNT is compared, not merely its existence, because the version
    /// describes the DOCUMENT and not the parse. Fixing the walker so that a heading
    /// wrapped in a list item opens a clause took TR 25.890 from 0 to 41 clauses at the
    /// very same version — content a version check would have refused to let in.
    pub fn changed_buckets(&self, shard_path: &str) -> Result<Vec<(String, String)>> {
        self.conn.execute_batch(&format!(
            "ATTACH '{}' AS s (READ_ONLY)",
            shard_path.replace('\'', "''")
        ))?;
        let out = {
            let mut st = self.conn.prepare(
                "SELECT sv.spec_id, sv.release
                   FROM s.spec_versions sv
                   LEFT JOIN spec_versions b
                     ON b.spec_id = sv.spec_id AND b.release = sv.release
                   LEFT JOIN (SELECT spec_id, release, count(*) AS n FROM clauses GROUP BY 1,2) c
                     ON c.spec_id = sv.spec_id AND c.release = sv.release
                   LEFT JOIN (SELECT spec_id, release, count(*) AS n FROM s.clauses GROUP BY 1,2) sc
                     ON sc.spec_id = sv.spec_id AND sc.release = sv.release
                  WHERE b.version IS NULL          -- never seen
                     OR b.version <> sv.version    -- the document moved
                     OR c.spec_id IS NULL          -- catalogued but textless: a HOLE
                     -- The PARSE moved at the same version. Guarded by sc.n IS NOT NULL:
                     -- a shard holding NO clauses for a bucket the corpus does hold text
                     -- for must never trigger a fold, because the fold would delete that
                     -- text and insert nothing in its place.
                     OR (sc.n IS NOT NULL AND c.n IS DISTINCT FROM sc.n)",
            )?;
            let rows =
                st.query_map([], |r| Ok((r.get::<_, String>(0)?, r.get::<_, String>(1)?)))?;
            rows.filter_map(std::result::Result::ok).collect::<Vec<_>>()
        };
        self.conn.execute_batch("DETACH s")?;
        Ok(out)
    }

    /// corpus_versions_with_text returns the (spec_id, version) pairs the corpus at
    /// `corpus_path` already holds actual CLAUSES for.
    ///
    /// `ingest --resume` consults the SHARD's own ingest_log, and a shard is scratch:
    /// delete it, or start a series that never had one, and the ledger is empty, so
    /// every converted file of that series is parsed and written again. The 2026-08-25
    /// run re-ingested ~300 000 clauses that way to acquire five specs, and merge then
    /// had to decide, bucket by bucket, that almost none of it had changed.
    ///
    /// The corpus is the durable record of what is already held, so let resume ask it.
    /// The version is the key rather than the release: a spec is re-ingested when its
    /// DOCUMENT changed, and that is what the version names.
    pub fn corpus_versions_with_text(&self, corpus_path: &str) -> Result<Vec<(String, String)>> {
        self.conn.execute_batch(&format!(
            "ATTACH '{}' AS corp (READ_ONLY)",
            corpus_path.replace('\'', "''")
        ))?;
        let out = {
            let mut st = self
                .conn
                .prepare("SELECT DISTINCT spec_id, version FROM corp.clauses")?;
            let rows =
                st.query_map([], |r| Ok((r.get::<_, String>(0)?, r.get::<_, String>(1)?)))?;
            rows.filter_map(std::result::Result::ok).collect::<Vec<_>>()
        };
        self.conn.execute_batch("DETACH corp")?;
        Ok(out)
    }

    /// fold_shard_buckets folds a shard, optionally restricted to the given (spec,
    /// release) buckets. `None` folds everything (== the old behaviour).
    pub fn fold_shard_buckets(
        &self,
        shard_path: &str,
        offset: u64,
        only: Option<&[(String, String)]>,
    ) -> Result<()> {
        // The row filter is applied to the clause-bearing tables only. The catalogue
        // inserts are ON CONFLICT DO NOTHING, so replaying them costs nothing and keeps
        // a spec's catalogue row present even when its text did not move.
        let (clause_where, sparse_where) = match only {
            None => (String::new(), String::new()),
            Some(bs) => {
                let pairs = bs
                    .iter()
                    .map(|(s, r)| format!("({}, {})", q(s), q(r)))
                    .collect::<Vec<_>>()
                    .join(", ");
                (
                    format!(" WHERE (spec_id, release) IN ({pairs})"),
                    format!(
                        " WHERE chunk_id IN (SELECT chunk_id FROM s.clauses WHERE (spec_id, release) IN ({pairs}))"
                    ),
                )
            }
        };
        let sql = format!(
            "ATTACH '{shard_path}' AS s (READ_ONLY);
             INSERT INTO specs SELECT * FROM s.specs ON CONFLICT DO NOTHING;
             INSERT INTO spec_versions SELECT * FROM s.spec_versions ON CONFLICT DO NOTHING;
             INSERT INTO releases SELECT * FROM s.releases ON CONFLICT DO NOTHING;
             INSERT INTO acronyms SELECT * FROM s.acronyms ON CONFLICT DO NOTHING;
             INSERT INTO clauses
               SELECT chunk_id + {offset}, spec_id, release, version, clause_path, heading, text,
                      is_normative, embedding, embedding_hash
               FROM s.clauses{clause_where};
             INSERT INTO clause_sparse
               SELECT chunk_id + {offset}, term_id, weight FROM s.clause_sparse{sparse_where};
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
    // `id` is a DB primary-key offset (starts at max+1) interpolated into each INSERT,
    // not a 0-based loop index, so enumerate() is not equivalent.
    #[allow(clippy::explicit_counter_loop)]
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
    // `id` is a DB primary-key offset interpolated into each INSERT, not a loop index.
    #[allow(clippy::explicit_counter_loop)]
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
    // One cohesive transactional write; the four parallel slices + identity triple are
    // intrinsic to the LI registry shape, so splitting into a struct would not clarify it.
    #[allow(clippy::too_many_arguments)]
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

    /// The embed step used to rematerialise every clause's text just to have a table it
    /// could UPDATE — 11.5 GB to 38.8 GB on the real corpus, before any vector existed.
    /// This pins the write going where the vectors actually live, including the case that
    /// makes it possible at all: several occurrences sharing one body.
    #[test]
    fn a_content_addressed_corpus_takes_its_vectors_on_bodies() {
        let s = Store::in_memory().unwrap();
        s.raw()
            .execute_batch(
                "DROP TABLE IF EXISTS clauses;
                 INSERT INTO paragraphs(para_id, part) VALUES (1,'alpha'),(2,'beta');
                 INSERT INTO bodies(body_id, heading) VALUES (10,'A'),(20,'B');
                 INSERT INTO body_seq(body_id, ord, para_id) VALUES (10,0,1),(20,0,2);
                 -- chunk 1 and 3 are the SAME body: one clause reused across releases.
                 INSERT INTO clause_occ(chunk_id, spec_id, release, version, clause_path, body_id, is_normative)
                   VALUES (1,'23.501','Rel-18','18.0','5.1',10,true),
                          (2,'23.501','Rel-18','18.0','5.2',20,true),
                          (3,'23.501','Rel-19','19.0','5.1',10,true);
                 CREATE OR REPLACE VIEW clauses AS
                   SELECT o.chunk_id, o.spec_id, o.release, o.version, o.clause_path, b.heading,
                          (SELECT string_agg(p.part, chr(10)||chr(10) ORDER BY s.ord)
                             FROM body_seq s JOIN paragraphs p USING (para_id)
                            WHERE s.body_id = o.body_id) AS text,
                          o.is_normative, b.embedding, b.embedding_hash
                   FROM clause_occ o JOIN bodies b USING (body_id);",
            )
            .unwrap();
        assert!(
            s.clauses_is_view().unwrap(),
            "the fixture must be converted"
        );

        let vec_a = (0..DENSE_DIM).map(|i| (i % 7) as f32).collect::<Vec<_>>();
        let vec_b = (0..DENSE_DIM).map(|i| (i % 5) as f32).collect::<Vec<_>>();
        let json = |chunk: u64, hash: &str, v: &[f32]| {
            let nums: Vec<String> = v.iter().map(|x| format!("{x}")).collect();
            format!(
                "{{\"chunk_id\":{chunk},\"hash\":\"{hash}\",\"vec\":[{}]}}",
                nums.join(",")
            )
        };
        // chunks 1 and 3 share body 10, so they carry the same hash and the same vector.
        let ledger = format!(
            "{}\n{}\n{}\n",
            json(1, "hash-a", &vec_a),
            json(2, "hash-b", &vec_b),
            json(3, "hash-a", &vec_a)
        );
        let path = std::env::temp_dir().join("ledger_bodies_test.jsonl");
        std::fs::write(&path, ledger).unwrap();

        let (staged, embedded) = s.import_ledger(path.to_str().unwrap()).unwrap();
        assert_eq!(staged, 3, "three ledger rows staged");
        assert_eq!(
            embedded, 3,
            "all three occurrences read a vector through the view"
        );

        let bodies_with_vec: i64 = s
            .raw()
            .query_row(
                "SELECT count(*) FROM bodies WHERE embedding IS NOT NULL",
                [],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(
            bodies_with_vec, 2,
            "two bodies hold the vectors, not three copies"
        );
        let hash_of_10: String = s
            .raw()
            .query_row(
                "SELECT embedding_hash FROM bodies WHERE body_id = 10",
                [],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(hash_of_10, "hash-a");
        let _ = std::fs::remove_file(&path);
    }

    /// The collapse is only sound because occurrences of one body have identical text.
    /// If the ledger says otherwise, writing either vector would be a coin toss.
    #[test]
    fn disagreeing_occurrences_of_one_body_are_refused() {
        let s = Store::in_memory().unwrap();
        s.raw()
            .execute_batch(
                "DROP TABLE IF EXISTS clauses;
                 INSERT INTO paragraphs(para_id, part) VALUES (1,'alpha');
                 INSERT INTO bodies(body_id, heading) VALUES (10,'A');
                 INSERT INTO body_seq(body_id, ord, para_id) VALUES (10,0,1);
                 INSERT INTO clause_occ(chunk_id, spec_id, release, version, clause_path, body_id, is_normative)
                   VALUES (1,'23.501','Rel-18','18.0','5.1',10,true),
                          (2,'23.501','Rel-19','19.0','5.1',10,true);
                 CREATE OR REPLACE VIEW clauses AS
                   SELECT o.chunk_id, o.spec_id, o.release, o.version, o.clause_path, b.heading,
                          '' AS text, o.is_normative, b.embedding, b.embedding_hash
                   FROM clause_occ o JOIN bodies b USING (body_id);",
            )
            .unwrap();
        let v = (0..DENSE_DIM).map(|i| i as f32).collect::<Vec<_>>();
        let nums: Vec<String> = v.iter().map(|x| format!("{x}")).collect();
        let ledger = format!(
            "{{\"chunk_id\":1,\"hash\":\"one\",\"vec\":[{n}]}}\n{{\"chunk_id\":2,\"hash\":\"TWO\",\"vec\":[{n}]}}\n",
            n = nums.join(",")
        );
        let path = std::env::temp_dir().join("ledger_conflict_test.jsonl");
        std::fs::write(&path, ledger).unwrap();
        let err = s.import_ledger(path.to_str().unwrap()).unwrap_err();
        assert!(
            err.to_string().contains("different embedding hashes"),
            "expected a refusal naming the disagreement, got: {err}"
        );
        let _ = std::fs::remove_file(&path);
    }

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
        let wl = s.clauses_needing_embedding(0, 0, "").unwrap();
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
    fn sparse_worklist_excludes_done_and_unembeddable() {
        let s = Store::in_memory().unwrap();
        s.raw()
            .execute_batch(
                "INSERT INTO clauses(chunk_id,spec_id,heading,text) VALUES
                 (1,'23.501','a','alpha'),(2,'23.501','b','beta'),(3,'23.501','c','');",
            )
            .unwrap();
        // Both embeddable clauses need sparse; the empty-text clause (3) is excluded.
        let wl = s.clauses_needing_sparse(0, 0).unwrap();
        assert_eq!(wl.len(), 2);
        assert_eq!(
            wl.iter().map(|w| w.chunk_id).collect::<Vec<_>>(),
            vec![1, 2]
        );

        // Writing a posting for clause 1 removes it from the next worklist (resume/additive).
        s.set_sparse(1, &[(10, 0.5), (20, 1.5)]).unwrap();
        let wl2 = s.clauses_needing_sparse(0, 0).unwrap();
        assert_eq!(wl2.iter().map(|w| w.chunk_id).collect::<Vec<_>>(), vec![2]);

        // --limit bounds the slice.
        s.set_sparse(2, &[(30, 0.9)]).unwrap();
        assert!(s.clauses_needing_sparse(0, 0).unwrap().is_empty());
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
    fn overlay_sparse_dedups_duplicate_identity() {
        // A shard whose clause_sparse maps two clauses of IDENTICAL natural identity
        // (spec/release/clause_path/text) onto ONE base clause must NOT raise a
        // PRIMARY KEY (chunk_id, term_id) violation — the bug behind resume_overlay_failed.
        let dir = std::env::temp_dir().join(format!("storers-ovl-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let mkpath = |n: &str| {
            let p = dir.join(n).to_str().unwrap().to_string();
            let _ = std::fs::remove_file(&p);
            p
        };

        // Shard: two clauses, SAME identity, each with a posting for term 50 (diff weights).
        let shard = mkpath("shard.duckdb");
        {
            let s = Store::open_rw(&shard).unwrap();
            s.raw()
                .execute_batch(
                    "INSERT INTO clauses(chunk_id,spec_id,release,version,clause_path,heading,text) VALUES
                       (1,'23.501','Rel-19','19.1.0','5.1','h','dup text'),
                       (2,'23.501','Rel-19','19.2.0','5.1','h','dup text');
                     INSERT INTO clause_sparse(chunk_id,term_id,weight) VALUES
                       (1,50,0.5),(2,50,1.5);",
                )
                .unwrap();
            s.checkpoint().unwrap();
        }

        // Base: one clause of that identity.
        let basep = mkpath("base.duckdb");
        let base = Store::open_rw(&basep).unwrap();
        base.raw()
            .execute_batch(
                "INSERT INTO clauses(chunk_id,spec_id,release,version,clause_path,heading,text) VALUES
                   (100,'23.501','Rel-19','19.1.0','5.1','h','dup text');",
            )
            .unwrap();

        // Overlay must succeed (no PK violation) and collapse to ONE posting (MAX weight).
        base.overlay(&[shard], "", "")
            .expect("overlay must not raise a PK violation");
        let rows: i64 = base
            .raw()
            .query_row(
                "SELECT count(*) FROM clause_sparse WHERE chunk_id=100",
                [],
                |r| r.get(0),
            )
            .unwrap();
        let w: f64 = base
            .raw()
            .query_row(
                "SELECT weight FROM clause_sparse WHERE chunk_id=100 AND term_id=50",
                [],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(
            rows, 1,
            "duplicate-identity postings collapse to one (chunk_id,term_id)"
        );
        assert!((w - 1.5).abs() < 1e-6, "MAX weight kept, got {w}");
        let _ = std::fs::remove_dir_all(&dir);
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

    // A writer must be able to touch a corpus that ALREADY carries a frozen HNSW.
    //
    // DuckDB refuses to modify — or even CHECKPOINT — a table whose index type it
    // cannot bind, so a Store that opens without vss works on the FIRST pass and
    // fails on every incremental one. ETSI re-ingesting into an already-indexed
    // etsi.duckdb is precisely that second pass, and it failed with
    // "Cannot bind index 'clauses', unknown index type 'HNSW'".
    #[test]
    fn a_writer_can_modify_a_corpus_that_already_carries_a_frozen_hnsw() {
        let dir = std::env::temp_dir().join(format!("storers-hnswrw-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let p = dir.join("corpus.duckdb");
        let _ = std::fs::remove_file(&p);
        let path = p.to_str().unwrap().to_string();

        // First pass: build the corpus and freeze the index, then CLOSE it, so the
        // second open sees the index on disk exactly as a later step would.
        {
            let s = Store::open_rw(&path).unwrap();
            s.raw()
                .execute_batch(
                    "INSERT INTO specs(spec_id,series,doc_type) VALUES ('23.501','23','TS');
                     INSERT INTO clauses(chunk_id,spec_id,heading,text) VALUES
                       (1,'23.501','h1','t1'),(2,'23.501','h2','t2');",
                )
                .unwrap();
            s.raw()
                .execute_batch(&format!(
                    "UPDATE clauses SET embedding =
                       (SELECT list(CAST(i AS FLOAT)) FROM range({DENSE_DIM}) t(i))::FLOAT[{DENSE_DIM}];"
                ))
                .unwrap();
            s.build_and_freeze_hnsw("test-model").unwrap();
        }

        // Second pass: the index exists. Both the write and the checkpoint must work.
        let s = Store::open_rw(&path).unwrap();
        s.raw()
            .execute_batch(
                "INSERT INTO clauses(chunk_id,spec_id,heading,text) VALUES (3,'23.501','h3','t3');",
            )
            .expect("a writer must bind the existing HNSW before modifying clauses");
        s.checkpoint()
            .expect("checkpoint must not fail on a corpus that carries an HNSW");

        drop(s);
        let _ = std::fs::remove_dir_all(&dir);
    }

    // A bucket replacement must not throw away the vectors of text that did not change.
    //
    // merge deletes a (spec, release) bucket and folds the shard's rows in its place, and
    // the shard carries no embeddings. So a re-ingested spec whose wording is untouched
    // came out unvectorised, and the next embed pass paid the GPU all over again — 211 511
    // clauses on the 2026-08-25 repair, for a corpus where almost nothing had changed.
    #[test]
    fn replacing_a_bucket_keeps_the_vectors_of_unchanged_text() {
        let s = Store::in_memory().unwrap();
        s.raw()
            .execute_batch(
                "INSERT INTO specs(spec_id,series,doc_type) VALUES ('23.501','23','TS');
                 INSERT INTO clauses(chunk_id,spec_id,release,heading,text) VALUES
                   (1,'23.501','Rel-19','h1','unchanged'),
                   (2,'23.501','Rel-19','h2','will be rewritten');",
            )
            .unwrap();
        s.raw()
            .execute_batch(&format!(
                "UPDATE clauses SET
                   embedding = (SELECT list(CAST(i AS FLOAT)) FROM range({DENSE_DIM}) t(i))::FLOAT[{DENSE_DIM}],
                   embedding_hash = 'sha-' || heading;"
            ))
            .unwrap();

        let buckets = vec![("23.501".to_string(), "Rel-19".to_string())];
        let stashed = s.stash_bucket_vectors(&buckets).unwrap();
        assert_eq!(stashed, 2, "both texts must be kept");

        // Replay what merge does: drop the bucket, then insert the shard's rows — same
        // wording for h1, new wording for h2, and no embeddings on either.
        s.delete_spec_release("23.501", "Rel-19").unwrap();
        s.raw()
            .execute_batch(
                "INSERT INTO clauses(chunk_id,spec_id,release,heading,text) VALUES
                   (10,'23.501','Rel-19','h1','unchanged'),
                   (11,'23.501','Rel-19','h2','rewritten in the new revision');",
            )
            .unwrap();

        let revived = s.restore_stashed_vectors().unwrap();
        assert_eq!(revived, 1, "only the unchanged text may be revived");

        let (id, hash): (u64, String) = s
            .raw()
            .query_row(
                "SELECT chunk_id, embedding_hash FROM clauses WHERE embedding IS NOT NULL",
                [],
                |r| Ok((r.get(0)?, r.get(1)?)),
            )
            .unwrap();
        assert_eq!(
            id, 10,
            "the vector must land on the NEW row, not the deleted one"
        );
        assert_eq!(
            hash, "sha-h1",
            "the hash travels with the vector it describes"
        );

        // The rewritten clause must stay unvectorised: handing it a stale vector would be
        // worse than the GPU cost this whole mechanism exists to avoid.
        let nulls: i64 = s
            .raw()
            .query_row(
                "SELECT count(*) FROM clauses WHERE embedding IS NULL",
                [],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(nulls, 1);
    }

    /// Stashing nothing must still leave a usable table: merge calls restore
    /// unconditionally, and a missing carry_vecs would abort a legitimate empty run.
    #[test]
    fn stashing_an_empty_bucket_set_is_not_an_error() {
        let s = Store::in_memory().unwrap();
        assert_eq!(s.stash_bucket_vectors(&[]).unwrap(), 0);
        assert_eq!(s.restore_stashed_vectors().unwrap(), 0);
    }

    // A merge must replace only what MOVED.
    //
    // A shard is rebuilt from every converted HTML of its series, so after one full pass
    // it carries the whole series whether or not anything changed. Replacing every
    // bucket is delete-then-insert, DuckDB does not reclaim the deleted blocks, and
    // re-folding 745 unchanged specs to bring in 5 changed ones took a 26 GB corpus past
    // 43 GB on ONE shard. Three merges died on disk before the cause turned out to be
    // the work itself rather than the machine.
    #[test]
    fn only_the_buckets_whose_version_moved_are_replaced() {
        let dir = std::env::temp_dir().join(format!("storers-changed-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let sp = dir.join("shard.duckdb");
        let _ = std::fs::remove_file(&sp);
        let shard = sp.to_str().unwrap().to_string();
        {
            let s = Store::open_rw(&shard).unwrap();
            s.raw()
                .execute_batch(
                    "INSERT INTO spec_versions(spec_id,release,version) VALUES
                       ('23.501','Rel-19','19.7.0'),   -- same as base  -> untouched
                       ('24.501','Rel-19','19.9.0'),   -- newer than base -> changed
                       ('34.123-1','Rel-10','10.7.0'); -- absent from base -> new",
                )
                .unwrap();
            s.checkpoint().unwrap();
        }

        let base = Store::in_memory().unwrap();
        base.raw()
            .execute_batch(
                "INSERT INTO spec_versions(spec_id,release,version) VALUES
                   ('23.501','Rel-19','19.7.0'),
                   ('24.501','Rel-19','19.2.0');
                 -- 23.501 is genuinely complete: catalogued AND textual.
                 INSERT INTO clauses(chunk_id,spec_id,release,heading,text)
                   VALUES (1,'23.501','Rel-19','h','t');",
            )
            .unwrap();

        let mut got = base.changed_buckets(&shard).unwrap();
        got.sort();
        let want = vec![
            ("24.501".to_string(), "Rel-19".to_string()),
            ("34.123-1".to_string(), "Rel-10".to_string()),
        ];
        assert_eq!(
            got, want,
            "a bucket at the SAME version must not be replaced"
        );

        // A CATALOGUED BUT TEXTLESS BUCKET IS A HOLE, AND MUST STILL BE FOLDED.
        //
        // This is the case a version check alone gets wrong, and getting it wrong
        // skipped every shard of the 2026-08-25 repair — including the one carrying
        // the 6 209 clauses the repair existed to acquire. spec_versions held the right
        // version, clauses held nothing, and "same version" read as "nothing to do".
        let holed = Store::in_memory().unwrap();
        holed
            .raw()
            .execute_batch(
                "INSERT INTO spec_versions(spec_id,release,version) VALUES
                   ('23.501','Rel-19','19.7.0'),
                   ('24.501','Rel-19','19.9.0'),
                   ('34.123-1','Rel-10','10.7.0');
                 -- every version matches the shard, but 34.123-1 has NO clauses
                 INSERT INTO clauses(chunk_id,spec_id,release,heading,text) VALUES
                   (1,'23.501','Rel-19','h','t'),
                   (2,'24.501','Rel-19','h','t');",
            )
            .unwrap();
        assert_eq!(
            holed.changed_buckets(&shard).unwrap(),
            vec![("34.123-1".to_string(), "Rel-10".to_string())],
            "a catalogued bucket with no text is a hole and must be re-folded"
        );

        // And a shard that moved nothing AND is fully textual yields nothing, so merge
        // can skip it whole — that is the optimisation this all exists for.
        let same = Store::in_memory().unwrap();
        same.raw()
            .execute_batch(
                "INSERT INTO spec_versions(spec_id,release,version) VALUES
                   ('23.501','Rel-19','19.7.0'),
                   ('24.501','Rel-19','19.9.0'),
                   ('34.123-1','Rel-10','10.7.0');
                 INSERT INTO clauses(chunk_id,spec_id,release,heading,text) VALUES
                   (1,'23.501','Rel-19','h','t'),
                   (2,'24.501','Rel-19','h','t'),
                   (3,'34.123-1','Rel-10','h','t');",
            )
            .unwrap();
        assert!(
            same.changed_buckets(&shard).unwrap().is_empty(),
            "an unchanged, fully textual shard must be skippable entirely"
        );

        let _ = std::fs::remove_dir_all(&dir);

        // A REPARSE AT THE SAME VERSION MUST STILL BE FOLDED.
        //
        // The version describes the DOCUMENT, not the parse. Fixing the walker so a
        // heading wrapped in a list item opens a clause took TR 25.890 from 0 to 41
        // clauses at the very same version — content a version check would have
        // refused to let in, leaving the hole open after the bug that caused it was
        // fixed.
        //
        // Its own directory: the assertions above end by removing theirs.
        let dir2 = std::env::temp_dir().join(format!("storers-reparse-{}", std::process::id()));
        std::fs::create_dir_all(&dir2).unwrap();
        let sp2 = dir2.join("shard.duckdb");
        let _ = std::fs::remove_file(&sp2);
        let shard = sp2.to_str().unwrap().to_string();
        {
            let s = Store::open_rw(&shard).unwrap();
            s.raw()
                .execute_batch(
                    "INSERT INTO spec_versions(spec_id,release,version) VALUES
                       ('23.501','Rel-19','19.7.0'),
                       ('24.501','Rel-19','19.9.0'),
                       ('34.123-1','Rel-10','10.7.0');",
                )
                .unwrap();
            s.checkpoint().unwrap();
        }
        let reparsed = Store::in_memory().unwrap();
        reparsed
            .raw()
            .execute_batch(
                "INSERT INTO spec_versions(spec_id,release,version) VALUES
                   ('23.501','Rel-19','19.7.0'),
                   ('24.501','Rel-19','19.9.0'),
                   ('34.123-1','Rel-10','10.7.0');
                 -- 23.501 is at the same version but the corpus holds FEWER clauses
                 -- than the shard now produces: the parse moved.
                 INSERT INTO clauses(chunk_id,spec_id,release,heading,text) VALUES
                   (1,'23.501','Rel-19','h','t'),
                   (2,'24.501','Rel-19','h','t'),
                   (3,'34.123-1','Rel-10','h','t');",
            )
            .unwrap();
        // The shard holds TWO clauses for 23.501 where the corpus holds one.
        {
            let s = Store::open_rw(&shard).unwrap();
            s.raw()
                .execute_batch(
                    "INSERT INTO clauses(chunk_id,spec_id,release,heading,text) VALUES
                       (10,'23.501','Rel-19','h1','t1'),
                       (11,'23.501','Rel-19','h2','t2'),
                       (12,'24.501','Rel-19','h','t'),
                       (13,'34.123-1','Rel-10','h','t');",
                )
                .unwrap();
            s.checkpoint().unwrap();
        }
        assert_eq!(
            reparsed.changed_buckets(&shard).unwrap(),
            vec![("23.501".to_string(), "Rel-19".to_string())],
            "a bucket whose PARSE produced more clauses must be re-folded"
        );

        // AND THE COUNT RULE MUST NEVER DESTROY TEXT.
        //
        // A shard that holds no clauses for a bucket the corpus DOES hold text for must
        // not trigger a fold: the fold deletes the bucket and inserts the shard's rows,
        // so folding an empty one would replace a good spec with nothing. The very
        // first assertion in this test is that case — a shard carrying only catalogue
        // rows — and it stays skipped.
        let empty_shard_dir = dir2.join("empty");
        std::fs::create_dir_all(&empty_shard_dir).unwrap();
        let esp = empty_shard_dir.join("shard.duckdb");
        let empty_shard = esp.to_str().unwrap().to_string();
        {
            let s = Store::open_rw(&empty_shard).unwrap();
            s.raw()
                .execute_batch(
                    "INSERT INTO spec_versions(spec_id,release,version)
                       VALUES ('23.501','Rel-19','19.7.0');",
                )
                .unwrap();
            s.checkpoint().unwrap();
        }
        let holder = Store::in_memory().unwrap();
        holder
            .raw()
            .execute_batch(
                "INSERT INTO spec_versions(spec_id,release,version)
                   VALUES ('23.501','Rel-19','19.7.0');
                 INSERT INTO clauses(chunk_id,spec_id,release,heading,text)
                   VALUES (1,'23.501','Rel-19','h','t');",
            )
            .unwrap();
        assert!(
            holder.changed_buckets(&empty_shard).unwrap().is_empty(),
            "an empty shard must never replace a bucket the corpus holds text for"
        );

        let _ = std::fs::remove_dir_all(&dir2);
    }

    // `--resume` must be able to ask the CORPUS what is already held, not only the
    // scratch shard it happens to be writing.
    //
    // ingest_log lives in the shard. Delete the shard — or start a series that never
    // had one — and the ledger is empty, so every converted file of that series is
    // parsed and written again. That re-ingested ~300 000 clauses on 2026-08-25 to
    // acquire five specs, and merge then had to decide bucket by bucket that almost
    // none of it had moved.
    #[test]
    fn the_corpus_can_be_asked_what_it_already_holds() {
        let dir = std::env::temp_dir().join(format!("storers-held-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let cp = dir.join("corpus.duckdb");
        let _ = std::fs::remove_file(&cp);
        let corpus = cp.to_str().unwrap().to_string();
        {
            let c = Store::open_rw(&corpus).unwrap();
            c.raw()
                .execute_batch(
                    "INSERT INTO specs(spec_id,series,doc_type) VALUES ('23.501','23','TS');
                     INSERT INTO clauses(chunk_id,spec_id,release,version,heading,text) VALUES
                       (1,'23.501','Rel-19','19.7.0','h','t'),
                       (2,'23.501','Rel-18','18.9.0','h','t');
                     -- catalogued but textless: the corpus does NOT hold this one
                     INSERT INTO spec_versions(spec_id,release,version)
                       VALUES ('34.123-1','Rel-10','10.7.0');",
                )
                .unwrap();
            c.checkpoint().unwrap();
        }

        let shard = Store::in_memory().unwrap();
        let mut got = shard.corpus_versions_with_text(&corpus).unwrap();
        got.sort();
        assert_eq!(
            got,
            vec![
                ("23.501".to_string(), "18.9.0".to_string()),
                ("23.501".to_string(), "19.7.0".to_string()),
            ],
            "only versions the corpus has TEXT for may be skipped"
        );
        assert!(
            !got.contains(&("34.123-1".to_string(), "10.7.0".to_string())),
            "a catalogued-but-textless version must still be re-ingested — it is a hole"
        );

        let _ = std::fs::remove_dir_all(&dir);
    }

    // The base copy must RECLAIM, not clone.
    //
    // std::fs::copy carried the previous run's dead space forward, so an incremental
    // merge grew the corpus every time and the growth compounded across runs — 38.5 GB
    // in, 133 GB out, and the next run would have started from 133. COPY FROM DATABASE
    // rebuilds the storage, so the copy is sized by the DATA, not by the file it came
    // from.
    #[test]
    fn the_base_copy_reclaims_dead_space_instead_of_cloning_it() {
        let dir = std::env::temp_dir().join(format!("storers-compact-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let src = dir.join("bloated.duckdb");
        let dst = dir.join("compact.duckdb");
        for p in [&src, &dst] {
            let _ = std::fs::remove_file(p);
        }
        let srcs = src.to_str().unwrap().to_string();
        let dsts = dst.to_str().unwrap().to_string();

        // Bloat the source the way a bucket replacement does: write a lot, delete most
        // of it, and let DuckDB keep the file it already grew to.
        {
            let s = Store::open_rw(&srcs).unwrap();
            s.raw()
                .execute_batch(
                    "INSERT INTO specs(spec_id,series,doc_type) VALUES ('23.501','23','TS');
                     INSERT INTO clauses(chunk_id,spec_id,release,heading,text)
                       SELECT i, '23.501', 'Rel-19', 'h' || i, repeat('x', 4000)
                       FROM range(60000) t(i);",
                )
                .unwrap();
            s.checkpoint().unwrap();
            s.raw()
                .execute_batch("DELETE FROM clauses WHERE chunk_id >= 200;")
                .unwrap();
            s.checkpoint().unwrap();
        }
        let bloated = std::fs::metadata(&src).unwrap().len();

        Store::copy_database_compact(&srcs, &dsts).unwrap();
        let compact = std::fs::metadata(&dst).unwrap().len();

        // Same rows on the other side — compaction must not be lossy.
        let s = Store::open_rw(&dsts).unwrap();
        let n: i64 = s
            .raw()
            .query_row("SELECT count(*) FROM clauses", [], |r| r.get(0))
            .unwrap();
        assert_eq!(n, 200, "every surviving row must make the trip");
        let specs: i64 = s
            .raw()
            .query_row("SELECT count(*) FROM specs", [], |r| r.get(0))
            .unwrap();
        assert_eq!(specs, 1, "the catalogue travels too");

        assert!(
            compact < bloated,
            "the copy must be sized by the DATA, not by the file: {compact} vs {bloated}"
        );

        drop(s);
        let _ = std::fs::remove_dir_all(&dir);
    }

    // The copy must NOT carry the FTS index, because merge rebuilds it.
    //
    // COPY FROM DATABASE copies every schema, including the six internal tables the fts
    // extension keeps under fts_main_clauses. merge then calls enable_fts(), whose
    // PRAGMA is overwrite=1 — so a BM25 index over 2.75 M clauses was copied at length
    // and immediately thrown away. Measured on the real corpus: the copy is the whole
    // cost of a merge (77 of 79 minutes).
    #[test]
    fn the_compact_copy_leaves_the_rebuildable_fts_behind() {
        let dir = std::env::temp_dir().join(format!("storers-fts-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let src = dir.join("src.duckdb");
        let dst = dir.join("dst.duckdb");
        for p in [&src, &dst] {
            let _ = std::fs::remove_file(p);
        }
        let (s1, d1) = (
            src.to_str().unwrap().to_string(),
            dst.to_str().unwrap().to_string(),
        );
        {
            let s = Store::open_rw(&s1).unwrap();
            s.raw().execute_batch(
                "INSERT INTO specs(spec_id,series,doc_type) VALUES ('23.501','23','TS');
                 INSERT INTO clauses(chunk_id,spec_id,release,heading,text)
                   SELECT i,'23.501','Rel-19','h'||i, 'the quick brown fox jumps over the lazy dog number '||i
                   FROM range(2000) t(i);").unwrap();
            s.enable_fts().unwrap();
            s.checkpoint().unwrap();
            let n: i64 = s
                .raw()
                .query_row(
                    "SELECT count(*) FROM duckdb_tables() WHERE schema_name LIKE 'fts_%'",
                    [],
                    |r| r.get(0),
                )
                .unwrap();
            eprintln!("SOURCE: {n} table(s) internes FTS");
            assert!(n > 0, "la source doit bien avoir un index FTS");
        }
        Store::copy_database_compact(&s1, &d1).unwrap();
        let d = Store::open_rw(&d1).unwrap();

        let n: i64 = d
            .raw()
            .query_row(
                "SELECT count(*) FROM duckdb_tables() WHERE schema_name LIKE 'fts_%'",
                [],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(n, 0, "the FTS index must not be copied — merge rebuilds it");

        // Everything that is NOT rebuildable must still make the trip.
        let rows: i64 = d
            .raw()
            .query_row("SELECT count(*) FROM clauses", [], |r| r.get(0))
            .unwrap();
        assert_eq!(rows, 2000, "every clause must survive the copy");
        let specs: i64 = d
            .raw()
            .query_row("SELECT count(*) FROM specs", [], |r| r.get(0))
            .unwrap();
        assert_eq!(specs, 1, "the catalogue travels too");

        // And the secondary indexes, dropped for the bulk insert, must be back.
        let idx: i64 = d
            .raw()
            .query_row(
                "SELECT count(*) FROM duckdb_indexes() WHERE index_name IN
               ('clauses_spec','clauses_rel','clauses_path')",
                [],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(
            idx, 3,
            "the secondary indexes must be rebuilt after the bulk insert"
        );

        // The rebuild itself must still work on the copy.
        d.enable_fts()
            .expect("FTS must be buildable on the copied corpus");

        drop(d);
        let _ = std::fs::remove_dir_all(&dir);
    }

    // ADR 0004 leaves `clauses` as a VIEW over the occurrences. schema.sql carries
    // three CREATE INDEX statements against that name, and DuckDB answers them with
    // "can only create an index on a base table"; execute_batch is all-or-nothing,
    // so every write-side tool used to die at bootstrap on a converted corpus,
    // before reading a row. freeze-hnsw, whose whole job is to index a corpus in
    // exactly that state, included.
    #[test]
    fn open_rw_bootstraps_a_corpus_that_serves_clauses_as_a_view() {
        let dir = std::env::temp_dir().join(format!("storers-viewrw-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let p = dir.join("converted.duckdb");
        let _ = std::fs::remove_file(&p);
        let path = p.to_str().unwrap().to_string();

        {
            let s = Store::open_rw(&path).unwrap();
            s.raw()
                .execute_batch(
                    "INSERT INTO specs(spec_id,series,doc_type) VALUES ('23.501','23','TS');
                     INSERT INTO clauses(chunk_id,spec_id,heading,text) VALUES (1,'23.501','h','t');
                     -- what --drop-clauses leaves behind, in miniature
                     DROP TABLE clauses;
                     CREATE VIEW clauses AS
                       SELECT 1::UBIGINT AS chunk_id, '23.501' AS spec_id, NULL AS release,
                              NULL AS version, NULL AS clause_path, 'h' AS heading, 't' AS text,
                              true AS is_normative, NULL::FLOAT[1024] AS embedding,
                              NULL AS embedding_hash;",
                )
                .unwrap();
        }

        // The whole assertion: this open used to fail, and the corpus is readable
        // through the view afterwards.
        let s = Store::open_rw(&path).expect("open_rw must bootstrap a converted corpus");
        let n: i64 = s
            .raw()
            .query_row("SELECT count(*) FROM clauses", [], |r| r.get(0))
            .unwrap();
        assert_eq!(n, 1, "the view still answers after the schema was applied");

        // And the three indexes it could not create are genuinely absent, rather
        // than the statements having silently succeeded against something else.
        let idx: i64 = s
            .raw()
            .query_row(
                "SELECT count(*) FROM duckdb_indexes() WHERE index_name IN
                   ('clauses_spec','clauses_rel','clauses_path')",
                [],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(idx, 0, "a view cannot carry those indexes");

        let _ = std::fs::remove_dir_all(&dir);
    }
}

#[cfg(test)]
mod import_via_read_json {
    use super::*;

    /// Can DuckDB ingest the ledger DIRECTLY, without Rust parsing it and without
    /// generating SQL text? If so the import collapses from
    ///   serde parse -> 1024 f32::to_string -> 28 GB of SQL -> DuckDB re-parses
    /// to a single statement over the file. This test decides it on real syntax
    /// rather than on hope.
    #[test]
    fn duckdb_reads_a_jsonl_ledger_into_a_float_array() {
        let store = Store::in_memory().expect("open");
        store
            .raw()
            .execute_batch(
                "INSERT INTO clauses(chunk_id,spec_id,release,version,clause_path,heading,text,is_normative)
                 VALUES (1,'23.501','Rel-19','19.5.0','5.1','A','alpha',true),
                        (2,'23.501','Rel-19','19.5.0','5.2','B','beta',true);",
            )
            .expect("seed");

        // A ledger with a SHORT vector so the fixture stays readable; the cast target
        // is what matters, not the width.
        let dir = std::env::temp_dir().join(format!("rj-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let led = dir.join("ledger.jsonl");
        std::fs::write(
            &led,
            "{\"chunk_id\":1,\"hash\":\"aaaa\",\"vec\":[0.5,0.25,0.125,0.0625]}\n\
             {\"chunk_id\":2,\"hash\":\"bbbb\",\"vec\":[1.0,2.0,3.0,4.0]}\n",
        )
        .unwrap();

        let p = led.to_str().unwrap().replace('\\', "/");
        let sql = format!(
            "CREATE OR REPLACE TEMP TABLE _l AS
               SELECT chunk_id::UBIGINT AS chunk_id,
                      hash::VARCHAR      AS embedding_hash,
                      vec                AS vec
               FROM read_json('{p}', format='newline_delimited',
                              columns={{chunk_id:'UBIGINT', hash:'VARCHAR', vec:'FLOAT[]'}});"
        );
        store.raw().execute_batch(&sql).expect("read_json");

        let n: i64 = store
            .raw()
            .query_row("SELECT count(*) FROM _l", [], |r| r.get(0))
            .unwrap();
        assert_eq!(n, 2, "read_json must see both ledger lines");

        let v: f32 = store
            .raw()
            .query_row("SELECT vec[2] FROM _l WHERE chunk_id = 1", [], |r| r.get(0))
            .unwrap();
        assert!((v - 0.25).abs() < 1e-6, "vec[2] came back {v}");

        let _ = std::fs::remove_dir_all(&dir);
    }
}

#[cfg(test)]
mod import_bench {
    use super::*;

    fn seed(store: &Store, n: u64) {
        let mut sql = String::from("BEGIN;");
        for i in 1..=n {
            sql.push_str(&format!(
                "INSERT INTO clauses(chunk_id,spec_id,release,version,clause_path,heading,text,is_normative) \
                 VALUES ({i},'23.501','Rel-19','19.5.0','5.{i}','H','body {i}',true);"
            ));
        }
        sql.push_str("COMMIT;");
        store.raw().execute_batch(&sql).unwrap();
    }

    fn ledger(path: &std::path::Path, n: u64) {
        use std::io::Write;
        let f = std::fs::File::create(path).unwrap();
        let mut w = std::io::BufWriter::new(f);
        let v: Vec<String> = (0..DENSE_DIM)
            .map(|i| format!("{:.6}", i as f32 * 1e-4))
            .collect();
        let joined = v.join(",");
        for i in 1..=n {
            writeln!(
                w,
                "{{\"chunk_id\":{i},\"hash\":\"h{i}\",\"vec\":[{joined}]}}"
            )
            .unwrap();
        }
        w.flush().unwrap();
    }

    /// A/B on the SAME ledger and the same row count. The point is not a precise
    /// speed-up figure -- an in-memory DB is not the 8 GB corpus -- but whether the
    /// set-based path is faster at all, and by roughly how much, before it replaces a
    /// working import on a multi-hour pipeline.
    #[test]
    fn set_based_import_beats_the_row_loop() {
        const N: u64 = 3000;
        let dir = std::env::temp_dir().join(format!("imp-bench-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let led = dir.join("l.jsonl");
        ledger(&led, N);

        // OLD: parse in Rust, format back to decimal, generate SQL.
        let a = Store::in_memory().unwrap();
        seed(&a, N);
        let t0 = std::time::Instant::now();
        {
            let txt = std::fs::read_to_string(&led).unwrap();
            let (mut ids, mut vecs, mut hs) = (Vec::new(), Vec::new(), Vec::new());
            for line in txt.lines() {
                let j: serde_json::Value = serde_json::from_str(line).unwrap();
                ids.push(j["chunk_id"].as_u64().unwrap());
                vecs.push(
                    j["vec"]
                        .as_array()
                        .unwrap()
                        .iter()
                        .map(|x| x.as_f64().unwrap() as f32)
                        .collect::<Vec<f32>>(),
                );
                hs.push(j["hash"].as_str().unwrap().to_string());
                if ids.len() >= 512 {
                    a.set_embeddings_batch(&ids, &vecs, &hs).unwrap();
                    ids.clear();
                    vecs.clear();
                    hs.clear();
                }
            }
            if !ids.is_empty() {
                a.set_embeddings_batch(&ids, &vecs, &hs).unwrap();
            }
        }
        let old = t0.elapsed();

        // NEW: DuckDB reads the file.
        let b = Store::in_memory().unwrap();
        seed(&b, N);
        let t1 = std::time::Instant::now();
        let (staged, embedded) = b.import_ledger(led.to_str().unwrap()).unwrap();
        let new = t1.elapsed();

        assert_eq!(staged, N as i64, "every ledger row must be staged");
        assert_eq!(embedded, N as i64, "every clause must end up embedded");

        // Same result, not just same count: spot-check a value survived.
        let av: f32 = a
            .raw()
            .query_row(
                "SELECT embedding[3] FROM clauses WHERE chunk_id = 7",
                [],
                |r| r.get(0),
            )
            .unwrap();
        let bv: f32 = b
            .raw()
            .query_row(
                "SELECT embedding[3] FROM clauses WHERE chunk_id = 7",
                [],
                |r| r.get(0),
            )
            .unwrap();
        assert!((av - bv).abs() < 1e-6, "paths disagree: {av} vs {bv}");

        eprintln!(
            "IMPORT BENCH n={N}: row-loop {:?}, set-based {:?}  ({:.1}x)",
            old,
            new,
            old.as_secs_f64() / new.as_secs_f64().max(1e-9)
        );
        let _ = std::fs::remove_dir_all(&dir);
    }
}
