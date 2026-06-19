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
}
