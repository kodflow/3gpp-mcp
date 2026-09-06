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
use store_rs::Store;

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

/// DuckDB's memory_limit DEFAULTS TO ~80% OF PHYSICAL RAM, and that default is what
/// killed two builds.
///
/// `import_ledger` materialises the whole ledger as a TEMP TABLE — 2.0 M rows of
/// 1024 floats is ~8 GB of vectors before a single parsing buffer — and DuckDB is
/// entitled to grow until its own cap. Measured 2026-09-06 on a 28 GB machine:
/// embed-io plateaued at 22.3 GB, which is 28 x 0.8 to within a tenth of a gigabyte.
/// DuckDB never felt pressure. The MACHINE did, and the build was killed at exactly
/// this step, twice, for an import that had 368 new vectors to write.
///
/// The cap is not a diet, it is a spill threshold: past it DuckDB writes to
/// temp_directory instead of asking the OS for more. `copy_database_compact` already
/// does this, for the same reason, with the same two knobs and the same kind of env
/// escape hatch — this is the crate's existing answer applied to the one writer that
/// had been left out of it.
///
/// The spill directory sits beside the LEDGER on purpose: that volume already holds
/// tens of gigabytes of vectors, so it has the room, whereas the process's working
/// directory is wherever `make` happened to be invoked from.
fn import_memory_knobs(ledger: &str) -> (String, std::path::PathBuf) {
    let tmp = std::path::Path::new(ledger)
        .parent()
        .filter(|p| !p.as_os_str().is_empty())
        .map(|p| p.join("import-ledger.tmp"))
        .unwrap_or_else(|| std::path::PathBuf::from("import-ledger.tmp"));
    let buf = std::env::var("EMBED_IMPORT_MEMORY_LIMIT")
        .ok()
        .filter(|v| !v.trim().is_empty())
        .unwrap_or_else(|| "12GB".into());
    (buf, tmp)
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
        let wl = store.clauses_needing_embedding(args.limit, floor_ord, &args.embed_identity)?;
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
        // ONE statement for the whole ledger: DuckDB reads the JSONL itself.
        //
        // This used to parse each line with serde, then hand the f32s to
        // set_embeddings_batch, which formatted every one of them back into decimal
        // and glued 1024 per row into generated SQL that DuckDB then re-parsed. Three
        // crossings of the text boundary for numbers that never needed to leave
        // binary; measured at ~70 minutes for 2.2 M vectors. See Store::import_ledger.
        //
        // Malformed lines stay tolerated: `ignore_errors` inside the reader plus a
        // width filter drop them exactly as the loop's `skipped` counter did, so a
        // killed embedder's half-written final line still cannot cost the ledger.
        let (buf, tmp) = import_memory_knobs(inp);
        store
            .raw()
            .execute_batch(&format!(
                "SET temp_directory = '{}';
                 SET memory_limit = '{buf}';",
                tmp.to_string_lossy().replace('\'', "''")
            ))
            .context("bound the ledger import's memory")?;
        eprintln!(
            "embed-io: import capped at {buf}, spilling to {} if it needs more",
            tmp.display()
        );

        let (staged, _) = store.import_ledger(inp)?;
        let total = staged;

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

        // BATCHED, and it is not an optimisation detail. One transaction per clause
        // is 2.2 million transactions and ~110 million individually-parsed INSERT
        // statements for the 3GPP layer — measured at over SEVEN HOURS, during which
        // the step prints nothing at all and looks exactly like a hang.
        //
        // 2 000 clauses per transaction keeps the generated SQL to a few MB while
        // collapsing the per-statement cost; the progress line every batch means the
        // step can no longer be mistaken for a stall.
        const BATCH: usize = 2_000;
        let mut batch: Vec<(u64, Vec<(u32, f32)>)> = Vec::with_capacity(BATCH);

        // The secondary index on term_id is dropped for the load and rebuilt after.
        // DuckDB maintains it row by row, so inserting ~265 million postings into a
        // growing ART index makes the import slower the further it goes — the shape
        // seen on the real run, brisk for hours then CPU-bound with the file barely
        // moving. Rebuilding afterwards is NOT optional: SearchSparse scores by
        // term_id, and leaving it off turns every sparse query into a full scan of a
        // 265-million-row table, with nothing to say so.
        store.drop_sparse_term_index()?;

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
            // worklist converges — the DELETE is issued for it either way.
            batch.push((r.chunk_id, r.terms));
            if batch.len() >= BATCH {
                store.set_sparse_many(&batch)?;
                total += batch.len();
                batch.clear();
                eprintln!("embed-io: sparse import {total} clause(s)");
            }
        }
        if !batch.is_empty() {
            store.set_sparse_many(&batch)?;
            total += batch.len();
        }
        eprintln!("embed-io: rebuilding the term_id index over {total} clause(s)…");
        store.create_sparse_term_index()?;
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
        let null_at_floor = store.clauses_needing_embedding(0, floor_ord, "")?.len();
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

