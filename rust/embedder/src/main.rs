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
mod window;

use std::collections::{HashMap, HashSet};
use std::fs::{File, OpenOptions};
use std::io::{BufRead, BufReader, BufWriter, Write};
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicUsize, Ordering as AtomicOrdering};
use std::time::Instant;

/// OOM_SPLITS counts how many times the adaptive batcher absorbed an allocation
/// failure. It is a global because `run_adaptive` recurses and threading a counter
/// through would obscure the recursion for no gain.
///
/// It exists to make the arena cap PROVABLE from the run itself. Watching
/// `nvidia-smi` shows a human that memory stayed bounded; it cannot be asserted on,
/// it samples, and it says nothing about whether the backoff was ever exercised.
/// A run that reports `oom_splits=0 peak_batch=512` and one that reports
/// `oom_splits=5 peak_batch=256` are both healthy — but only the second proves the
/// recovery path works, and before the cap existed neither number could be produced
/// at all.
static OOM_SPLITS: AtomicUsize = AtomicUsize::new(0);
static PEAK_BATCH: AtomicUsize = AtomicUsize::new(0);

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

    /// Words per embedding window (mean_pool). The default is measured, not
    /// inherited: 300 comes from the Go reference, which sized it for ~1.3
    /// tokens/word prose, and 3GPP tables and ASN.1 tokenise at over 3 — so 300-word
    /// windows still hit MAX_TOKENS and still dropped their tails, which is the whole
    /// defect #208 is about. Exposed so the value can be re-measured against
    /// truncated_windows when the corpus or the tokenizer changes.
    #[arg(long, default_value_t = crate::window::DEFAULT_WINDOW_WORDS)]
    window_words: usize,

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

    /// CUDA device id this process runs on. The kernel launches one process per GPU
    /// (--device 0, 1, …) so an N-GPU box embeds N× in parallel.
    #[arg(long, default_value_t = 0)]
    device: i32,

    /// This process's shard index in [0, shard-count). Combined with --shard-count it
    /// makes each GPU process a DISJOINT slice of the work-list (sharded by a hash of the
    /// clause text, so identical texts land in the same shard and the dedup is preserved).
    #[arg(long, default_value_t = 0)]
    shard_index: u64,

    /// Number of shards (= number of GPUs). 1 = process the whole work-list (single GPU).
    #[arg(long, default_value_t = 1)]
    shard_count: u64,

    /// Extra resume ledger(s) to treat as already-done (in addition to --out). The
    /// multi-GPU launcher passes the merged ledger here so every per-GPU process skips
    /// what any shard already embedded. Repeatable.
    #[arg(long)]
    resume_from: Vec<PathBuf>,
}

/// shard_of maps a clause content hash to a shard in [0, count). Deterministic and
/// identical for identical text (the hash is text-derived), so every copy of a clause
/// lands in the same shard — the dedup never splits across GPUs. count==0 is treated as 1.
fn shard_of(hash: &str, count: u64) -> u64 {
    if count <= 1 {
        return 0;
    }
    // First 16 hex chars of the (sha-hex) hash as a u64; fall back to a byte FNV if the
    // hash is shorter/non-hex so the function is total.
    let v = u64::from_str_radix(hash.get(..16).unwrap_or(""), 16).unwrap_or_else(|_| {
        let mut h: u64 = 0xcbf2_9ce4_8422_2325;
        for b in hash.bytes() {
            h ^= b as u64;
            h = h.wrapping_mul(0x0000_0100_0000_01b3);
        }
        h
    });
    v % count
}

#[derive(Deserialize)]
struct WorkItem {
    chunk_id: u64,
    #[serde(default)]
    heading: String,
    #[serde(default)]
    text: String,
}

#[derive(Serialize, Deserialize)]
struct VecRecord {
    chunk_id: u64,
    hash: String,
    vec: Vec<f32>,
}

/// Slot is one MODEL INPUT after windowing and tokenisation: a single window of one
/// clause, plus enough identity to fold it back. Windowing turns the clause→input
/// relation from 1:1 into 1:N, and mean-pooling turns it back at the writer.
///
/// The clause's own metadata (chunk_id, hash) is NOT duplicated here — a long clause has
/// hundreds of windows and would carry hundreds of copies of the same String. It stays in
/// the parallel `chunk_ids` / `hashes` vectors, indexed by `clause`.
struct Slot {
    clause: u32,
    window: u32,
    ids: Vec<i64>,
}

impl AsRef<[i64]> for Slot {
    fn as_ref(&self) -> &[i64] {
        &self.ids
    }
}

