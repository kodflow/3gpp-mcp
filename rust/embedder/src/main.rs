//! embedder — the optimised Rust BGE-M3 dense embedder for the 3GPP/ETSI corpus.
//!
//! ## Why Rust, and why JSONL in/out
//!
//! The embedding *compute* (ONNX Runtime + tokenisation) is the expensive, parallel,
//! GPU-friendly part — that is what moves to Rust (ort + tokenizers, see model.rs). All
//! DuckDB I/O stays in Go, which owns the exact DuckDB version that wrote the corpus. So
//! this binary never opens the database: it reads a JSONL **work-list** and writes a
//! JSONL **vector file**, and the Go side (`cmd/embed-io`) exports the work-list and
//! imports the vectors. This decoupling removes any DuckDB storage-format compatibility
//! risk and makes the Kaggle binary tiny.
//!
//! ## Throughput: VRAM-aware dynamic batching
//!
//! The work-list is tokenised once, length-sorted, and packed into batches whose SIZE
//! adapts to each batch's sequence length and the detected free VRAM (see batch.rs +
//! gpu.rs). This keeps peak GPU memory ~constant and the GPU saturated across the whole
//! length range — instead of a fixed clause-count batch that underfills the GPU on the
//! many short clauses and OOMs on the few long ones. A runtime CUDA out-of-memory is
//! caught and the batch is split (and the memory model shrunk) so the run self-corrects
//! to the actual card without a restart.
//!
//! ## Contract
//!
//! Input  (--in):  one JSON object per line: {"chunk_id":U64,"heading":S,"text":S}
//! Output (--out): one JSON object per line: {"chunk_id":U64,"hash":S,"vec":[f32;1024]}
//!
//! `hash` is byte-for-byte the Go `embed.ClauseHash` (see hash.rs). The output file is
//! the resume ledger: chunk_ids already present are skipped and new vectors APPENDED, so
//! a crashed/killed run resumes from where it stopped. `--limit` caps a bounded session.

mod batch;
mod gpu;
mod hash;
mod model;

use std::collections::HashSet;
use std::fs::{File, OpenOptions};
use std::io::{BufRead, BufReader, BufWriter, Write};
use std::path::{Path, PathBuf};
use std::time::Instant;

use anyhow::{Context, Result};
use clap::Parser;
use serde::{Deserialize, Serialize};

use crate::batch::MemModel;
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

    /// CPU-fallback batch size (used only when no GPU is detected). On GPU the batch is
    /// sized dynamically from VRAM (see --vram-fraction / --max-batch).
    #[arg(long, default_value_t = 64)]
    batch: usize,

    /// Fraction of FREE VRAM (measured after the model loads) to spend on activations.
    /// 0.8 leaves headroom for transient peaks; the OOM backoff lowers it in effect.
    #[arg(long, default_value_t = 0.8)]
    vram_fraction: f64,

    /// Hard cap on batch size for short clauses, so a tiny-sequence batch can't explode
    /// the (un-modelled) linear-activation term. Raise on big-VRAM cards to saturate.
    #[arg(long, default_value_t = 512)]
    max_batch: usize,

    /// Cap the work-list to N NEW clauses (0 = no cap). Bounded sessions resume next run.
    #[arg(long, default_value_t = 0)]
    limit: usize,

    /// Hard-fail (with the ONNX Runtime error) if the CUDA execution provider can't be
    /// registered, instead of silently falling back to CPU (~13 clause/s).
    #[arg(long, default_value_t = false)]
    require_cuda: bool,
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

/// Prepared is a work item after tokenisation: the id row plus the metadata needed to
/// write its output record. AsRef<[i64]> lets model.embed_ids consume a slice of these
/// directly, without cloning the id vectors per batch.
struct Prepared {
    chunk_id: u64,
    hash: String,
    ids: Vec<i64>,
}

impl AsRef<[i64]> for Prepared {
    fn as_ref(&self) -> &[i64] {
        &self.ids
    }
}

