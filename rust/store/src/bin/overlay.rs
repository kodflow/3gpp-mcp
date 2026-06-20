//! overlay (Rust) — write the embedding vectors (+ sparse postings) from one or more vector
//! shards onto a full lexical base by NATURAL identity, via store-rs (== Go cmd/overlay).
//! The base is modified IN PLACE (pass a copy to keep the lexical original). All sources
//! feeding one fused DB must agree on embedding_model (the EmbedIdentity invariant). HNSW is
//! NOT built here — freeze it later (freeze-hnsw).
use anyhow::{Context, Result};
use clap::Parser;
use store_rs::Store;

#[derive(Parser)]
#[command(
    name = "overlay",
    about = "Fuse vector shards onto a lexical base (Rust port of cmd/overlay)"
)]
struct Args {
    /// Full lexical base DB to overlay vectors onto (modified IN PLACE — pass a copy).
    #[arg(long)]
    base: String,
    /// A vector shard (clauses+embedding) to overlay; repeat for each lot.
    #[arg(long = "vec")]
    vecs: Vec<String>,
    /// Copy the catalogue tables from this DB into the base (for a clauses-only lots-merge).
    #[arg(long = "catalogue-from", default_value = "")]
    catalogue_from: String,
    /// Canonical EmbedIdentity to stamp; shards carrying meta must agree (fail-closed).
    #[arg(long = "embedding-model", default_value = "")]
    embedding_model: String,
}

fn main() -> Result<()> {
    let args = Args::parse();
    let store = Store::open_rw(&args.base).context("open base")?;
    let (after, model) = store.overlay(&args.vecs, &args.catalogue_from, &args.embedding_model)?;
    eprintln!("overlay: done — {after} clauses vectorised, embedding_model={model:?}. HNSW NOT built (freeze later).");
    Ok(())
}
