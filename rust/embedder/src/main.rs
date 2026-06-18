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

/// Resume is what a prior (partial) run left in the output ledger: the chunk_ids already
/// embedded (skip them) and a content-hash → vector map (fill a not-yet-done clause whose
/// text was already embedded under another chunk_id, instead of re-embedding it).
#[derive(Default)]
struct Resume {
    done: HashSet<u64>,
    by_hash: std::collections::HashMap<String, Vec<f32>>,
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

    let bge = Bge::load(
        &args.model_dir.join(&args.onnx),
        &args.model_dir.join("tokenizer.json"),
        args.require_cuda,
        args.device,
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

    // DEDUP identical model inputs. Clauses with the same (truncated) token-id sequence
    // produce the SAME vector, and 3GPP reuses a clause verbatim across many releases, so
    // the work-list is highly redundant. Group by id sequence, embed each DISTINCT input
    // once, and fan the vector out to every chunk_id in the group. Pure win, zero quality
    // loss — typically the dominant lever on a multi-release corpus.
    let mut groups = dedup_by_ids(prepared.iter().map(|p| p.ids.as_slice()));
    let distinct = groups.len();
    // Length-bucket the DISTINCT inputs by representative token length so each batch holds
    // similar-length sequences and the dynamic batcher sizes by true sequence length.
    groups.sort_by_key(|g| prepared[g[0]].ids.len());
    let lens: Vec<usize> = groups.iter().map(|g| prepared[g[0]].ids.len()).collect();
    log_distribution(&lens);
    eprintln!(
        "RESULT dedup clauses={total} distinct={distinct} factor={:.2}x (only distinct inputs hit the GPU)",
        total as f64 / distinct.max(1) as f64
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
    let mut k_at_window_start = mem.k_attn;
    eprintln!("PROGRESS 0/{total} (0%) starting…");

    let mut i = 0usize;
    while i < distinct {
        let end = batch::batch_end(&lens, i, &mem);
        // Embed only the representative (first member) of each distinct group this batch.
        let batch_ids: Vec<&[i64]> = groups[i..end]
            .iter()
            .map(|g| prepared[g[0]].ids.as_slice())
            .collect();
        let vecs = run_adaptive(&bge, &mut mem, &batch_ids)
            .with_context(|| format!("embed distinct batch [{i}, {end})"))?;
        if vecs.len() != end - i {
            anyhow::bail!("embed returned {} vecs for {} inputs", vecs.len(), end - i);
        }
        // Fan each distinct vector out to every chunk_id that shares its token sequence.
        for (g, vec) in groups[i..end].iter().zip(vecs) {
            if vec.len() != DENSE_DIM {
                anyhow::bail!("distinct got dim {}, want {}", vec.len(), DENSE_DIM);
            }
            for &m in g {
                let p = &prepared[m];
                let rec = VecRecord {
                    chunk_id: p.chunk_id,
                    hash: p.hash.clone(),
                    vec: vec.clone(),
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
                    mem.grow(0.85);
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
    eprintln!("PROGRESS {total}/{total} (100%) done");
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
    use super::{dedup_by_ids, load_done, shard_of, DENSE_DIM};
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
}
