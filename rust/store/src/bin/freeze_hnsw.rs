//! freeze-hnsw (Rust) — build + freeze the cosine HNSW index on an already-embedded DuckDB
//! via store-rs (== Go cmd/freeze-hnsw). The corpus-data-image bake's final step, AFTER the
//! vector overlay + compaction (COPY FROM DATABASE drops custom indexes, so the index is
//! (re)built here). Indexes the EXISTING embedding column — no embedder, no ONNX. The model
//! is read from the DB meta so the freeze never disagrees with what the overlay stamped.
use anyhow::{bail, Context, Result};
use clap::Parser;
use store_rs::Store;

#[derive(Parser)]
#[command(
    name = "freeze-hnsw",
    about = "Build + freeze the cosine HNSW index (Rust port of cmd/freeze-hnsw)"
)]
struct Args {
    /// Path to the embedded DuckDB to index (writable).
    #[arg(long)]
    db: String,
}

fn main() -> Result<()> {
    let args = Args::parse();
    let store = Store::open_rw(&args.db).context("open db")?;
    let model = store.get_meta("embedding_model")?;
    if model.is_empty() {
        bail!(
            "no embedding_model in meta — DB is not embedded (refusing to index an empty column)"
        );
    }
    store.build_and_freeze_hnsw(&model)?;
    let state = store.get_meta("hnsw_state")?;
    if state != "frozen" {
        bail!("post-state is {state:?}, want frozen");
    }
    eprintln!("freeze-hnsw: frozen cosine HNSW index for model {model}");
    Ok(())
}
