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

    /// clauses_needing_embedding streams the work-list (never-embedded, embeddable
    /// clauses), oldest chunk first, capped at `limit` (0 = all). Mirrors the Go
    /// ClausesNeedingEmbedding ResumeOnly path so the Rust embedder can read the
    /// work-list straight from the DB instead of a JSONL bridge.
    pub fn clauses_needing_embedding(&self, limit: usize, floor_ord: i64) -> Result<Vec<WorkItem>> {
        // Carry `release` so the floor (release ordinal ≥ floor_ord) is applied in Rust — the
        // Rel-99→3 special makes a pure-SQL ordinal awkward (== Go ClausesNeedingEmbedding
        // FloorOrd). floor_ord ≤ 0 = no floor.
        let sql = format!(
            "SELECT chunk_id, COALESCE(release,''), COALESCE(heading,''), COALESCE(text,'') FROM clauses
             WHERE embedding IS NULL AND {EMBEDDABLE_TEXT_SQL} ORDER BY chunk_id"
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

        // The cast to the FIXED width happens only here: staging is FLOAT[] (variable),
        // the column is FLOAT[1024], and the filter above has already guaranteed every
        // surviving row is exactly that wide.
        self.conn
            .execute_batch(&format!(
                "UPDATE clauses SET embedding = l.vec::FLOAT[{DENSE_DIM}],
                                    embedding_hash = l.embedding_hash
                 FROM _ledger AS l WHERE clauses.chunk_id = l.chunk_id;
                 DROP TABLE _ledger;
                 COMMIT;"
            ))
            .context("apply ledger to clauses")?;

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
        let sql = format!(
            "CHECKPOINT;
             SET memory_limit = '{buf}';
             SET preserve_insertion_order = false;
             {knobs}
             INSTALL vss; LOAD vss; SET hnsw_enable_experimental_persistence = true;
             CREATE INDEX IF NOT EXISTS clauses_hnsw ON clauses USING HNSW (embedding) WITH (metric = 'cosine');
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
        let wl = s.clauses_needing_embedding(0, 0).unwrap();
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
        let v: Vec<String> = (0..DENSE_DIM).map(|i| format!("{:.6}", i as f32 * 1e-4)).collect();
        let joined = v.join(",");
        for i in 1..=n {
            writeln!(w, "{{\"chunk_id\":{i},\"hash\":\"h{i}\",\"vec\":[{joined}]}}").unwrap();
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
                    j["vec"].as_array().unwrap().iter()
                        .map(|x| x.as_f64().unwrap() as f32).collect::<Vec<f32>>(),
                );
                hs.push(j["hash"].as_str().unwrap().to_string());
                if ids.len() >= 512 {
                    a.set_embeddings_batch(&ids, &vecs, &hs).unwrap();
                    ids.clear(); vecs.clear(); hs.clear();
                }
            }
            if !ids.is_empty() { a.set_embeddings_batch(&ids, &vecs, &hs).unwrap(); }
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
        let av: f32 = a.raw()
            .query_row("SELECT embedding[3] FROM clauses WHERE chunk_id = 7", [], |r| r.get(0)).unwrap();
        let bv: f32 = b.raw()
            .query_row("SELECT embedding[3] FROM clauses WHERE chunk_id = 7", [], |r| r.get(0)).unwrap();
        assert!((av - bv).abs() < 1e-6, "paths disagree: {av} vs {bv}");

        eprintln!(
            "IMPORT BENCH n={N}: row-loop {:?}, set-based {:?}  ({:.1}x)",
            old, new, old.as_secs_f64() / new.as_secs_f64().max(1e-9)
        );
        let _ = std::fs::remove_dir_all(&dir);
    }
}
