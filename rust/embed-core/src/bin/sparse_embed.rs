//! embed-core-sparse — the bulk learned-lexical (sparse) corpus pass for the write-side
//! Rust pipeline. It replaces the deleted Go `cmd/embed --sparse-only` (Phase 11b): read a
//! clause work-list (JSONL, same shape `embed-io --export-sparse-worklist` emits) and emit
//! one per-clause posting line, computed by the SAME proven dual-head sparse arm the serve
//! path uses (`embed_core::embed_sparse_batch`). The store-rs `embed-io --import-sparse`
//! then writes the postings into `clause_sparse`.
//!
//!   in : {"chunk_id":U64,"heading":S,"text":S}                        (one per line)
//!   out: {"chunk_id":U64,"terms":[[term_id,weight],…]}                (one per line)
//!
//! BATCHED: clauses are embedded `--batch` at a time (one ONNX forward per batch) — per-clause
//! is far too slow for the corpus campaign. Resumable: chunk_ids already present in --out are
//! skipped, so a killed/bounded run continues. --limit caps NEW clauses this session. The model
//! is loaded lazily from $EMBED_MODEL_DIR on the first embed (build --features ort,cuda for GPU).
use std::collections::HashSet;
use std::io::{BufRead, BufReader, BufWriter, Write};

use serde::{Deserialize, Serialize};

#[derive(Deserialize)]
struct WorkItem {
    chunk_id: u64,
    #[serde(default)]
    heading: String,
    #[serde(default)]
    text: String,
}

#[derive(Serialize)]
struct Posting {
    chunk_id: u64,
    terms: Vec<(u32, f32)>,
}

#[derive(Deserialize)]
struct DoneId {
    chunk_id: u64,
}

/// arg returns the value following `--name` on the command line, if present.
fn arg(name: &str) -> Option<String> {
    let a: Vec<String> = std::env::args().collect();
    a.iter()
        .position(|x| x == name)
        .and_then(|i| a.get(i + 1).cloned())
}

/// flush_batch embeds the buffered clauses in ONE forward pass and writes a posting line each.
#[allow(clippy::too_many_arguments)]
fn flush_batch(
    ids: &mut Vec<u64>,
    txt: &mut Vec<String>,
    w: &mut BufWriter<std::fs::File>,
    embedded: &mut usize,
    skipped: &mut usize,
    total: usize,
    start: std::time::Instant,
) {
    if ids.is_empty() {
        return;
    }
    let refs: Vec<&str> = txt.iter().map(|s| s.as_str()).collect();
    let res = embed_core::embed_sparse_batch(&refs, refs.len());
    for (k, postings) in res.into_iter().enumerate() {
        match postings {
            Some(terms) => {
                if serde_json::to_writer(
                    &mut *w,
                    &Posting {
                        chunk_id: ids[k],
                        terms,
                    },
                )
                .is_ok()
                {
                    let _ = w.write_all(b"\n");
                    *embedded += 1;
                } else {
                    *skipped += 1;
                }
            }
            None => *skipped += 1, // backend error on this row — left for the next pass
        }
    }
    let _ = w.flush();
    // Progress bar: count, %, throughput, ETA — so a multi-hour run is legible (and a GPU vs
    // CPU regression is obvious from cl/s).
    let el = start.elapsed().as_secs_f64();
    let rate = if el > 0.0 { *embedded as f64 / el } else { 0.0 };
    let pct = if total > 0 {
        100.0 * *embedded as f64 / total as f64
    } else {
        0.0
    };
    let eta = if rate > 0.0 {
        ((total.saturating_sub(*embedded)) as f64 / rate) as u64
    } else {
        0
    };
    eprintln!(
        "embed-core-sparse: {}/{} ({:.1}%) {:.1} cl/s ETA {}h{:02}m (skipped {})",
        *embedded,
        total,
        pct,
        rate,
        eta / 3600,
        (eta % 3600) / 60,
        *skipped
    );
    ids.clear();
    txt.clear();
}

