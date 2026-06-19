//! merge (Rust) — fold disjoint per-series/-release shard DuckDBs into one corpus DB via
//! store-rs (== Go cmd/merge). ATTACH each shard, offset its clause chunk_ids by the
//! running max so synthetic PKs stay distinct across shards, copy the vectors as-is, then
//! rebuild FTS + the frozen HNSW ONCE on the merged DB (per-shard HNSW indexes are not
//! concatenable — internal/store/CLAUDE.md).
//!
//! Usage: merge --out merged.duckdb [--model ID] [--no-index] shard1.duckdb shard2.duckdb …
use anyhow::{Context, Result};
use clap::Parser;
use store_rs::Store;

#[derive(Parser)]
#[command(
    name = "merge",
    about = "Fold per-shard DuckDBs into one corpus DB (Rust store-rs port of cmd/merge)"
)]
struct Args {
    /// Output merged DuckDB (overwritten).
    #[arg(long)]
    out: String,
    /// EmbedIdentity stamped into the merged DB's frozen HNSW.
    #[arg(long, default_value = "")]
    model: String,
    /// Fold rows only; skip the FTS + HNSW rebuild (a later pass will index).
    #[arg(long, default_value_t = false)]
    no_index: bool,
    /// Shard DuckDB paths to fold (disjoint on (spec, release)).
    shards: Vec<String>,
}

fn main() -> Result<()> {
    let args = Args::parse();
    if args.shards.is_empty() {
        anyhow::bail!("merge: pass at least one shard path");
    }
    let _ = std::fs::remove_file(&args.out);
    let store = Store::open_rw(&args.out).context("open merged DB")?;

    for shard in &args.shards {
        let offset = store.max_chunk_id()?;
        store.fold_shard(shard, offset)?;
        eprintln!("merge: folded {shard} (chunk_id offset {offset})");
    }

    // Producer marker (Phase 11a A14): the merged corpus is stamped as Rust-produced so a
    // Go read-side OpenReadOnly can assert "served a DB built by the Rust write-side" and
    // warn on a schema_version mismatch. schema_meta is read-only to the serve side.
    store.set_meta("producer", "rust-writeside")?;
    store.set_meta("schema_version", "1")?;
    store.checkpoint()?;

    if !args.no_index {
        // FTS is best-effort (degrade, never block); HNSW only on a fully-embedded corpus.
        if let Err(e) = store.enable_fts() {
            eprintln!("merge: FTS build skipped ({e})");
        }
        if store.count_null_embeddings()? == 0 {
            let model = if args.model.is_empty() {
                "merged"
            } else {
                &args.model
            };
            store.build_and_freeze_hnsw(model)?;
            eprintln!("merge: built FTS + frozen HNSW (model={model})");
        } else {
            eprintln!("merge: some clauses still lack vectors — FTS built, HNSW skipped");
        }
    }
    eprintln!("merge: wrote {}", args.out);
    Ok(())
}
