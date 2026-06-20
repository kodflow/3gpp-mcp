//! roundtrip-fixture — Phase 0/2 keystone. Writes a DuckDB the Go serve side reads back
//! read-only, proving Rust (store-rs over duckdb-rs, bundled DuckDB 1.4.x) and Go
//! (go-duckdb, engine 1.4.3) share the on-disk format — the one risk the whole "Rust
//! writes DuckDB, Go reads it" migration hinges on.
//!
//! With `indexes` as the 2nd arg it also builds FTS (BM25) + the frozen HNSW vector
//! index via store-rs, so the round-trip exercises the FULL serve path (Go LoadFTS /
//! LoadVSS over a Rust-built corpus), not just the raw tables.
//!
//! Usage: roundtrip-fixture <out.duckdb> [indexes]
use anyhow::{Context, Result};
use store_rs::{Store, DENSE_DIM};

fn main() -> Result<()> {
    let path = std::env::args()
        .nth(1)
        .context("usage: roundtrip-fixture <out.duckdb> [indexes]")?;
    let with_indexes = std::env::args().nth(2).as_deref() == Some("indexes");
    let _ = std::fs::remove_file(&path);

    let s = Store::open_rw(&path)?;
    s.raw().execute_batch(
        "INSERT INTO specs(spec_id,series,title,doc_type) VALUES ('23.501','23','System architecture','TS');
         INSERT INTO clauses(chunk_id,spec_id,release,version,clause_path,heading,text,is_normative) VALUES
           (1,'23.501','Rel-19','19.0.0','5.1','5.1 Architecture','the 5G system architecture',true),
           (2,'23.501','Rel-19','19.0.0','5.2','5.2 AMF','access and mobility management function',true),
           (3,'23.501','Rel-19','19.0.0','5.3','5.3 SMF','session management function',true);",
    ).context("seed specs+clauses")?;

    // Distinct vectors so the HNSW graph is non-degenerate.
    let ids = [1u64, 2, 3];
    let vecs: Vec<Vec<f32>> = (0..3)
        .map(|k| {
            (0..DENSE_DIM)
                .map(|i| ((i + k * 7) as f32 / DENSE_DIM as f32).sin())
                .collect()
        })
        .collect();
    let hashes = vec!["h1".to_string(), "h2".to_string(), "h3".to_string()];
    s.set_embeddings_batch(&ids, &vecs, &hashes)?;
    s.set_sparse(1, &[(101, 0.9), (202, 1.4)])?;
    s.set_meta("embedding_model", "roundtrip-fixture-id")?;

    if with_indexes {
        s.enable_fts()
            .context("enable_fts (needs the fts extension)")?;
        s.build_and_freeze_hnsw("roundtrip-fixture-id")
            .context("build_and_freeze_hnsw (needs the vss extension)")?;
        eprintln!("roundtrip-fixture: wrote {path} WITH FTS + frozen HNSW");
    } else {
        s.checkpoint()?;
        eprintln!("roundtrip-fixture: wrote {path} (data only)");
    }
    Ok(())
}
