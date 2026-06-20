//! embed-core-sparse — the bulk learned-lexical (sparse) corpus pass for the write-side
//! Rust pipeline. It replaces the deleted Go `cmd/embed --sparse-only` (Phase 11b): read a
//! clause work-list (JSONL, same shape `embed-io --export-sparse-worklist` emits) and emit
//! one per-clause posting line, computed by the SAME proven dual-head sparse arm the serve
//! path uses (`embed_core_embed_sparse`, byte-exact vs FlagEmbedding). The store-rs
//! `embed-io --import-sparse` then writes the postings into `clause_sparse`.
//!
//!   in : {"chunk_id":U64,"heading":S,"text":S}                        (one per line)
//!   out: {"chunk_id":U64,"terms":[[term_id,weight],…]}                (one per line)
//!
//! Resumable: chunk_ids already present in --out are skipped, so a killed/bounded run
//! continues. --limit caps NEW clauses this session. The model is loaded lazily from
//! $EMBED_MODEL_DIR on the first embed (CPU by default; build --features ort,cuda for GPU).
use std::collections::HashSet;
use std::ffi::CString;
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

fn main() {
    let in_path = arg("--in").expect("--in <worklist.jsonl> required");
    let out_path = arg("--out").expect("--out <postings.jsonl> required");
    let limit: usize = arg("--limit").and_then(|s| s.parse().ok()).unwrap_or(0);

    // The dual-head model must carry the sparse head, else this pass cannot run.
    if embed_core::embed_core_has_sparse() != 1 {
        eprintln!("embed-core-sparse: loaded model has NO sparse head (set EMBED_MODEL_DIR to the dual-head export) — nothing to do");
        std::process::exit(1);
    }

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

    let cap = 16384usize;
    let mut ids = vec![0u32; cap];
    let mut wt = vec![0f32; cap];
    let (mut embedded, mut skipped) = (0usize, 0usize);

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
        let text = format!("{}\n{}", item.heading, item.text);
        let c = match CString::new(text.replace('\0', " ")) {
            Ok(c) => c,
            Err(_) => {
                skipped += 1;
                continue;
            }
        };
        let n = embed_core::embed_core_embed_sparse(
            c.as_ptr(),
            ids.as_mut_ptr(),
            wt.as_mut_ptr(),
            cap as i32,
        );
        if n < 0 {
            // Backend error on this clause — leave it for the next pass (don't write it).
            skipped += 1;
            continue;
        }
        let k = (n as usize).min(cap);
        let terms: Vec<(u32, f32)> = (0..k).map(|j| (ids[j], wt[j])).collect();
        if serde_json::to_writer(
            &mut w,
            &Posting {
                chunk_id: item.chunk_id,
                terms,
            },
        )
        .is_err()
        {
            skipped += 1;
            continue;
        }
        let _ = w.write_all(b"\n");
        embedded += 1;
        if embedded % 256 == 0 {
            let _ = w.flush();
            eprintln!("embed-core-sparse: {embedded} embedded …");
        }
        if limit > 0 && embedded >= limit {
            break;
        }
    }
    let _ = w.flush();
    eprintln!("embed-core-sparse: done — embedded={embedded} skipped={skipped}");
}
