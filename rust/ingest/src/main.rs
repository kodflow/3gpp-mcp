//! ingest (Rust) — Phase 5 of the write-side migration. Parses one converted spec with
//! parse3gpp and writes it to DuckDB through store-rs, closing the loop: Rust parses +
//! writes the corpus, Go serves it read-only. This is the Rust replacement for the parse
//! half of Go's cmd/ingest (the embed pass — Rust embedder + embed-io — already runs after).
//!
//! Usage: ingest --html <…/convert/<Rel>/<num>-<code>.html> --db <out.duckdb>
use anyhow::{Context, Result};
use clap::Parser;
use parse3gpp::{parse_filename_meta, parse_html_clauses};
use store_rs::{ClauseIn, Store};

/// The ingest pipeline identity stamped into the resume ledger; a change here (new parser
/// / chunker) invalidates the log and forces a rebuild. Mirrors model.SpecIngestParts.
const PIPELINE_VERSION: &str = "html-v2|clause-leaf-v1|1";

#[derive(Parser)]
#[command(
    name = "ingest",
    about = "Parse a converted 3GPP spec and write it to DuckDB (parse3gpp + store-rs)"
)]
struct Args {
    /// Converted HTML spec at …/convert/<Rel>/<num>-<code>.html.
    #[arg(long)]
    html: String,
    /// Output DuckDB shard (created/opened read-write).
    #[arg(long)]
    db: String,
}

fn main() -> Result<()> {
    let args = Args::parse();

    let meta = parse_filename_meta(&args.html).map_err(|e| anyhow::anyhow!(e))?;
    let html =
        std::fs::read_to_string(&args.html).with_context(|| format!("read {}", args.html))?;
    let (clauses, saw_change_history, degraded) =
        parse_html_clauses(&html, &meta.spec_id, &meta.release, &meta.version);

    let store = Store::open_rw(&args.db)?;
    store.log_ingest(&meta.spec_id, &meta.version, "started", PIPELINE_VERSION)?;

    // title is refined from the HTML later (deriveTitleAndType); empty is a safe default
    // the catalogue overlay / a later pass fills.
    store.upsert_spec(
        &meta.spec_id,
        &meta.series,
        "",
        &meta.doc_type,
        &meta.working_group,
    )?;
    store.upsert_version(&meta.spec_id, &meta.release, &meta.version, &meta.docx_url)?;

    let rows: Vec<ClauseIn> = clauses
        .iter()
        .map(|c| ClauseIn {
            chunk_id: c.chunk_id,
            spec_id: meta.spec_id.clone(),
            release: meta.release.clone(),
            version: meta.version.clone(),
            clause_path: c.clause_path.clone(),
            heading: c.heading.clone(),
            text: c.text.clone(),
            is_normative: c.is_normative,
        })
        .collect();
    store.insert_clauses(&rows)?;

    store.log_ingest(&meta.spec_id, &meta.version, "done", PIPELINE_VERSION)?;
    store.checkpoint()?;

    eprintln!(
        "ingest: {} {} {} → {} clause(s) (change_history={saw_change_history} degraded={degraded})",
        meta.spec_id,
        meta.release,
        meta.version,
        rows.len()
    );
    Ok(())
}