fn main() -> Result<()> {
    let args = Args::parse();

    // Surface ort's logs (default WARN, ort=DEBUG): EP-registration failures — e.g. WHY
    // the CUDA provider fell back to CPU — are logged via `tracing`.
    let _ = tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env()
                .unwrap_or_else(|_| tracing_subscriber::EnvFilter::new("warn,ort=debug")),
        )
        .with_writer(std::io::stderr)
        .try_init();

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

    // Read the work-list, skipping resumed/empty ids and honouring --limit. We keep the
    // metadata (chunk_id, hash) and the text to embed; the hash is computed now from the
    // FULL heading+text (so a later Go re-embed gate is a no-op).
    let mut chunk_ids: Vec<u64> = Vec::new();
    let mut hashes: Vec<String> = Vec::new();
    let mut texts: Vec<String> = Vec::new();
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
            chunk_ids.push(it.chunk_id);
            hashes.push(hash::clause_hash(
                &it.heading,
                &it.text,
                &args.embed_identity,
            ));
            texts.push(hash::embed_text(&it.heading, &it.text));
            if args.limit > 0 && chunk_ids.len() >= args.limit {
                break;
            }
        }
    }
    if chunk_ids.is_empty() {
        eprintln!("embedder: nothing to do (work-list empty or fully resumed)");
        // Touch the output so a downstream import opens a real (possibly empty) file.
        OpenOptions::new()
            .create(true)
            .append(true)
            .open(&args.out)
            .with_context(|| format!("touch output {:?}", args.out))?;
        return Ok(());
    }
    let total = chunk_ids.len();
    eprintln!("embedder: {total} clause(s) to embed");

    let bge = Bge::load(
        &args.model_dir.join(&args.onnx),
        &args.model_dir.join("tokenizer.json"),
        args.require_cuda,
    )?;

    // Tokenise the whole work-list once (windowed to bound transient memory). The ids let
    // us length-sort and size batches by token budget without re-tokenising at inference.
    let mut prepared: Vec<Prepared> = Vec::with_capacity(total);
    {
        const TOK_WINDOW: usize = 8192;
        let mut idx = 0usize;
        for w in texts.chunks(TOK_WINDOW) {
            let rows = bge.encode(w).context("tokenise work-list window")?;
            for ids in rows {
                prepared.push(Prepared {
                    chunk_id: chunk_ids[idx],
                    hash: std::mem::take(&mut hashes[idx]),
                    ids,
                });
                idx += 1;
            }
        }
    }
    drop(texts);

    // Length-bucket: sort by token count so each batch holds clauses of similar length
    // (tiny padding) and the dynamic batcher can size by the batch's true sequence length.
    prepared.sort_by_key(|p| p.ids.len());
    let lens: Vec<usize> = prepared.iter().map(|p| p.ids.len()).collect();
    log_distribution(&lens);

    // Size the memory model from the FREE VRAM measured after the model loaded (so the
    // weights are already accounted for). No GPU → fixed CPU-fallback batch.
    let mut mem = match gpu::detect() {
        Some(g) => {
            let avail = (g.free_bytes as f64) * args.vram_fraction;
            eprintln!(
                "RESULT gpu name={:?} total_mib={} free_mib={} avail_gib={:.2} fraction={} max_batch={}",
                g.name,
                g.total_bytes / (1024 * 1024),
                g.free_bytes / (1024 * 1024),
                avail / (1024.0 * 1024.0 * 1024.0),
                args.vram_fraction,
                args.max_batch
            );
            MemModel {
                avail_bytes: avail,
                k_attn: batch::K_ATTN_DEFAULT,
                max_batch: args.max_batch,
            }
        }
        None => {
            eprintln!(
                "RESULT gpu=none — CPU fallback, fixed batch={} (set --require-cuda on a GPU box to fail loud)",
                args.batch
            );
            // avail huge ⇒ batch_for_len always saturates to max_batch = --batch (the old
            // fixed-batch behaviour) without risking inf arithmetic.
            MemModel {
                avail_bytes: 1.0e18,
                k_attn: batch::K_ATTN_DEFAULT,
                max_batch: args.batch.max(1),
            }
        }
    };
    eprintln!(
        "RESULT batch_plan seq32={} seq128={} seq256={} seq512={} seq1024={}",
        mem.batch_for_len(32),
        mem.batch_for_len(128),
        mem.batch_for_len(256),
        mem.batch_for_len(512),
        mem.batch_for_len(1024)
    );

    // Append to the output ledger so a resumed run extends it crash-safely.
    let out = OpenOptions::new()
        .create(true)
        .append(true)
        .open(&args.out)
        .with_context(|| format!("open output {:?}", args.out))?;
    let mut w = BufWriter::new(out);

    const PROGRESS_EVERY: usize = 2000;
    let start = Instant::now();
    let mut done_n = 0usize;
    let mut next_log = PROGRESS_EVERY;
    eprintln!("PROGRESS 0/{total} (0%) starting…");

    let mut i = 0usize;
    while i < total {
        let end = batch::batch_end(&lens, i, &mem);
        let vecs = run_adaptive(&bge, &mut mem, &prepared[i..end])
            .with_context(|| format!("embed batch [{i}, {end})"))?;
        if vecs.len() != end - i {
            anyhow::bail!("embed returned {} vecs for {} inputs", vecs.len(), end - i);
        }
        for (p, vec) in prepared[i..end].iter().zip(vecs) {
            if vec.len() != DENSE_DIM {
                anyhow::bail!(
                    "clause {} got dim {}, want {}",
                    p.chunk_id,
                    vec.len(),
                    DENSE_DIM
                );
            }
            let rec = VecRecord {
                chunk_id: p.chunk_id,
                hash: p.hash.clone(),
                vec,
            };
            serde_json::to_writer(&mut w, &rec).context("serialize vector")?;
            w.write_all(b"\n")?;
        }
        // Flush per batch: the on-disk ledger is always a valid resume point.
        w.flush()?;
        done_n += end - i;
        i = end;
        if done_n >= next_log || done_n == total {
            let secs = start.elapsed().as_secs_f64().max(0.001);
            let rate = done_n as f64 / secs;
            let pct = done_n * 100 / total.max(1);
            let eta = if rate > 0.0 {
                ((total - done_n) as f64 / rate) as u64
            } else {
                0
            };
            eprintln!("PROGRESS {done_n}/{total} ({pct}%) {rate:.1} clause/s eta {eta}s");
            next_log = done_n + PROGRESS_EVERY;
        }
    }
    eprintln!("PROGRESS {total}/{total} (100%) done");
    eprintln!("embedder: wrote {total} vector(s) to {:?}", args.out);
    Ok(())
}

