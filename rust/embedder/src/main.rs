//! embedder — the optimised Rust BGE-M3 dense embedder for the 3GPP/ETSI corpus.
//!
//! ## Why Rust, and why JSONL in/out
//!
//! The embedding *compute* (ONNX Runtime + tokenisation) is the expensive,
//! parallel, GPU-friendly part — that is what moves to Rust (ort + tokenizers, see
//! model.rs). All DuckDB I/O stays in Go, which owns the exact DuckDB version that
//! wrote the corpus. So this binary never opens the database: it reads a JSONL
//! **work-list** and writes a JSONL **vector file**, and the Go side (`cmd/embed-io`)
//! exports the work-list and imports the vectors. This decoupling removes any DuckDB
//! storage-format compatibility risk and makes the Kaggle binary tiny.
//!
//! ## Contract
//!
//! Input  (--in):  one JSON object per line: {"chunk_id":U64,"heading":S,"text":S}
//! Output (--out): one JSON object per line: {"chunk_id":U64,"hash":S,"vec":[f32;1024]}
//!
//! `hash` is byte-for-byte the Go `embed.ClauseHash` (see hash.rs), computed from the
//! SAME `--embed-identity` the Go `cmd/embedid` prints — so importing these vectors
//! makes a later Go re-embed gate a no-op (no churn).
//!
//! ## Resume
//!
//! The output file is the resume ledger: on start every chunk_id already present in
//! --out is skipped, and new vectors are APPENDED. A crashed/killed run therefore
//! resumes from where it stopped — never recomputes a written vector — matching the
//! Go embedder's micro-granular resume. `--limit` caps a bounded session.

mod hash;
mod model;

use std::collections::HashSet;
use std::fs::{File, OpenOptions};
use std::io::{BufRead, BufReader, BufWriter, Write};
use std::path::{Path, PathBuf};

use anyhow::{Context, Result};
use clap::Parser;
use indicatif::{ProgressBar, ProgressStyle};
use serde::{Deserialize, Serialize};

use crate::model::Bge;

/// BGE-M3 dense dimensionality (must match clauses.embedding FLOAT[1024]).
const DENSE_DIM: usize = 1024;

#[derive(Parser, Debug)]
#[command(
    name = "embedder",
    about = "Rust BGE-M3 dense embedder (JSONL work-list -> JSONL vectors); Go owns DuckDB"
)]
struct Args {
    /// JSONL work-list: {"chunk_id":U64,"heading":S,"text":S} per line.
    #[arg(long)]
    r#in: PathBuf,

    /// JSONL output (append + resume ledger): {"chunk_id":U64,"hash":S,"vec":[..]} per line.
    #[arg(long)]
    out: PathBuf,

    /// Directory holding the BGE-M3 files: the ONNX (--onnx) + its external data
    /// (model.onnx_data, loaded automatically) + tokenizer.json.
    #[arg(long)]
    model_dir: PathBuf,

    /// ONNX file name inside --model-dir.
    #[arg(long, default_value = "model.onnx")]
    onnx: String,

    /// The canonical Go EmbedIdentity (printed by `go run ./cmd/embedid`). Required so
    /// the written hash matches Go's ClauseHash and importing causes no re-embed churn.
    #[arg(long)]
    embed_identity: String,

    /// Clauses per ONNX batch.
    #[arg(long, default_value_t = 32)]
    batch: usize,

    /// Cap the work-list to N NEW clauses (0 = no cap). Bounded sessions resume next run.
    #[arg(long, default_value_t = 0)]
    limit: usize,
}

#[derive(Deserialize)]
struct WorkItem {
    chunk_id: u64,
    #[serde(default)]
    heading: String,
    #[serde(default)]
    text: String,
}

#[derive(Serialize)]
struct VecRecord {
    chunk_id: u64,
    hash: String,
    vec: Vec<f32>,
}

