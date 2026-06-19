//! discover — CI-matrix discovery tool (Phase 9 Rust port of cmd/discover).
//!
//! Reads the 3GPP status report from --status-file (or stdin), compares site
//! versions against the published indexes PER (spec, release), and prints the JSON
//! array of 2-digit series that changed (the GitHub Actions matrix). Alternate
//! modes: --emit-worklist, --emit-draft-ledger, --list-drift, --sparse-check.
//!
//! Unlike the Go tool it does NOT fetch the status report itself (no HTTP client
//! dep): the driver curls it once and passes the file. The build-identity-drift
//! and subject-delta signals are NOT YET ported (they need the embed/model/
//! subjectmeta/enrichmeta identities Rust-side); until then cmd/discover stays the
//! production matrix tool and this binary is additive.

use std::cmp::Ordering;
use std::collections::BTreeMap;
use std::io::Read;
use std::process::exit;

use discover3gpp::*;

struct Args {
    status_file: String,
    index: String,
    absent_index: String,
    floor: String,
    all: bool,
    series: String,
    include_legacy_gsm: bool,
    mode: Mode,
    sparse_id: String,
    sparse_index: String,
}

enum Mode {
    Delta,
    Worklist,
    DraftLedger,
    ListDrift,
    SparseCheck,
}

fn parse_args() -> Args {
    let mut a = Args {
        status_file: String::new(),
        index: String::new(),
        absent_index: String::new(),
        floor: "Rel-99".into(),
        all: false,
        series: String::new(),
        include_legacy_gsm: false,
        mode: Mode::Delta,
        sparse_id: String::new(),
        sparse_index: String::new(),
    };
    let argv: Vec<String> = std::env::args().skip(1).collect();
    let mut i = 0;
    while i < argv.len() {
        let arg = argv[i].as_str();
        let mut next = || {
            i += 1;
            argv.get(i).cloned().unwrap_or_default()
        };
        match arg {
            "--status-file" => a.status_file = next(),
            "--index" => a.index = next(),
            "--absent-index" => a.absent_index = next(),
            "--floor" => a.floor = next(),
            "--series" => a.series = next(),
            "--sparse-id" => a.sparse_id = next(),
            "--sparse-index" => a.sparse_index = next(),
            "--all" => a.all = true,
            "--include-legacy-gsm" => a.include_legacy_gsm = true,
            "--emit-worklist" => a.mode = Mode::Worklist,
            "--emit-draft-ledger" => a.mode = Mode::DraftLedger,
            "--list-drift" => a.mode = Mode::ListDrift,
            "--sparse-check" => a.mode = Mode::SparseCheck,
            other => {
                eprintln!("discover: unknown flag {other}");
                exit(2);
            }
        }
        i += 1;
    }
    a
}

fn read_status(path: &str) -> String {
    if path.is_empty() {
        let mut s = String::new();
        if std::io::stdin().read_to_string(&mut s).is_err() {
            eprintln!("discover: failed to read status report from stdin");
            exit(1);
        }
        s
    } else {
        std::fs::read_to_string(path).unwrap_or_else(|e| {
            eprintln!("discover: read {path}: {e}");
            exit(1);
        })
    }
}

fn main() {
    let a = parse_args();
    let floor_major = major(&a.floor);

    // --sparse-check is independent of the status report (a self-heal signal).
    if let Mode::SparseCheck = a.mode {
        let published = load_sparse_model_id(&a.sparse_index);
        let needed = sparse_needed(&a.sparse_id, &published);
        println!("sparse_needed={needed}");
        println!("sparse_id={}", a.sparse_id);
        eprintln!(
            "sparse-check: expected={:?} published={:?} -> needed={needed}",
            a.sparse_id, published
        );
        return;
    }

    let html = read_status(&a.status_file);
    let site = parse_status(&html).unwrap_or_else(|e| {
        eprintln!("discover: {e}");
        exit(1);
    });

    match a.mode {
        Mode::Worklist => {
            let (lines, n, skipped) = emit_worklist(&site, floor_major, &a.series);
            print!("{lines}");
            eprintln!("emit-worklist: {n} entries ({skipped} un-encodable versions skipped)");
        }
        Mode::DraftLedger => {
            println!("{}", emit_draft_ledger(&site, floor_major, &a.series));
        }
        Mode::ListDrift => {
            let have = build_have(&a);
            let (rows, missing, stale) = dump_drift(&site, &have, floor_major);
            print!("{rows}");
            eprintln!(
                "drift: total={} missing={missing} stale={stale}",
                missing + stale
            );
        }
        Mode::Delta => {
            let idx = load_index(&a.index);
            let have = build_have(&a);
            let full = a.all || idx.is_empty();
            let mut series = delta_series(&site, &have, floor_major, full);

            // Legacy GSM opt-in: add a legacy series ONLY when absent from the index
            // (so it converges; an unconditional add re-selects it forever — #129).
            if a.include_legacy_gsm {
                for s in LEGACY_GSM_SERIES {
                    if !series_in_index(&idx, s) {
                        series.insert(s.to_string());
                    }
                }
            }

            let out: Vec<String> = series.into_iter().collect();
            let mode = if full { "full" } else { "delta" };
            eprintln!(
                "discover: mode={mode} site_keys={} indexed={} -> {} series: {:?} \
                 (subject-delta + build-identity-drift NOT ported — Go cmd/discover owns those)",
                site.len(),
                idx.len(),
                out.len(),
                out
            );
            println!("{}", serde_json::to_string(&out).unwrap());
        }
        Mode::SparseCheck => unreachable!(),
    }
}

/// build_have merges the index with the absent-ledger (highest version wins),
/// matching Go's `have` so genuinely-absent specs stop re-flagging.
fn build_have(a: &Args) -> BTreeMap<String, String> {
    let mut have = load_index(&a.index);
    for (k, v) in load_merged_index(&a.absent_index) {
        let cur = have.get(&k).map(String::as_str).unwrap_or("");
        if cmp_ver(&v, cur) == Ordering::Greater {
            have.insert(k, v);
        }
    }
    have
}
