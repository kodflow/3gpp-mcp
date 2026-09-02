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
use std::collections::HashMap;
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
    /// The hash of the TEXT these postings were computed from. See resume_hash.
    h: String,
    terms: Vec<(u32, f32)>,
}

#[derive(Deserialize)]
struct DoneId {
    chunk_id: u64,
    /// Absent on ledgers written before this field existed. An empty hash can never
    /// satisfy the resume test, so such a line is recomputed rather than trusted:
    /// that costs GPU, never correctness.
    #[serde(default)]
    h: String,
}

/// resume_hash identifies the TEXT a posting line was computed from.
///
/// A chunk_id is a POSITION, not an identity: ingest assigns it sequentially
/// (offset = max_chunk_id), so a corpus rebuilt from scratch reuses the same numbers
/// for different clauses. Measured 2026-09-02 between two ETSI builds of the SAME
/// 11 821 documents: chunk_id 138 was "ETSI TS 101 671 v3.15.1 clause 10" in one and
/// "ETSI EN 300 113-1 v1.3.1 clause 4" in the other.
///
/// This ledger carried the id and nothing else, and both the resume here and
/// `embed-io --import-sparse` key on it — so a ledger from another build gives each
/// clause the learned-lexical postings of an unrelated one. Nothing downstream
/// compares a posting to the text it came from: the sparse arm would simply retrieve
/// the wrong clauses, confidently.
fn resume_hash(embed_text: &str) -> String {
    use sha2::{Digest, Sha256};
    let mut h = Sha256::new();
    h.update(embed_text.as_bytes());
    format!("{:x}", h.finalize())[..16].to_string()
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
                        h: resume_hash(&txt[k]),
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

/// flush_window sorts a read-ahead window by text length, then emits it in
/// `batch`-sized groups.
///
/// WHY SORT AT ALL. A batch is padded to its LONGEST member, so a batch drawn in
/// file order — where a 40-character heading sits next to a 12 000-character
/// ASN.1 clause — spends most of its forward pass on padding. Measured on the
/// 2 207 218-clause corpus: the card sat at 88% utilisation but only 5.2 GB of
/// 19 GB, and throughput barely moved between --batch 64 and --batch 256, which
/// is what "the batch is full of padding" looks like from the outside.
///
/// Sorting a WINDOW rather than the whole file keeps memory bounded (the corpus
/// work-list is 3 GB) while making each batch nearly homogeneous, which is what
/// the dense embedder achieves by length-bucketing its windows up front.
///
/// Output order changes, and that is safe: every posting line carries its own
/// chunk_id, and resume is keyed on chunk_id, not on position.
#[allow(clippy::too_many_arguments)]
fn flush_window(
    ids: &mut Vec<u64>,
    txt: &mut Vec<String>,
    batch: usize,
    w: &mut BufWriter<std::fs::File>,
    embedded: &mut usize,
    skipped: &mut usize,
    total: usize,
    start: std::time::Instant,
) {
    if ids.is_empty() {
        return;
    }
    let mut order: Vec<usize> = (0..ids.len()).collect();
    order.sort_unstable_by_key(|&i| txt[i].len());

    let mut bi: Vec<u64> = Vec::with_capacity(batch);
    let mut bt: Vec<String> = Vec::with_capacity(batch);
    for &i in &order {
        bi.push(ids[i]);
        bt.push(std::mem::take(&mut txt[i]));
        if bi.len() >= batch {
            flush_batch(&mut bi, &mut bt, w, embedded, skipped, total, start);
        }
    }
    flush_batch(&mut bi, &mut bt, w, embedded, skipped, total, start);
    ids.clear();
    txt.clear();
}

fn main() {
    let in_path = arg("--in").expect("--in <worklist.jsonl> required");
    let out_path = arg("--out").expect("--out <postings.jsonl> required");
    let limit: usize = arg("--limit").and_then(|s| s.parse().ok()).unwrap_or(0);
    let batch: usize = arg("--batch").and_then(|s| s.parse().ok()).unwrap_or(64);
    // Read-ahead window for length-bucketing: filled, sorted by length, then emitted
    // in `batch`-sized groups. 200 batches deep is tens of MB of text at corpus clause
    // sizes — bounded, and deep enough that a batch is near-homogeneous in length.
    let window: usize = arg("--window")
        .and_then(|s| s.parse().ok())
        .unwrap_or(batch.saturating_mul(200).max(batch));

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
    let mut done: HashMap<u64, String> = HashMap::new();
    if let Ok(f) = std::fs::File::open(&out_path) {
        for line in BufReader::new(f).lines().map_while(Result::ok) {
            if line.trim().is_empty() {
                continue;
            }
            if let Ok(d) = serde_json::from_str::<DoneId>(&line) {
                done.insert(d.chunk_id, d.h);
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
        // Match the dense EmbedText join exactly (embedder hash::embed_text).
        let embed_text = format!("{}\n{}", item.heading, item.text).replace('\0', " ");
        // Resume on the id AND the text it names: see resume_hash.
        let want = resume_hash(&embed_text);
        if matches!(done.get(&item.chunk_id), Some(h) if !h.is_empty() && *h == want) {
            continue;
        }
        buf_ids.push(item.chunk_id);
        buf_txt.push(embed_text);
        if buf_ids.len() >= window {
            let before = embedded;
            flush_window(
                &mut buf_ids,
                &mut buf_txt,
                batch,
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
    flush_window(
        &mut buf_ids,
        &mut buf_txt,
        batch,
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

#[cfg(test)]
mod tests {
    use super::*;

    /// CROSS-RUNTIME GOLDEN. goal's sparse step re-derives this hash in Go to decide
    /// whether a postings file was written against another build of the corpus; if the
    /// two implementations drift, the check either archives a good file every run or
    /// stops noticing a bad one. Both sides are pinned to this constant.
    #[test]
    fn resume_hash_matches_the_go_side() {
        assert_eq!(resume_hash("6 X1\nbody"), "b76cec67248a1ec9");
    }

    /// It identifies the TEXT, so a different clause hashes differently and the same
    /// clause hashes the same however many times it is asked.
    #[test]
    fn resume_hash_is_stable_and_text_derived() {
        let a = resume_hash("h\nt");
        assert_eq!(a, resume_hash("h\nt"));
        assert_ne!(a, resume_hash("h\nt2"));
        assert_eq!(a.len(), 16);
    }
}