fn main() {
    let in_path = arg("--in").expect("--in <worklist.jsonl> required");
    let out_path = arg("--out").expect("--out <postings.jsonl> required");
    let limit: usize = arg("--limit").and_then(|s| s.parse().ok()).unwrap_or(0);
    let batch: usize = arg("--batch").and_then(|s| s.parse().ok()).unwrap_or(64);

    // The dual-head model must carry the sparse head, else this pass cannot run.
    // has_sparse() lazy-loads the ONNX model; log around it so a slow/hung model load
    // (e.g. a >2GB model exported without external data) is visible within seconds.
    eprintln!(
        "embed-core-sparse: loading model (EMBED_MODEL_DIR={:?})…",
        std::env::var("EMBED_MODEL_DIR").unwrap_or_default()
    );
    if embed_core::embed_core_has_sparse() != 1 {
        eprintln!("embed-core-sparse: loaded model has NO sparse head (set EMBED_MODEL_DIR to the dual-head export) — nothing to do");
        std::process::exit(1);
    }
    eprintln!(
        "embed-core-sparse: model loaded ✓ (sparse head present) — starting embed (batch={batch})"
    );

    // Resume: skip chunk_ids already written to --out by a prior (bounded/killed) run.
    let mut done: HashSet<u64> = HashSet::new();
    if let Ok(f) = std::fs::File::open(&out_path) {
        for line in BufReader::new(f).lines().map_while(Result::ok) {
            if line.trim().is_empty() {
                continue;
            }
            if let Ok(d) = serde_json::from_str::<DoneId>(&line) {
                done.insert(d.chunk_id);
            }
        }
    }
    eprintln!(
        "embed-core-sparse: {} clause(s) already done in {out_path}",
        done.len()
    );

    let inf = std::fs::File::open(&in_path).expect("open --in");
    let outf = std::fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(&out_path)
        .expect("open --out (append)");
    let mut w = BufWriter::new(outf);

    // Pre-count the worklist for the progress bar's denominator (cheap single scan).
    let total = std::fs::File::open(&in_path)
        .map(|f| {
            BufReader::new(f)
                .lines()
                .map_while(Result::ok)
                .filter(|l| !l.trim().is_empty())
                .count()
        })
        .unwrap_or(0);
    let start = std::time::Instant::now();

    let (mut embedded, mut skipped) = (0usize, 0usize);
    let mut fail_streak = 0usize; // consecutive batches that embedded nothing (broken-model guard)
    let mut buf_ids: Vec<u64> = Vec::with_capacity(batch);
    let mut buf_txt: Vec<String> = Vec::with_capacity(batch);

    for line in BufReader::new(inf).lines().map_while(Result::ok) {
        if line.trim().is_empty() {
            continue;
        }
        let item: WorkItem = match serde_json::from_str(&line) {
            Ok(i) => i,
            Err(_) => {
                skipped += 1;
                continue;
            }
        };
        if done.contains(&item.chunk_id) {
            continue;
        }
        // Match the dense EmbedText join exactly (embedder hash::embed_text).
        buf_ids.push(item.chunk_id);
        buf_txt.push(format!("{}\n{}", item.heading, item.text).replace('\0', " "));
        if buf_ids.len() >= batch {
            let before = embedded;
            flush_batch(
                &mut buf_ids,
                &mut buf_txt,
                &mut w,
                &mut embedded,
                &mut skipped,
                total,
                start,
            );
            // Fast-abort guard: if many consecutive batches embed NOTHING while the total is
            // still 0, the model/inference is broken (e.g. a batch-static ONNX export → every
            // batch fails with a MatMul dim mismatch). Bail out in seconds instead of burning
            // hours of GPU producing 0 (the failure is already streamed above).
            if embedded == before {
                fail_streak += 1;
            } else {
                fail_streak = 0;
            }
            if embedded == 0 && fail_streak >= 8 {
                eprintln!("embed-core-sparse: ABORT — {fail_streak} consecutive batches embedded 0 (model/inference broken); stopping early");
                std::process::exit(2);
            }
            if limit > 0 && embedded >= limit {
                break;
            }
        }
    }
    flush_batch(
        &mut buf_ids,
        &mut buf_txt,
        &mut w,
        &mut embedded,
        &mut skipped,
        total,
        start,
    );
    eprintln!(
        "embed-core-sparse: done — embedded={embedded}/{total} skipped={skipped} in {:.0}s",
        start.elapsed().as_secs_f64()
    );
}
