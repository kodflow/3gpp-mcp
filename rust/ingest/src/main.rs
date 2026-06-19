//! ingest (Rust) — Phase 5/10 of the write-side migration. Parses converted specs with
//! parse3gpp and writes them to DuckDB through store-rs, closing the loop: Rust parses +
//! writes the corpus, Go serves it read-only. Replaces the parse half of Go's cmd/ingest.
//!
//! Two modes:
//!   ingest --html <…/convert/<Rel>/<num>-<code>.html> --db <out>     (single file)
//!   ingest --series <NN> --convert <…/convert> --db <out> [--resume]  (series batch, the
//!     drop-in for Go `ingest --series NN --out … --resume`: walks every <Rel>/<file>.html
//!     whose spec is in the series, offsetting chunk_ids so all shards share one id space).
use anyhow::{Context, Result};
use clap::Parser;
use parse3gpp::{parse_filename_meta, parse_html_clauses, SpecMeta};
use store_rs::{ClauseIn, Store};

/// The ingest pipeline identity stamped into the resume ledger; a change here (new parser
/// / chunker) invalidates the log and forces a rebuild. Mirrors model.SpecIngestParts.
const PIPELINE_VERSION: &str = "html-v2|clause-leaf-v1|1";

#[derive(Parser)]
#[command(
    name = "ingest",
    about = "Parse converted 3GPP specs and write them to DuckDB (parse3gpp + store-rs)"
)]
struct Args {
    /// Single converted HTML spec at …/convert/<Rel>/<num>-<code>.html.
    #[arg(long)]
    html: Option<String>,
    /// Batch: 2-digit series to ingest from --convert.
    #[arg(long)]
    series: Option<String>,
    /// Batch: the converted-corpus root (…/convert), holding <Rel>/<num>-<code>.html.
    #[arg(long)]
    convert: Option<String>,
    /// Batch: restrict to one release dir (the Go `--release Rel-NN` / --relflag). Empty = all.
    #[arg(long, default_value = "")]
    release: String,
    /// Output DuckDB shard (created/opened read-write).
    #[arg(long)]
    db: String,
    /// Batch: skip specs already 'done' in the ingest ledger under this pipeline_version.
    #[arg(long, default_value_t = false)]
    resume: bool,
    /// Phase-0: open --db, print clause embedded/null counts as JSON, then exit (== Go
    /// cmd/ingest --count-only; the CI gate reads .embedded_clauses / .clauses).
    #[arg(long, default_value_t = false)]
    count_only: bool,
}

/// run_count_only mirrors Go cmd/ingest --count-only: a read-only count summary as JSON.
fn run_count_only(store: &Store) -> Result<()> {
    let total = store.count_clauses()?;
    let null = store.count_null_embeddings()?;
    let model = store.get_meta("embedding_model")?;
    let hnsw = store.get_meta("hnsw_state")? == "frozen";
    // JSON object with the keys the Phase-0 CI gate consumes.
    println!(
        "{{\n  \"clauses\": {total},\n  \"embedded_clauses\": {},\n  \"null_embeddings\": {null},\n  \"this_run_clauses\": 0,\n  \"model\": \"{}\",\n  \"hnsw\": {hnsw},\n  \"version\": \"rust\"\n}}",
        total - null,
        model.replace('"', "\\\"")
    );
    Ok(())
}

/// ingest_one parses one converted HTML and writes it, offsetting clause chunk_ids by
/// `offset` so a multi-spec DB keeps a single id space. Returns the clause count.
fn ingest_one(store: &Store, html_path: &str, offset: u64) -> Result<(SpecMeta, usize)> {
    let meta = parse_filename_meta(html_path).map_err(|e| anyhow::anyhow!(e))?;
    let html = std::fs::read_to_string(html_path).with_context(|| format!("read {html_path}"))?;
    let (clauses, _saw_ch, _degraded) =
        parse_html_clauses(&html, &meta.spec_id, &meta.release, &meta.version);

    store.log_ingest(&meta.spec_id, &meta.version, "started", PIPELINE_VERSION)?;
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
            chunk_id: c.chunk_id + offset,
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

    // Subject pass: the glossary vertical seeds the acronym vocabulary from TS 21.905.
    if meta.spec_id == parse3gpp::glossary::GLOSSARY_SPEC_ID {
        for a in parse3gpp::glossary::extract_acronyms(&clauses, &meta.release) {
            store.upsert_acronym(
                &a.term,
                &a.expansion,
                "",
                &a.first_release,
                &a.last_release,
                &a.source_series,
            )?;
        }
    }
    store.log_ingest(&meta.spec_id, &meta.version, "done", PIPELINE_VERSION)?;
    Ok((meta, rows.len()))
}

/// collect_series_html walks <convert>/<Rel>/*.html keeping files whose spec_id is in the
/// series, sorted for deterministic chunk_id assignment.
fn collect_series_html(convert: &str, series: &str, release: &str) -> Result<Vec<String>> {
    // --series accepts a comma-separated set (the Go --series CSV, e.g. "23,33").
    let wanted: std::collections::HashSet<&str> = series
        .split(',')
        .map(str::trim)
        .filter(|s| !s.is_empty())
        .collect();
    let mut out = Vec::new();
    for rel in std::fs::read_dir(convert).with_context(|| format!("read_dir {convert}"))? {
        let rel = rel?.path();
        if !rel.is_dir() {
            continue;
        }
        for f in std::fs::read_dir(&rel)? {
            let p = f?.path();
            if p.extension().and_then(|e| e.to_str()) != Some("html") {
                continue;
            }
            let Some(path) = p.to_str() else { continue };
            if let Ok(meta) = parse_filename_meta(path) {
                if wanted.contains(meta.series.as_str())
                    && (release.is_empty() || meta.release == release)
                {
                    out.push(path.to_string());
                }
            }
        }
    }
    out.sort();
    Ok(out)
}

fn main() -> Result<()> {
    let args = Args::parse();
    let store = Store::open_rw(&args.db)?;

    if args.count_only {
        return run_count_only(&store);
    }

    let total: usize = if let Some(html) = args.html.as_deref() {
        let off = store.max_chunk_id()?;
        let (m, n) = ingest_one(&store, html, off)?;
        eprintln!(
            "ingest: {} {} {} → {n} clause(s)",
            m.spec_id, m.release, m.version
        );
        n
    } else {
        let (Some(series), Some(convert)) = (args.series.as_deref(), args.convert.as_deref())
        else {
            anyhow::bail!("ingest: pass --html <file> or --series <NN> --convert <dir>");
        };
        let files = collect_series_html(convert, series, &args.release)?;
        let mut specs = 0usize;
        let mut clauses = 0usize;
        for f in &files {
            if args.resume {
                if let Ok(m) = parse_filename_meta(f) {
                    if store.ingest_done(&m.spec_id, &m.version, PIPELINE_VERSION)? {
                        continue;
                    }
                }
            }
            let off = store.max_chunk_id()?;
            match ingest_one(&store, f, off) {
                Ok((_, n)) => {
                    specs += 1;
                    clauses += n;
                }
                Err(e) => eprintln!("ingest: skip {f}: {e:#}"),
            }
        }
        eprintln!(
            "ingest: series {series} → {specs} spec(s), {clauses} clause(s) ({} file(s))",
            files.len()
        );
        clauses
    };

    // Producer marker (Phase 11a A14): stamp the shard as Rust-produced.
    store.set_meta("producer", "rust-writeside")?;
    store.set_meta("schema_version", "1")?;
    store.checkpoint()?;
    let _ = total;
    Ok(())
}
