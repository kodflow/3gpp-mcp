//! ingest-catalog (Rust) — Phase 7. The additive DynaReport metadata overlay: parse the
//! public status-report.htm (per-spec title / TS-or-TR / working group + versions) and
//! Releases.aspx (release calendar + freeze_date) and overlay them onto specs/spec_versions
//! rows content ingest already created. Idempotent, cite-or-silent (never invents
//! catalogue-only specs — UpdateSpecMeta only updates rows already on disk).
//!
//! Usage: ingest-catalog --db <db> [--status-report <file>] [--releases <file>]
use anyhow::{Context, Result};
use clap::Parser;
use parse3gpp::catalog::{parse_releases, parse_status_report};
use store_rs::{ReleaseRow, Store};

#[derive(Parser)]
#[command(
    name = "ingest-catalog",
    about = "Overlay DynaReport metadata (title/WG/freeze_date) onto an existing DuckDB"
)]
struct Args {
    #[arg(long)]
    db: String,
    /// dynareport status-report.htm (per-spec title / type / WG).
    #[arg(long)]
    status_report: Option<String>,
    /// portal Releases.aspx (release calendar + freeze_date).
    #[arg(long)]
    releases: Option<String>,
}

fn main() -> Result<()> {
    let args = Args::parse();
    let store = Store::open_rw(&args.db)?;

    let mut specs_updated = 0usize;
    if let Some(path) = args.status_report.as_deref() {
        let data = std::fs::read_to_string(path).with_context(|| format!("read {path}"))?;
        let (specs, _vers) = parse_status_report(&data);
        for s in &specs {
            if store.update_spec_meta(&s.spec_id, &s.title, &s.doc_type, &s.working_group)? {
                specs_updated += 1;
            }
        }
    }

    let mut releases_n = 0usize;
    if let Some(path) = args.releases.as_deref() {
        let data = std::fs::read_to_string(path).with_context(|| format!("read {path}"))?;
        let rels = parse_releases(&data);
        let rows: Vec<ReleaseRow> = rels
            .iter()
            .map(|r| ReleaseRow {
                code: r.code.clone(),
                name: r.name.clone(),
                status: r.status.clone(),
                start_date: r.start_date.clone(),
                freeze_date: r.freeze_date.clone(),
                freeze_meeting: r.freeze_meeting.clone(),
            })
            .collect();
        store.upsert_releases(&rows)?;
        store.apply_release_freeze(&rows)?;
        releases_n = rows.len();
    }

    store.set_version_metadata_source("dynareport")?;
    store.checkpoint()?;
    eprintln!("ingest-catalog: {specs_updated} spec(s) overlaid, {releases_n} release(s)");
    Ok(())
}