fn main() -> Result<()> {
    let args = Args::parse();

    // Resume ledger: chunk_ids already embedded in --out are skipped.
    let done =
        load_done(&args.out).with_context(|| format!("scan resume ledger {:?}", args.out))?;
    if !done.is_empty() {
        eprintln!(
            "embedder: resume — {} clause(s) already in {:?}",
            done.len(),
            args.out
        );
    }

    // Build the work-list, skipping resumed ids and honouring --limit.
    let mut items: Vec<WorkItem> = Vec::new();
    {
        let f =
            File::open(&args.r#in).with_context(|| format!("open work-list {:?}", args.r#in))?;
        for line in BufReader::new(f).lines() {
            let line = line?;
            if line.trim().is_empty() {
                continue;
            }
            let it: WorkItem = serde_json::from_str(&line).context("parse work-list line")?;
            if done.contains(&it.chunk_id) || it.text.trim().is_empty() {
                continue;
            }
            items.push(it);
            if args.limit > 0 && items.len() >= args.limit {
                break;
            }
        }
    }
    if items.is_empty() {
        eprintln!("embedder: nothing to do (work-list empty or fully resumed)");
        return Ok(());
    }
    eprintln!(
        "embedder: {} clause(s) to embed (batch={})",
        items.len(),
        args.batch
    );

    let model = Bge::load(
        &args.model_dir.join(&args.onnx),
        &args.model_dir.join("tokenizer.json"),
    )?;

    // Append to the output ledger so a resumed run extends it crash-safely.
    let out = OpenOptions::new()
        .create(true)
        .append(true)
        .open(&args.out)
        .with_context(|| format!("open output {:?}", args.out))?;
    let mut w = BufWriter::new(out);

    let pb = ProgressBar::new(items.len() as u64);
    pb.set_style(
        ProgressStyle::with_template(
            "{spinner} {pos}/{len} clauses ({percent}%) [{elapsed_precise}] eta {eta} {per_sec}",
        )
        .unwrap(),
    );

    for chunk in items.chunks(args.batch) {
        let texts: Vec<String> = chunk
            .iter()
            .map(|it| hash::embed_text(&it.heading, &it.text))
            .collect();
        // ort tokenises, runs the forward pass, CLS-pools and L2-normalises per row.
        let embeddings = model.embed_batch(&texts).context("embed batch")?;
        if embeddings.len() != chunk.len() {
            anyhow::bail!(
                "embed returned {} vecs for {} inputs",
                embeddings.len(),
                chunk.len()
            );
        }
        for (it, vec) in chunk.iter().zip(embeddings) {
            if vec.len() != DENSE_DIM {
                anyhow::bail!(
                    "clause {} got dim {}, want {}",
                    it.chunk_id,
                    vec.len(),
                    DENSE_DIM
                );
            }
            let rec = VecRecord {
                chunk_id: it.chunk_id,
                hash: hash::clause_hash(&it.heading, &it.text, &args.embed_identity),
                vec,
            };
            serde_json::to_writer(&mut w, &rec).context("serialize vector")?;
            w.write_all(b"\n")?;
        }
        // Flush per batch: the on-disk ledger is always a valid resume point.
        w.flush()?;
        pb.inc(chunk.len() as u64);
    }
    pb.finish_with_message("done");
    eprintln!(
        "embedder: wrote {} vector(s) to {:?}",
        items.len(),
        args.out
    );
    Ok(())
}

/// load_done returns the set of chunk_ids already present in the output ledger, so a
/// resumed run skips them. A missing file yields an empty set (fresh run).
fn load_done(out: &Path) -> Result<HashSet<u64>> {
    let mut done = HashSet::new();
    let f = match File::open(out) {
        Ok(f) => f,
        Err(_) => return Ok(done),
    };
    for line in BufReader::new(f).lines() {
        let line = line?;
        if line.trim().is_empty() {
            continue;
        }
        // Only the chunk_id is needed; parse leniently so a truncated final line
        // (killed mid-write) never aborts resume — it is simply re-embedded.
        if let Ok(v) = serde_json::from_str::<serde_json::Value>(&line) {
            if let Some(id) = v.get("chunk_id").and_then(|x| x.as_u64()) {
                done.insert(id);
            }
        }
    }
    Ok(done)
}
