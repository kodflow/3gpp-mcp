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
}
