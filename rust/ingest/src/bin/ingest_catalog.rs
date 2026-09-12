//! ingest-catalog (Rust) — Phase 7. The additive DynaReport metadata overlay: read the
//! catalogue projection `discover --emit-catalog` derives from the public
//! status-report.htm (per-spec title / TS-or-TR / working group) and
//! Releases.aspx (release calendar + freeze_date) and overlay them onto specs/spec_versions
//! rows content ingest already created. Idempotent, cite-or-silent (never invents
//! catalogue-only specs — UpdateSpecMeta only updates rows already on disk).
//!
//! Usage: ingest-catalog --db <db> [--catalog <tsv>] [--releases <file>]
//!
//! IT TAKES THE PROJECTION, NOT THE HTML. www.3gpp.org serves the status report
//! with a fresh CSP nonce, CSRF token, GDPR session id and Cloudflare e-mail
//! obfuscation on every single response, so the file this overlay used to declare
//! as its input could never be equal to itself twice. Reading the four fields it
//! actually writes — through discover, which owns that parser — is what lets an
//! unchanged catalogue leave the corpus, and the published image, alone.
use anyhow::{Context, Result};
use clap::Parser;
use parse3gpp::catalog::parse_releases;
use store_rs::{ReleaseRow, Store};

#[derive(Parser)]
#[command(
    name = "ingest-catalog",
    about = "Overlay DynaReport metadata (title/WG/freeze_date) onto an existing DuckDB"
)]
struct Args {
    #[arg(long)]
    db: String,
    /// catalogue projection from `discover --emit-catalog`:
    /// spec_id \t doc_type \t working_group \t title, one spec per line.
    #[arg(long)]
    catalog: Option<String>,
    /// portal Releases.aspx (release calendar + freeze_date).
    #[arg(long)]
    releases: Option<String>,
}

/// A projection line. Four fields, in the order `discover --emit-catalog` writes
/// them; a line with fewer is a truncated projection and is refused rather than
/// overlaid as empty metadata — an empty title would silently blank a spec the
/// content ingest had titled correctly.
struct CatalogRow {
    spec_id: String,
    doc_type: String,
    working_group: String,
    title: String,
}

fn parse_catalog(data: &str) -> Result<Vec<CatalogRow>> {
    let mut rows = Vec::new();
    for (n, line) in data.lines().enumerate() {
        if line.trim().is_empty() {
            continue;
        }
        let f: Vec<&str> = line.split('\t').collect();
        if f.len() != 4 {
            anyhow::bail!(
                "catalogue projection line {}: {} field(s), want 4 — the projection and this \
                 reader have drifted apart",
                n + 1,
                f.len()
            );
        }
        rows.push(CatalogRow {
            spec_id: f[0].to_string(),
            doc_type: f[1].to_string(),
            working_group: f[2].to_string(),
            title: f[3].to_string(),
        });
    }
    Ok(rows)
}

fn main() -> Result<()> {
    let args = Args::parse();
    let store = Store::open_rw(&args.db)?;

    let mut specs_updated = 0usize;
    if let Some(path) = args.catalog.as_deref() {
        let data = std::fs::read_to_string(path).with_context(|| format!("read {path}"))?;
        let specs = parse_catalog(&data)?;
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_the_four_fields_in_order() {
        let rows = parse_catalog("23.501\tTS\tS2\tSystem architecture\n").unwrap();
        assert_eq!(rows.len(), 1);
        assert_eq!(rows[0].spec_id, "23.501");
        assert_eq!(rows[0].doc_type, "TS");
        assert_eq!(rows[0].working_group, "S2");
        assert_eq!(rows[0].title, "System architecture");
    }

    // A SHORT LINE IS A DRIFT, NOT AN EMPTY FIELD. update_spec_meta writes what it
    // is handed, so accepting three fields would overlay an empty title over a
    // title the content ingest got right, on every spec, silently.
    #[test]
    fn refuses_a_line_that_is_not_four_fields() {
        assert!(parse_catalog("23.501\tTS\tS2\n").is_err());
        assert!(parse_catalog("23.501\tTS\tS2\ttitle\textra\n").is_err());
    }

    #[test]
    fn skips_blank_lines() {
        let rows = parse_catalog("\n23.501\tTS\tS2\tTitle\n\n").unwrap();
        assert_eq!(rows.len(), 1);
    }
}
