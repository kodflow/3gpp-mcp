//! ingest-li (Rust) — Phase 8b. The Lawful-Interception subject: parse TS 33.128's ASN.1
//! module (TS33128Payloads.asn) with parse3gpp::asn1 and write the authoritative LI event
//! registry (li_events / li_event_fields / li_nf_clauses) + the full asn1 type catalogue
//! (asn1_types) for one release via store-rs. Additive + idempotent (clears the (spec,
//! release) rows first). spec_id is always 33.128.
//!
//! Usage: ingest-li --asn <TS33128Payloads.asn> --db <db> [--release Rel-NN]
use anyhow::{Context, Result};
use clap::Parser;
use parse3gpp::asn1::{members_json, parse_module};
use store_rs::{Asn1TypeIn, LiEventIn, LiFieldIn, LiNfClauseIn, Store};

const LI_SPEC_ID: &str = "33.128";

#[derive(Parser)]
#[command(
    name = "ingest-li",
    about = "Parse TS 33.128 ASN.1 into the LI registry (parse3gpp::asn1 + store-rs)"
)]
struct Args {
    #[arg(long)]
    db: String,
    /// TS33128Payloads.asn text.
    #[arg(long)]
    asn: String,
    /// Override the release (default: the r<NN> from the module OID).
    #[arg(long)]
    release: Option<String>,
}

fn main() -> Result<()> {
    let args = Args::parse();
    let text = std::fs::read_to_string(&args.asn).with_context(|| format!("read {}", args.asn))?;
    let m = parse_module(&text).map_err(|e| anyhow::anyhow!(e))?;
    let release = args.release.unwrap_or_else(|| m.release.clone());

    let events: Vec<LiEventIn> = m
        .events
        .iter()
        .map(|e| LiEventIn {
            interface: e.interface.clone(),
            event_name: e.name.clone(),
            asn1_type: e.asn1_type.clone(),
            asn1_tag: e.tag as i64,
            originating_nf: e.nf.clone(),
            domain: e.domain.clone(),
            spec_clause: e.clause.clone(),
            field_count: e.field_count as i64,
        })
        .collect();
    let fields: Vec<LiFieldIn> = m
        .fields
        .iter()
        .map(|f| LiFieldIn {
            interface: f.interface.clone(),
            event_name: f.event_name.clone(),
            field_name: f.field_name.clone(),
            asn1_type: f.asn1_type.clone(),
            asn1_tag: f.tag as i64,
            is_optional: f.optional,
            ordinal: f.ordinal as i64,
        })
        .collect();
    let nf_clauses: Vec<LiNfClauseIn> = m
        .nf_clauses
        .iter()
        .map(|c| LiNfClauseIn {
            originating_nf: c.nf.clone(),
            interface: c.interface.clone(),
            spec_clause: c.clause.clone(),
        })
        .collect();
    let types: Vec<Asn1TypeIn> = m
        .types
        .iter()
        .map(|t| Asn1TypeIn {
            type_name: t.name.clone(),
            kind: t.kind.clone(),
            members_json: members_json(&t.members),
        })
        .collect();

    let store = Store::open_rw(&args.db)?;
    store.clear_li(LI_SPEC_ID, &release)?;
    store.write_li_registry(
        LI_SPEC_ID,
        &release,
        &m.module_version,
        &events,
        &fields,
        &nf_clauses,
        &types,
    )?;
    store.checkpoint()?;
    eprintln!(
        "ingest-li: {release} {} → {} event(s), {} field(s), {} nf-clause(s), {} type(s)",
        m.module_version,
        events.len(),
        fields.len(),
        nf_clauses.len(),
        types.len()
    );
    Ok(())
}