#[cfg(test)]
mod tests {
    use super::*;

    /// The spill file must land beside the ledger — that is the volume with room for
    /// it. A relative ledger path with no parent must still yield a usable path
    /// rather than an empty one.
    #[test]
    fn the_spill_directory_sits_beside_the_ledger() {
        let (_, tmp) = import_memory_knobs("/c/vecs/etsi-ledger.jsonl");
        assert_eq!(
            tmp,
            std::path::Path::new("/c/vecs").join("import-ledger.tmp")
        );

        let (_, bare) = import_memory_knobs("etsi-ledger.jsonl");
        assert_eq!(bare, std::path::PathBuf::from("import-ledger.tmp"));
        assert!(!bare.as_os_str().is_empty());
    }

    /// THE ONLY TEST THAT TOUCHES THE ENVIRONMENT, and it has to stay that way.
    ///
    /// `std::env` is process-wide and cargo runs tests in THREADS, so a test that sets
    /// this variable sets it for every other test running at that instant. Split across
    /// two tests, this suite passed alone and failed under `cargo test` — measured, not
    /// theorised. Go's `t.Setenv` refuses to coexist with `t.Parallel` for exactly this
    /// reason; Rust offers no equivalent, so the discipline is manual: one test.
    ///
    /// It covers three things at once because they cannot safely be separated:
    ///   - the default is FIXED at 12GB, never derived from physical RAM — a
    ///     percentage-of-RAM default is precisely the bug this whole change removes;
    ///   - an explicit override wins;
    ///   - a BLANK override falls back rather than reaching DuckDB, where
    ///     `SET memory_limit = ''` is a parse error that would kill the import over a
    ///     stray variable.
    #[test]
    fn the_cap_is_fixed_by_default_and_overridable_but_never_blank() {
        let key = "EMBED_IMPORT_MEMORY_LIMIT";
        let restore = std::env::var(key).ok();

        std::env::remove_var(key);
        assert_eq!(import_memory_knobs("/tmp/l.jsonl").0, "12GB", "default");

        std::env::set_var(key, "4GB");
        assert_eq!(
            import_memory_knobs("/tmp/l.jsonl").0,
            "4GB",
            "explicit override"
        );

        std::env::set_var(key, "   ");
        assert_eq!(
            import_memory_knobs("/tmp/l.jsonl").0,
            "12GB",
            "a blank override must fall back, not reach DuckDB"
        );

        match restore {
            Some(v) => std::env::set_var(key, v),
            None => std::env::remove_var(key),
        }
    }

    /// The knobs are worthless if DuckDB refuses them, so open a real corpus and read
    /// the setting BACK — a renamed option fails here rather than at 22 GB on a build
    /// machine.
    ///
    /// The cap is written literally rather than taken from `import_memory_knobs`, so
    /// this test never reads the environment and cannot race the one above.
    #[test]
    fn duckdb_accepts_the_knobs_and_reports_them_back() {
        let dir = std::env::temp_dir().join(format!("embedio-knobs-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let db = dir.join("t.duckdb");
        let _ = std::fs::remove_file(&db);

        let store = Store::open_rw(db.to_str().unwrap()).unwrap();
        let tmp = dir.join("import-ledger.tmp");
        store
            .raw()
            .execute_batch(&format!(
                "SET temp_directory = '{}'; SET memory_limit = '3GB';",
                tmp.to_string_lossy().replace('\'', "''")
            ))
            .expect("DuckDB rejected the knobs");

        let got: String = store
            .raw()
            .query_row("SELECT current_setting('memory_limit')", [], |r| r.get(0))
            .unwrap();
        // DuckDB normalises the unit ("3GB" -> "2.7 GiB"), so assert the cap MOVED and
        // stayed modest rather than matching a formatted string.
        assert!(
            got.starts_with('2') || got.starts_with('3'),
            "memory_limit did not take: {got} (the default would be ~80% of RAM)"
        );

        drop(store);
        let _ = std::fs::remove_dir_all(&dir);
    }
}