/// Pending holds the window vectors of a MULTI-window clause until every one has come
/// back from the GPU. Single-window clauses never land here: they are written straight
/// through, exactly as before windowing existed.
///
/// The vectors are kept in WINDOW ORDER, not arrival order, and pooled only when the
/// clause is complete — mean_pool_l2 sums in f64 and float addition is not associative,
/// so pooling in arrival order would make the result depend on how the batcher happened
/// to sort the work, and no two runs would agree.
struct Pending {
    windows: Vec<Vec<f32>>,
    filled: u32,
}

/// Resume is what a prior (partial) run left in the output ledger: the chunk_ids already
/// embedded (skip them) and a content-hash → vector map (fill a not-yet-done clause whose
/// text was already embedded under another chunk_id, instead of re-embedding it).
#[derive(Default)]
struct Resume {
    done: HashSet<u64>,
    by_hash: std::collections::HashMap<String, Vec<f32>>,
}

fn main() -> Result<()> {
    let mut args = Args::parse();

    // Clamp --vram-fraction to a sane (0, 1] range: a value >1 (typo, e.g. `--vram-fraction
    // 8`) would oversubscribe VRAM and trigger avoidable CUDA OOMs, while a negative/NaN
    // value makes avail_bytes ≤0 and collapses every batch to size 1. The OOM backoff would
    // eventually recover, but clamping is cheaper and fails-soft with a clear log.
    if !(args.vram_fraction > 0.0 && args.vram_fraction <= 1.0) {
        let clamped = if args.vram_fraction.is_finite() {
            args.vram_fraction.clamp(0.05, 1.0)
        } else {
            0.8
        };
        eprintln!(
            "embedder: --vram-fraction {} out of (0,1] — clamped to {clamped}",
            args.vram_fraction
        );
        args.vram_fraction = clamped;
    }

    // Surface ort's logs (default WARN, ort=DEBUG): EP-registration failures — e.g. WHY
    // the CUDA provider fell back to CPU — are logged via `tracing`.
    let _ = tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env()
                .unwrap_or_else(|_| tracing_subscriber::EnvFilter::new("warn,ort=debug")),
        )
        .with_writer(std::io::stderr)
        .try_init();

    let shard_count = args.shard_count.max(1);
    if args.shard_index >= shard_count {
        anyhow::bail!(
            "--shard-index {} out of range for --shard-count {}",
            args.shard_index,
            shard_count
        );
    }
    if shard_count > 1 {
        eprintln!(
            "RESULT shard index={} count={} device={}",
            args.shard_index, shard_count, args.device
        );
    }

    // Resume ledger: chunk_ids already embedded in --out (+ any --resume-from) are skipped;
    // their content hashes let us copy a not-yet-done duplicate's vector without the GPU.
    let resume = load_done(&args.out, &args.resume_from)
        .with_context(|| format!("scan resume ledger {:?}", args.out))?;
    if !resume.done.is_empty() {
        eprintln!(
            "embedder: resume — {} clause(s) already in {:?} ({} distinct vectors cached)",
            resume.done.len(),
            args.out,
            resume.by_hash.len()
        );
    }

    // Read the work-list, skipping resumed/empty ids and honouring --limit. We keep the
    // metadata (chunk_id, hash) and the text to embed; the hash is computed now from the
    // FULL heading+text (so a later Go re-embed gate is a no-op). A clause whose hash is
    // already in the resume cache (same text embedded under another chunk_id) is diverted
    // to `to_copy` — written by copy, never sent to the GPU.
    let mut chunk_ids: Vec<u64> = Vec::new();
    let mut hashes: Vec<String> = Vec::new();
    let mut texts: Vec<String> = Vec::new();
    let mut to_copy: Vec<(u64, String)> = Vec::new();
    {
        let f =
            File::open(&args.r#in).with_context(|| format!("open work-list {:?}", args.r#in))?;
        for line in BufReader::new(f).lines() {
            let line = line?;
            if line.trim().is_empty() {
                continue;
            }
            let it: WorkItem = serde_json::from_str(&line).context("parse work-list line")?;
            if resume.done.contains(&it.chunk_id) || it.text.trim().is_empty() {
                continue;
            }
            let h = hash::clause_hash(&it.heading, &it.text, &args.embed_identity);
            // Multi-GPU: this process only owns its shard of the work-list. Sharding by the
            // text-derived hash keeps every copy of a clause in the SAME shard, so the dedup
            // is never split across GPUs.
            if shard_of(&h, shard_count) != args.shard_index {
                continue;
            }
            if resume.by_hash.contains_key(&h) {
                to_copy.push((it.chunk_id, h));
                continue;
            }
            chunk_ids.push(it.chunk_id);
            hashes.push(h);
            texts.push(hash::embed_text(&it.heading, &it.text));
            // --limit bounds GPU work; copies are free, so cap on embed items only.
            if args.limit > 0 && chunk_ids.len() >= args.limit {
                break;
            }
        }
    }

    // Open the output ledger up front and write the copied duplicates immediately — this
    // also covers the all-duplicates case (no GPU work but vectors still produced).
    let out = OpenOptions::new()
        .create(true)
        .append(true)
        .open(&args.out)
        .with_context(|| format!("open output {:?}", args.out))?;
    let mut w = BufWriter::new(out);
    if !to_copy.is_empty() {
        for (chunk_id, h) in &to_copy {
            let vec = resume.by_hash.get(h).expect("hash present").clone();
            serde_json::to_writer(
                &mut w,
                &VecRecord {
                    chunk_id: *chunk_id,
                    hash: h.clone(),
                    vec,
                },
            )
            .context("serialize copied vector")?;
            w.write_all(b"\n")?;
        }
        w.flush()?;
        eprintln!(
            "embedder: copied {} duplicate clause(s) from the resume cache (no GPU)",
            to_copy.len()
        );
    }
    if chunk_ids.is_empty() {
        eprintln!("embedder: nothing to embed (work-list empty, fully resumed, or all duplicates)");
        return Ok(());
    }
    let total = chunk_ids.len();
    eprintln!("embedder: {total} clause(s) to embed");

    // Cap the CUDA arena BEFORE the session is built (it is a session option, so it
    // cannot be applied later). The base is TOTAL VRAM, not free: the arena holds the
    // weights too, and the post-load `gpu::detect` below — which sizes batches from free
    // VRAM — would double-count them. The remaining fraction is what the desktop, the
    // CUDA context and any other process on the card get to keep.
    let arena_cap = gpu::detect(args.device).map(|g| {
        let cap = ((g.total_bytes as f64) * args.vram_fraction) as usize;
        eprintln!(
            "RESULT arena_cap bytes={} gib={:.2} of total_gib={:.2} (fraction={})",
            cap,
            cap as f64 / (1024.0 * 1024.0 * 1024.0),
            g.total_bytes as f64 / (1024.0 * 1024.0 * 1024.0),
            args.vram_fraction
        );
        cap
    });

    let bge = Bge::load(
        &args.model_dir.join(&args.onnx),
        &args.model_dir.join("tokenizer.json"),
        args.require_cuda,
        args.device,
        arena_cap,
    )?;

    // WINDOWING (issue #208). A clause the model cannot see whole is embedded in pieces
    // and the pieces are pooled into one vector. Truncation used to drop the tail of a
    // long clause — the body of a big table, the second half of an ASN.1 block, the
    // closing normative paragraphs — so a query that matched only there could not reach
    // the clause at all.
    //
    // WINDOW ONLY WHAT WOULD OTHERWISE BE TRUNCATED. The Go reference windows on a word
    // count alone, which also re-writes clauses that were never truncated: measured here,
    // a 302-word clause that fitted in 1024 tokens came back at cosine 0.81 against its
    // old vector, for no recall gain at all. So the first question is not "is this long?"
    // but "does the model actually lose any of it?" — tokenise the clause whole, and if it
    // fits, that IS the window and the vector does not move. Every vector that changes,
    // changes because content was previously being dropped, which is what makes the
    // re-embed auditable.
    //
    // Clauses that do NOT fit are split twice, and the second split is what makes the
    // guarantee real:
    //
    //   1. By WORDS (`window::window_text`), mirroring internal/embed/window.go.
    //   2. By TOKENS: any window that still reaches MAX_TOKENS is halved and re-tokenised,
    //      repeatedly, until it fits.
    //
    // Step 2 is not defensive padding. Measured on this corpus, 300-word windows — the
    // size the Go reference chose for ~1.3 tokens/word prose — hit the 1024-token cap
    // 10.8% of the time, because 3GPP tables and ASN.1 tokenise at over 3 tokens/word. A
    // word-only port would have kept dropping those tails. Shrinking the word window does
    // not close it either (160 words still truncated 0.5%, and no word count can bound a
    // single space-free ASN.1 blob) while multiplying the windows, and therefore the GPU
    // hours, by 9 instead of 5. Splitting only the offenders cost +11.5% windows and ended
    // at zero.
    //
    // From here down the unit of work is a WINDOW, not a clause: dedup, length-sorting and
    // batching all operate on windows, and only the writer folds them back into clauses.
    let mut win_count: Vec<u32> = Vec::with_capacity(total);
    let mut slots: Vec<Slot> = Vec::with_capacity(total);
    let mut forced_splits = 0usize;
    let mut unsplittable = 0usize;
    let mut windowed_clauses = 0usize;
    {
        const TOK_WINDOW: usize = 8192;
        // Drain `texts` into the window texts rather than holding both: the work-list text
        // is already the largest thing in this process. Every clause starts as ONE window
        // holding its whole text; the pass below keeps that window if the model can see all
        // of it, and only splits the ones it cannot.
        let mut cw: Vec<Vec<String>> = Vec::with_capacity(total);
        for t in texts.drain(..) {
            cw.push(vec![t]);
        }
        drop(texts);
        let mut cids: Vec<Vec<Option<Vec<i64>>>> = vec![vec![None]; cw.len()];

        // Halve a window on a word boundary. Returns None for a single word, the only case
        // where the tail is genuinely unreachable — counted, never hidden.
        fn halve(s: &str) -> Option<(String, String)> {
            let words: Vec<&str> = s.split_whitespace().collect();
            if words.len() < 2 {
                return None;
            }
            let mid = words.len() / 2;
            Some((words[..mid].join(" "), words[mid..].join(" ")))
        }

        // Pass 1: does the whole clause fit? Those that do are finished here, with the
        // ORIGINAL text — the same bytes the pre-windowing embedder handed the tokenizer.
        {
            let idx: Vec<usize> = (0..cw.len()).collect();
            for chunk in idx.chunks(TOK_WINDOW) {
                let batch: Vec<String> = chunk.iter().map(|&c| cw[c][0].clone()).collect();
                let rows = bge.encode(&batch).context("tokenise clauses whole")?;
                for (&c, ids) in chunk.iter().zip(rows) {
                    if ids.len() < crate::model::MAX_TOKENS {
                        cids[c][0] = Some(ids);
                    } else {
                        // The model would truncate this one. Split it by words and let the
                        // convergence loop below take over.
                        cw[c] = window::window_text(&cw[c][0], args.window_words);
                        cids[c] = vec![None; cw[c].len()];
                        windowed_clauses += 1;
                    }
                }
            }
        }

        // Pass 2: tokenise the windows, splitting any that still reach the cap, until none do.
        loop {
            let pending: Vec<(usize, usize)> = cids
                .iter()
                .enumerate()
                .flat_map(|(c, ws)| {
                    ws.iter()
                        .enumerate()
                        .filter(|(_, v)| v.is_none())
                        .map(move |(w, _)| (c, w))
                })
                .collect();
            if pending.is_empty() {
                break;
            }
            let mut to_split: Vec<(usize, usize)> = Vec::new();
            for chunk in pending.chunks(TOK_WINDOW) {
                let batch: Vec<String> = chunk.iter().map(|&(c, w)| cw[c][w].clone()).collect();
                let rows = bge.encode(&batch).context("tokenise window batch")?;
                for (&(c, w), ids) in chunk.iter().zip(rows) {
                    if ids.len() >= crate::model::MAX_TOKENS {
                        if halve(&cw[c][w]).is_some() {
                            to_split.push((c, w));
                            continue;
                        }
                        // One word longer than the model's context. Nothing left to split.
                        unsplittable += 1;
                    }
                    cids[c][w] = Some(ids);
                }
            }
            if to_split.is_empty() {
                break;
            }
            // Descending, so splicing never invalidates an index still to be processed.
            to_split.sort_unstable_by(|a, b| b.cmp(a));
            for (c, w) in to_split {
                let (l, r) = halve(&cw[c][w]).expect("checked splittable");
                cw[c][w] = l;
                cw[c].insert(w + 1, r);
                cids[c][w] = None;
                cids[c].insert(w + 1, None);
                forced_splits += 1;
            }
        }

        for (c, ws) in cids.into_iter().enumerate() {
            win_count.push(ws.len() as u32);
            for (w, ids) in ws.into_iter().enumerate() {
                slots.push(Slot {
                    clause: c as u32,
                    window: w as u32,
                    ids: ids.expect("every window tokenised"),
                });
            }
        }
    }

    let windows_total = slots.len();
    // A window that reaches MAX_TOKENS was still truncated: its tail did not reach
    // the model. Windowing exists to make that number zero, so the run reports it
    // rather than leaving the loss invisible the way plain truncation did.
    let truncated = slots
        .iter()
        .filter(|s| s.ids.len() >= crate::model::MAX_TOKENS)
        .count();
    let multi = win_count.iter().filter(|&&n| n > 1).count();
    eprintln!(
        "RESULT windowing strategy=mean_pool max_words={} clauses={total} windows={windows_total} multi_window_clauses={multi} windowed_clauses={windowed_clauses} truncated_windows={truncated} forced_splits={forced_splits} unsplittable={unsplittable} ratio={:.2}x",
        args.window_words,
        windows_total as f64 / total.max(1) as f64
    );

    // DEDUP identical model inputs. Windows with the same token-id sequence produce the
    // SAME vector, and 3GPP reuses a clause verbatim across many releases, so the work is
    // highly redundant. Group by id sequence, embed each DISTINCT window once, and fan the
    // vector out to every window slot in the group. Pure win, zero quality loss — and
    // windowing makes it BETTER, because two clauses that differ only in their tail now
    // share every window they have in common instead of nothing.
    let mut groups = dedup_by_ids(slots.iter().map(|s| s.ids.as_slice()));
    let distinct = groups.len();
    // Length-bucket the DISTINCT inputs by representative token length so each batch holds
    // similar-length sequences and the dynamic batcher sizes by true sequence length.
    groups.sort_by_key(|g| slots[g[0]].ids.len());
    let lens: Vec<usize> = groups.iter().map(|g| slots[g[0]].ids.len()).collect();
    log_distribution(&lens);
    eprintln!(
        "RESULT dedup windows={windows_total} distinct={distinct} factor={:.2}x (only distinct inputs hit the GPU)",
        windows_total as f64 / distinct.max(1) as f64
    );

    // Size the memory model from the FREE VRAM measured after the model loaded (so the
    // weights are already accounted for). No GPU → fixed CPU-fallback batch. gpu_total is
    // kept for the runtime VRAM probe that grows batches when headroom is left over.
    let mut gpu_total: Option<u64> = None;
    let mut mem = match gpu::detect(args.device) {
        Some(g) => {
            gpu_total = Some(g.total_bytes);
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
                // Allow the runtime probe to grow batches up to ~3.3× the conservative
                // calibration before the OOM backoff (the hard ceiling) kicks in.
                min_k: batch::K_ATTN_DEFAULT * 0.3,
                max_batch: args.max_batch,
                // Past this, the longest sequence is already down to one clause per
                // batch; shrinking further only penalises the short ones.
                max_k: avail / ((crate::model::MAX_TOKENS * crate::model::MAX_TOKENS) as f64),
            }
        }
        None => {
            eprintln!(
                "RESULT gpu=none — CPU fallback, fixed batch={} (set --require-cuda on a GPU box to fail loud)",
                args.batch
            );
            // avail huge ⇒ batch_for_len always saturates to max_batch = --batch (the old
            // fixed-batch behaviour) without risking inf arithmetic. min_k == k_attn so the
            // (never-reached, CPU) growth path is a no-op.
            MemModel {
                avail_bytes: 1.0e18,
                k_attn: batch::K_ATTN_DEFAULT,
                min_k: batch::K_ATTN_DEFAULT,
                max_batch: args.batch.max(1),
                max_k: f64::INFINITY, // CPU path: no CUDA arena to protect
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

    // The output ledger writer `w` was opened up front (it already wrote any copied
    // duplicates); the embed loop appends to it crash-safely.
    const PROGRESS_EVERY: usize = 2000;
    // Re-probe free VRAM every PROBE_EVERY batches: if headroom is left and no OOM hit
    // since the last probe, grow the batches (the calibration is deliberately conservative).
    const PROBE_EVERY: usize = 64;
    let start = Instant::now();
    let mut done_n = 0usize;
    let mut next_log = PROGRESS_EVERY;
    let mut batches_since_probe = 0usize;
    // Window vectors of clauses that are not complete yet. Only MULTI-window
    // clauses ever land here (18.1% of this corpus), and each leaves the moment its
    // last window arrives, so the map holds the in-flight tail, not the corpus.
    let mut pending: HashMap<usize, Pending> = HashMap::new();
    let mut k_at_window_start = mem.k_attn;
    eprintln!("PROGRESS 0/{total} (0%) starting…");

    let mut i = 0usize;
    while i < distinct {
        let end = batch::batch_end(&lens, i, &mem);
        // Embed only the representative (first member) of each distinct group this batch.
        let batch_ids: Vec<&[i64]> = groups[i..end]
            .iter()
            .map(|g| slots[g[0]].ids.as_slice())
            .collect();
        PEAK_BATCH.fetch_max(batch_ids.len(), AtomicOrdering::Relaxed);
        let vecs = run_adaptive(&bge, &mut mem, &batch_ids)
            .with_context(|| format!("embed distinct batch [{i}, {end})"))?;
        if vecs.len() != end - i {
            anyhow::bail!("embed returned {} vecs for {} inputs", vecs.len(), end - i);
        }
        // Fan each distinct vector out to every window slot that shares its token
        // sequence, then fold a clause's windows back into ONE vector.
        for (g, vec) in groups[i..end].iter().zip(vecs) {
            if vec.len() != DENSE_DIM {
                anyhow::bail!("distinct got dim {}, want {}", vec.len(), DENSE_DIM);
            }
            for &m in g {
                let clause = slots[m].clause as usize;
                let wslot = slots[m].window as usize;
                let n = win_count[clause] as usize;
                let pooled: Vec<f32> = if n == 1 {
                    // The common case. One window — the clause fitted whole — and the
                    // vector is returned unchanged. Measured against the corpus it
                    // replaces: cosine 0.999994, worst component delta 3.9e-04. NOT
                    // bit-identical, and cannot be: windowing changes how work is
                    // batched, and a GEMM reduces in a different order at a different
                    // batch shape. That is run-to-run float noise, not a drift.
                    vec.clone()
                } else {
                    {
                        let p = pending.entry(clause).or_insert_with(|| Pending {
                            windows: std::iter::repeat_with(Vec::new).take(n).collect(),
                            filled: 0,
                        });
                        if p.windows[wslot].is_empty() {
                            p.filled += 1;
                        }
                        p.windows[wslot] = vec.clone();
                        if p.filled < win_count[clause] {
                            // More windows of this clause are still in flight. Length-sorting
                            // scatters them across batches, so a clause completes when its
                            // LAST window lands, not when its first does.
                            continue;
                        }
                    }
                    let p = pending.remove(&clause).expect("pending present");
                    match window::mean_pool_l2(&p.windows) {
                        Some(v) => v,
                        None => {
                            anyhow::bail!("clause {clause} pooled to nothing from {n} windows")
                        }
                    }
                };
                if pooled.len() != DENSE_DIM {
                    anyhow::bail!("pooled got dim {}, want {}", pooled.len(), DENSE_DIM);
                }
                let rec = VecRecord {
                    chunk_id: chunk_ids[clause],
                    // Taken, not cloned: a clause is written exactly once, and a long one
                    // would otherwise clone the same String per window.
                    hash: std::mem::take(&mut hashes[clause]),
                    vec: pooled,
                };
                serde_json::to_writer(&mut w, &rec).context("serialize vector")?;
                w.write_all(b"\n")?;
                done_n += 1;
            }
        }
        // Flush per batch: the on-disk ledger is always a valid resume point.
        w.flush()?;
        i = end;

        // Runtime VRAM autotune (GPU only): every PROBE_EVERY batches, if there's free
        // VRAM headroom AND no OOM shrank the model since the last probe, grow the
        // batches. The OOM backoff (which raises k_attn) is the hard ceiling, so growth
        // and shrink form a closed loop that converges on the real card's limit.
        batches_since_probe += 1;
        if batches_since_probe >= PROBE_EVERY {
            batches_since_probe = 0;
            if let (Some(total_b), Some(g)) = (gpu_total, gpu::detect(args.device)) {
                let oomed = mem.k_attn > k_at_window_start + 1e-9;
                let headroom = (g.free_bytes as f64) > (total_b as f64) * 0.25;
                if !oomed && headroom && i < distinct {
                    // 1/1.5 — the exact inverse of the OOM backoff, so ONE clean
                    // window undoes ONE over-correction.
                    //
                    // It was 0.85 against a shrink of 1.5: geometric up, arithmetic
                    // down. Thirteen OOMs multiplied k_attn by ~194x and nothing
                    // brought it back, so the run finished computing long clauses ONE
                    // AT A TIME where the calibration allowed 132. The backoff stays
                    // the hard ceiling; a window with no OOM and free VRAM is evidence
                    // the model over-corrected, and should cost one window to undo,
                    // not thirty.
                    mem.grow(1.0 / 1.5);
                    eprintln!(
                        "PROGRESS autotune grow — free {} MiB, k_attn now {:.0} (seq1024 batch {})",
                        g.free_bytes / (1024 * 1024),
                        mem.k_attn,
                        mem.batch_for_len(1024)
                    );
                }
            }
            k_at_window_start = mem.k_attn;
        }

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
    // Every clause must have been folded back. A non-empty map here means some
    // clause never received all its windows — the vector for it was never written,
    // and the run would otherwise finish "successfully" with a hole in the ledger.
    if !pending.is_empty() {
        let stuck: Vec<String> = pending
            .iter()
            .take(5)
            .map(|(c, p)| format!("clause {c}: {}/{} windows", p.filled, p.windows.len()))
            .collect();
        anyhow::bail!(
            "{} clause(s) never completed their windows, e.g. {}",
            pending.len(),
            stuck.join(", ")
        );
    }
    eprintln!("PROGRESS {total}/{total} (100%) done");
    // Machine-checkable evidence that the memory discipline held, emitted by the
    // process that actually did the work. `nvidia-smi` samples from outside and
    // cannot be asserted on; these three can. A campaign that reports oom_splits>0
    // and still finishes is the backoff doing its job — the state that was
    // unreachable while the arena was uncapped.
    eprintln!(
        "RESULT gpu_evidence arena_cap={} peak_batch={} oom_splits={} final_k_attn={:.0}",
        arena_cap.unwrap_or(0),
        PEAK_BATCH.load(AtomicOrdering::Relaxed),
        OOM_SPLITS.load(AtomicOrdering::Relaxed),
        mem.k_attn
    );
    eprintln!("embedder: wrote {total} vector(s) to {:?}", args.out);
    Ok(())
}

/// run_adaptive embeds one batch, and on a CUDA out-of-memory shrinks the memory model
/// (so every later batch is smaller too) and splits the batch in half, recursively, down
/// to a single clause. A non-OOM error propagates unchanged.
fn run_adaptive<R: AsRef<[i64]>>(
    bge: &Bge,
    mem: &mut MemModel,
    rows: &[R],
) -> Result<Vec<Vec<f32>>> {
    match bge.embed_ids(rows) {
        Ok(v) => Ok(v),
        Err(e) if is_oom(&e) => {
            OOM_SPLITS.fetch_add(1, AtomicOrdering::Relaxed);
            mem.shrink(1.5);
            if rows.len() <= 1 {
                return Err(e.context("CUDA OOM on a single clause (LOWER --vram-fraction for more headroom, or lower MAX_TOKENS)"));
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
///
/// The list is deliberately broad, because a missed pattern is not a missed
/// optimisation: `run_adaptive` aborts the campaign on anything it does not
/// recognise. The BFC-arena entries are the ones that matter once the arena is
/// capped (see `Bge::load`) — exhausting a *capped* arena does NOT produce any of
/// the CUDA driver wordings above. It reads:
///
///   BFCArena::AllocateRawInternal Available memory of 1177295872 is smaller
///   than requested bytes of 1233125376
///
/// which matched nothing here, so capping the arena traded a silent hang for an
/// immediate abort until these two patterns were added.
fn is_oom(e: &anyhow::Error) -> bool {
    let s = format!("{e:?}").to_lowercase();
    s.contains("out of memory")
        || s.contains("cudaerrormemoryallocation")
        || s.contains("cuda_error_out_of_memory")
        || s.contains("failed to allocate")
        || s.contains("bad_alloc")
        || s.contains("is smaller than requested")
        || (s.contains("bfcarena") && s.contains("allocate"))
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

/// dedup_by_ids groups items by their token-id sequence. It returns one group per
/// DISTINCT sequence, each group being the original indices that share it (group[0] is
/// the representative to embed). Identical model inputs ⇒ identical vectors, so only the
/// representatives need the GPU and the rest are filled by copying. The map borrows the
/// id slices from the caller (no key clones).
fn dedup_by_ids<'a>(rows: impl Iterator<Item = &'a [i64]>) -> Vec<Vec<usize>> {
    let mut group_of: std::collections::HashMap<&'a [i64], usize> =
        std::collections::HashMap::new();
    let mut groups: Vec<Vec<usize>> = Vec::new();
    for (idx, row) in rows.enumerate() {
        match group_of.get(row) {
            Some(&d) => groups[d].push(idx),
            None => {
                let d = groups.len();
                group_of.insert(row, d);
                groups.push(vec![idx]);
            }
        }
    }
    groups
}

/// load_done scans the output ledger plus any extra `--resume-from` ledgers (the
/// multi-GPU launcher passes the merged ledger so every per-GPU process skips what any
/// shard already embedded). A missing file is ignored (fresh run).
fn load_done(out: &Path, extra: &[PathBuf]) -> Result<Resume> {
    let mut r = Resume::default();
    scan_ledger(out, &mut r)?;
    for p in extra {
        scan_ledger(p, &mut r)?;
    }
    Ok(r)
}

/// scan_ledger folds one JSONL vector ledger into `r`: chunk_ids (skip already-done) and,
/// the first time a content hash is seen, its vector (copy-fill a duplicate without GPU).
fn scan_ledger(path: &Path, r: &mut Resume) -> Result<()> {
    let f = match File::open(path) {
        Ok(f) => f,
        Err(_) => return Ok(()),
    };
    for line in BufReader::new(f).lines() {
        let line = line?;
        if line.trim().is_empty() {
            continue;
        }
        // Parse leniently so a truncated final line (killed mid-write) never aborts resume.
        if let Ok(rec) = serde_json::from_str::<VecRecord>(&line) {
            r.done.insert(rec.chunk_id);
            if !rec.hash.is_empty() && rec.vec.len() == DENSE_DIM {
                r.by_hash.entry(rec.hash).or_insert(rec.vec);
            }
        } else if let Ok(v) = serde_json::from_str::<serde_json::Value>(&line) {
            if let Some(id) = v.get("chunk_id").and_then(|x| x.as_u64()) {
                r.done.insert(id);
            }
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::{dedup_by_ids, is_oom, load_done, shard_of, DENSE_DIM};
    use std::io::Write;

    #[test]
    fn shard_of_is_deterministic_balanced_and_in_range() {
        // count<=1 is always shard 0 (single-GPU path).
        assert_eq!(shard_of("deadbeefdeadbeef", 1), 0);
        // Deterministic + identical text (same hash) → same shard, so dedup never splits.
        let h = "a1b2c3d4e5f600112233445566778899";
        assert_eq!(shard_of(h, 4), shard_of(h, 4));
        // In range, and a non-hex hash still resolves (FNV fallback).
        for n in [2u64, 3, 8] {
            assert!(shard_of(h, n) < n);
            assert!(shard_of("not-hex-!", n) < n);
        }
        // Spread: 4096 distinct hashes hit every one of 8 shards.
        let mut seen = [false; 8];
        for i in 0..4096u64 {
            seen[shard_of(&format!("{i:016x}"), 8) as usize] = true;
        }
        assert!(seen.iter().all(|&s| s), "all 8 shards should receive work");
    }

    #[test]
    fn load_done_collects_ids_and_caches_full_vectors() {
        // A ledger with: a full-dim vector (→ done + by_hash), a wrong-dim vector
        // (→ done only, never cached), and a chunk_id-only line (→ done only).
        let dir = std::env::temp_dir();
        let path = dir.join(format!("embedder-resume-test-{}.jsonl", std::process::id()));
        {
            let mut f = std::fs::File::create(&path).unwrap();
            let full = vec![0.0f32; DENSE_DIM];
            writeln!(
                f,
                "{}",
                serde_json::json!({"chunk_id": 1, "hash": "h1", "vec": full})
            )
            .unwrap();
            writeln!(
                f,
                "{}",
                serde_json::json!({"chunk_id": 2, "hash": "h2", "vec": [0.5]})
            )
            .unwrap();
            writeln!(f, "{}", serde_json::json!({"chunk_id": 3})).unwrap();
        }
        let r = load_done(&path, &[]).unwrap();
        std::fs::remove_file(&path).ok();

        assert!(r.done.contains(&1) && r.done.contains(&2) && r.done.contains(&3));
        // Only the full-dim vector is cached for copy-dedup.
        assert!(
            r.by_hash.contains_key("h1"),
            "full-dim vector should be cached"
        );
        assert!(
            !r.by_hash.contains_key("h2"),
            "wrong-dim vector must not be cached"
        );
        assert_eq!(r.by_hash.len(), 1);
        assert_eq!(r.by_hash["h1"].len(), DENSE_DIM);
    }

    #[test]
    fn dedup_groups_identical_id_rows_and_covers_all() {
        let a = vec![1i64, 2, 3];
        let b = vec![9i64];
        let rows = [a.clone(), b.clone(), a.clone(), a.clone(), b.clone()];
        let groups = dedup_by_ids(rows.iter().map(|r| r.as_slice()));
        // Two distinct sequences.
        assert_eq!(groups.len(), 2);
        // The [1,2,3] rows are indices 0,2,3; the [9] rows are 1,4.
        let g_a = groups.iter().find(|g| g.contains(&0)).unwrap();
        assert_eq!(g_a, &vec![0, 2, 3]);
        let g_b = groups.iter().find(|g| g.contains(&1)).unwrap();
        assert_eq!(g_b, &vec![1, 4]);
        // Every original index appears exactly once across the groups.
        let mut all: Vec<usize> = groups.iter().flatten().copied().collect();
        all.sort_unstable();
        assert_eq!(all, vec![0, 1, 2, 3, 4]);
    }

    #[test]
    fn dedup_all_distinct_is_identity() {
        let rows = [vec![1i64], vec![2], vec![3]];
        let groups = dedup_by_ids(rows.iter().map(|r| r.as_slice()));
        assert_eq!(groups, vec![vec![0], vec![1], vec![2]]);
    }

    /// The message a CAPPED BFC arena produces when it is exhausted, copied verbatim
    /// from the run that aborted at batch [2048, 2560). It carries none of the CUDA
    /// driver wordings, so before this case existed the cap turned a silent hang into
    /// an immediate abort — the backoff it was supposed to enable never ran.
    #[test]
    fn capped_arena_exhaustion_is_an_oom() {
        let e = anyhow::anyhow!(concat!(
            "Non-zero status code returned while running BiasGelu node. Name:'BiasGelu' ",
            "Status Message: bfc_arena.cc:376 onnxruntime::BFCArena::AllocateRawInternal ",
            "Available memory of 1177295872 is smaller than requested bytes of 1233125376",
        ));
        assert!(
            is_oom(&e),
            "capped-arena exhaustion must trigger the backoff"
        );
    }

    #[test]
    fn the_classic_cuda_wordings_still_match() {
        for m in [
            "CUDA error: out of memory",
            "cudaErrorMemoryAllocation",
            "Failed to allocate memory",
            "std::bad_alloc",
        ] {
            assert!(is_oom(&anyhow::anyhow!("{m}")), "{m} must be an OOM");
        }
    }

    #[test]
    fn an_unrelated_error_is_not_an_oom() {
        // A shape mismatch must still abort loudly; splitting the batch cannot fix it.
        let e = anyhow::anyhow!("Non-zero status code: invalid rank for input: input_ids");
        assert!(!is_oom(&e));
    }
}
