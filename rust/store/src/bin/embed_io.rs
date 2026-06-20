//! embed-io (Rust) — the store-rs port of Go's cmd/embed-io. Same JSONL contract, but
//! all DuckDB I/O goes through store-rs, so the embed pipeline writes the corpus without
//! the Go bridge. Two modes:
//!
//!   embed-io --db D --export-worklist work.jsonl [--limit N]
//!     → read the never-embedded, embeddable clauses straight from the DB and write one
//!       {"chunk_id","heading","text"} line each (what the Rust embedder consumes).
//!
//!   embed-io --db D --import-vectors vecs.jsonl --embed-identity ID [--build-hnsw] [--batch N]
//!     → read {"chunk_id","hash","vec":[f32;1024]} lines, write them into clauses
//!       (+embedding_hash) in batched transactions, stamp embedding_model, and — only on
//!       the FINAL pass (no embeddable clause left NULL) — build + freeze the HNSW.
//!       Tolerates a truncated/garbled final line (killed worker) by skipping it.
use anyhow::{Context, Result};
use clap::Parser;
use serde::{Deserialize, Serialize};
use std::io::{BufRead, BufReader, BufWriter, Write};
use store_rs::{Store, DENSE_DIM};

#[derive(Parser)]
#[command(
    name = "embed-io",
    about = "Rust store-rs port of cmd/embed-io (DuckDB I/O for the embed pipeline)"
)]
struct Args {
    /// DuckDB shard to read/write.
    #[arg(long)]
    db: String,
    /// Export the embedding work-list as JSONL to this path, then exit.
    #[arg(long)]
    export_worklist: Option<String>,
    /// Import a JSONL vector file (from the Rust embedder) into the DB, then exit.
    #[arg(long)]
    import_vectors: Option<String>,
    /// EmbedIdentity stamped into embedding_model on import (and into the frozen HNSW).
    #[arg(long, default_value = "")]
    embed_identity: String,
    /// After import, build + freeze the HNSW — but only on the final (null_after==0) pass.
    #[arg(long, default_value_t = false)]
    build_hnsw: bool,
    /// Cap the exported work-list to N clauses (0 = all).
    #[arg(long, default_value_t = 0)]
    limit: usize,
    /// Export ONLY clauses at/above this release (e.g. Rel-19); empty = all (== Go embed
    /// --embed-floor). Lexical coverage is unaffected; this only narrows what gets vectorised.
    #[arg(long, default_value = "")]
    embed_floor: String,
    /// Print the embed completeness report as JSON (model, hnsw, embedded_clauses,
    /// null_embeddings_at_floor) and exit — the CI semantic gate (== Go embed --report json).
    #[arg(long, default_value_t = false)]
    report: bool,
    /// Rows per write transaction on import.
    #[arg(long, default_value_t = 512)]
    batch: usize,
    /// Export the SPARSE work-list (embeddable clauses with no clause_sparse posting) as
    /// JSONL to this path, then exit. Mirrors --export-worklist for the sparse arm.
    #[arg(long)]
    export_sparse_worklist: Option<String>,
    /// Import a JSONL sparse-postings file (from the embed-core sparse bin) into
    /// clause_sparse, then exit. Each line: {"chunk_id":U64,"terms":[[term_id,weight],…]}.
    #[arg(long)]
    import_sparse: Option<String>,
    /// SPARSE identity stamped into meta `sparse_model` on --import-sparse (so the
    /// data-contract --require-sparse gate and discover --sparse-check can match it).
    #[arg(long, default_value = "")]
    sparse_model: String,
}

#[derive(Deserialize)]
struct SparseRecord {
    chunk_id: u64,
    #[serde(default)]
    terms: Vec<(u32, f32)>,
}

#[derive(Serialize)]
struct WlRecord {
    chunk_id: u64,
    heading: String,
    text: String,
}

#[derive(Deserialize)]
struct VecRecord {
    chunk_id: u64,
    #[serde(default)]
    hash: String,
    vec: Vec<f32>,
}