/// run_adaptive embeds one batch, and on a CUDA out-of-memory shrinks the memory model
/// (so every later batch is smaller too) and splits the batch in half, recursively, down
/// to a single clause. A non-OOM error propagates unchanged.
fn run_adaptive(bge: &Bge, mem: &mut MemModel, rows: &[Prepared]) -> Result<Vec<Vec<f32>>> {
    match bge.embed_ids(rows) {
        Ok(v) => Ok(v),
        Err(e) if is_oom(&e) => {
            mem.shrink(1.5);
            if rows.len() <= 1 {
                return Err(e.context("CUDA OOM on a single clause (raise --vram-fraction headroom or lower MAX_TOKENS)"));
            }
            let mid = rows.len() / 2;
            eprintln!(
                "PROGRESS oom — splitting batch {} into {}+{}, k_attn now {:.0}",
                rows.len(),
                mid,
                rows.len() - mid,
                mem.k_attn
            );
            let mut a = run_adaptive(bge, mem, &rows[..mid])?;
            let mut b = run_adaptive(bge, mem, &rows[mid..])?;
            a.append(&mut b);
            Ok(a)
        }
        Err(e) => Err(e),
    }
}

/// is_oom recognises a CUDA/host out-of-memory in an ort error so the caller can back off
/// instead of aborting the whole campaign.
fn is_oom(e: &anyhow::Error) -> bool {
    let s = format!("{e:?}").to_lowercase();
    s.contains("out of memory")
        || s.contains("cudaerrormemoryallocation")
        || s.contains("cuda_error_out_of_memory")
        || s.contains("failed to allocate")
        || s.contains("bad_alloc")
}

/// log_distribution prints the REAL token-length distribution of this work-list (the data
/// that drives batch sizing) so the campaign log shows what we're actually embedding.
/// `lens` must be ascending (the work-list is length-sorted before this call).
fn log_distribution(lens: &[usize]) {
    let n = lens.len();
    if n == 0 {
        return;
    }
    let pct = |q: usize| lens[((n - 1) * q) / 100];
    let count_le = |t: usize| lens.partition_point(|&l| l <= t);
    eprintln!(
        "RESULT token_dist n={} min={} p50={} p90={} p99={} max={} | <=64:{} <=128:{} <=256:{} <=512:{} <=1024:{}",
        n,
        lens[0],
        pct(50),
        pct(90),
        pct(99),
        lens[n - 1],
        count_le(64),
        count_le(128),
        count_le(256),
        count_le(512),
        count_le(1024),
    );
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
        // Only the chunk_id is needed; parse leniently so a truncated final line never
        // aborts resume — it is simply re-embedded.
        if let Ok(v) = serde_json::from_str::<serde_json::Value>(&line) {
            if let Some(id) = v.get("chunk_id").and_then(|x| x.as_u64()) {
                done.insert(id);
            }
        }
    }
    Ok(done)
}