fn main() -> Result<()> {
    let args = Args::parse();
    let store = Store::open_rw(&args.db)?;

    if let Some(out) = args.export_worklist.as_deref() {
        // Resolve the optional release floor to its ordinal (unparseable → 0 = no floor).
        let floor_ord = if args.embed_floor.is_empty() {
            0
        } else {
            store_rs::identity::release_ordinal(&args.embed_floor).unwrap_or(0)
        };
        let wl = store.clauses_needing_embedding(args.limit, floor_ord)?;
        let f = std::fs::File::create(out).with_context(|| format!("create {out}"))?;
        let mut w = BufWriter::new(f);
        for it in &wl {
            serde_json::to_writer(
                &mut w,
                &WlRecord {
                    chunk_id: it.chunk_id,
                    heading: it.heading.clone(),
                    text: it.text.clone(),
                },
            )?;
            w.write_all(b"\n")?;
        }
        w.flush()?;
        eprintln!("embed-io: wrote {} clause(s) to {out}", wl.len());
        return Ok(());
    }

    if let Some(inp) = args.import_vectors.as_deref() {
        let f = std::fs::File::open(inp).with_context(|| format!("open {inp}"))?;
        let mut ids: Vec<u64> = Vec::new();
        let mut vecs: Vec<Vec<f32>> = Vec::new();
        let mut hashes: Vec<String> = Vec::new();
        let mut total = 0usize;
        let mut skipped = 0usize;
        for line in BufReader::new(f).lines() {
            let line = line?;
            if line.trim().is_empty() {
                continue;
            }
            let r: VecRecord = match serde_json::from_str(&line) {
                Ok(r) => r,
                Err(_) => {
                    // truncated/garbled line (killed worker) — skip, the clause stays NULL.
                    skipped += 1;
                    continue;
                }
            };
            if r.vec.len() != DENSE_DIM {
                skipped += 1;
                continue;
            }
            ids.push(r.chunk_id);
            vecs.push(r.vec);
            hashes.push(r.hash);
            if ids.len() >= args.batch {
                store.set_embeddings_batch(&ids, &vecs, &hashes)?;
                total += ids.len();
                ids.clear();
                vecs.clear();
                hashes.clear();
            }
        }
        if !ids.is_empty() {
            store.set_embeddings_batch(&ids, &vecs, &hashes)?;
            total += ids.len();
        }

        if !args.embed_identity.is_empty() {
            store.set_meta("embedding_model", &args.embed_identity)?;
        }

        let mut built = false;
        if args.build_hnsw {
            if args.embed_identity.is_empty() {
                anyhow::bail!("build hnsw: --embed-identity is required to stamp the frozen index");
            }
            let n = store.count_null_embeddings()?;
            if n > 0 {
                eprintln!("embed-io: {n} embeddable clause(s) still NULL — skipping HNSW build (not the final pass)");
            } else {
                store.build_and_freeze_hnsw(&args.embed_identity)?;
                built = true;
            }
        }
        if skipped > 0 {
            eprintln!("embed-io: skipped {skipped} malformed/truncated line(s) — affected clauses stay NULL and resume next pass");
        }
        eprintln!("embed-io: wrote {total} vector(s) (hnsw={built})");
        return Ok(());
    }

    if let Some(out) = args.export_sparse_worklist.as_deref() {
        let floor_ord = if args.embed_floor.is_empty() {
            0
        } else {
            store_rs::identity::release_ordinal(&args.embed_floor).unwrap_or(0)
        };
        let wl = store.clauses_needing_sparse(args.limit, floor_ord)?;
        let f = std::fs::File::create(out).with_context(|| format!("create {out}"))?;
        let mut w = BufWriter::new(f);
        for it in &wl {
            serde_json::to_writer(
                &mut w,
                &WlRecord {
                    chunk_id: it.chunk_id,
                    heading: it.heading.clone(),
                    text: it.text.clone(),
                },
            )?;
            w.write_all(b"\n")?;
        }
        w.flush()?;
        eprintln!("embed-io: wrote {} sparse work item(s) to {out}", wl.len());
        return Ok(());
    }

    if let Some(inp) = args.import_sparse.as_deref() {
        let f = std::fs::File::open(inp).with_context(|| format!("open {inp}"))?;
        let mut total = 0usize;
        let mut skipped = 0usize;
        for line in BufReader::new(f).lines() {
            let line = line?;
            if line.trim().is_empty() {
                continue;
            }
            let r: SparseRecord = match serde_json::from_str(&line) {
                Ok(r) => r,
                Err(_) => {
                    // truncated/garbled line (killed worker) — skip; the clause stays sparse-less
                    // and is re-picked next pass (clauses_needing_sparse).
                    skipped += 1;
                    continue;
                }
            };
            // An empty-postings clause (heading-only/void text) still counts as "done" so the
            // worklist converges — write the (empty) posting set, which set_sparse handles.
            store.set_sparse(r.chunk_id, &r.terms)?;
            total += 1;
        }
        if !args.sparse_model.is_empty() {
            store.set_meta("sparse_model", &args.sparse_model)?;
        }
        if skipped > 0 {
            eprintln!("embed-io: skipped {skipped} malformed sparse line(s) — affected clauses re-picked next pass");
        }
        eprintln!("embed-io: wrote sparse postings for {total} clause(s)");
        return Ok(());
    }

    if args.report {
        let model = store.get_meta("embedding_model")?;
        let hnsw = store.get_meta("hnsw_state")? == "frozen";
        let floor_ord = if args.embed_floor.is_empty() {
            0
        } else {
            store_rs::identity::release_ordinal(&args.embed_floor).unwrap_or(0)
        };
        // null_embeddings_at_floor = embeddable + still-NULL clauses at/above the floor.
        let null_at_floor = store.clauses_needing_embedding(0, floor_ord)?.len();
        let total = store.count_clauses()?;
        let null_all = store.count_null_embeddings()?;
        println!(
            "{{\n  \"model\": \"{}\",\n  \"hnsw\": {hnsw},\n  \"embedded_clauses\": {},\n  \"null_embeddings_at_floor\": {null_at_floor}\n}}",
            model.replace('"', "\\\""),
            total - null_all
        );
        return Ok(());
    }

    anyhow::bail!("embed-io: pass --export-worklist <file>, --import-vectors <file>, or --report");
}
