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
    /// ETSI corpus mode: ingest every .html under --convert deriving id/version from the
    /// in-body ETSI provenance header (not a 3GPP filename); series/release do not apply.
    #[arg(long, default_value_t = false)]
    etsi: bool,
    /// Accepted for Go cmd/ingest CLI compat (per-spec progress is already terse). No-op.
    #[arg(long, default_value_t = false)]
    quiet: bool,
    /// Accepted for Go cmd/ingest CLI compat (ASN.1 origin zips); the LI registry is now a
    /// separate pass (ingest-li). No-op here.
    #[arg(long, default_value = "")]
    origin: String,
    /// The published corpus, so --resume can ask what is ALREADY HELD rather than only
    /// what this scratch shard happens to remember. Optional; absent = shard-only resume.
    #[arg(long, default_value = "")]
    corpus: String,
    /// With --etsi: report, WITHOUT WRITING, what `--etsi --resume` would add to --db,
    /// and the state of its vector index. --db is attached READ_ONLY; nothing is
    /// restored, dropped, rebuilt or checkpointed. See run_etsi_plan.
    #[arg(long, default_value_t = false)]
    plan: bool,
}

/// What an `--etsi --resume` pass would add to a corpus, measured without writing.
#[derive(Debug, Default, PartialEq)]
struct EtsiPlan {
    /// Deliverables whose (spec, version) is not yet `done` in the corpus ledger.
    pending_docs: usize,
    /// Clauses those deliverables would write. ZERO is the only number the step
    /// needs: every pending deliverable parses to nothing, so a pass would re-parse
    /// them, write no clause, and still rewrite the file around them.
    pending_clauses: usize,
    /// schema_meta.hnsw_state of the corpus ("" when absent).
    hnsw_state: String,
}

/// etsi_done_set reads, READ_ONLY, the (spec, version) pairs the corpus ledger marks
/// done under this pipeline version, and its hnsw_state.
///
/// The corpus is ATTACHED to a throwaway in-memory database rather than opened with
/// Store::open_rw, which bootstraps the schema and migrates: opening it that way is
/// itself a write, and the whole point of the plan is that a pass with nothing to do
/// leaves the file byte for byte as it was.
fn etsi_done_set(
    corpus: &str,
) -> Result<(std::collections::HashSet<(String, String)>, String)> {
    let mem = Store::in_memory()?;
    let conn = mem.raw();
    // Best-effort, as in open_rw: a corpus whose `clauses` carries an HNSW index is
    // attached fine without vss, but loading it costs nothing and keeps a future
    // DuckDB from refusing to bind the catalogue.
    let _ = conn.execute_batch("INSTALL vss; LOAD vss;");
    conn.execute_batch(&format!(
        "ATTACH '{}' AS corp (READ_ONLY)",
        corpus.replace('\'', "''")
    ))
    .with_context(|| format!("attach {corpus} read-only"))?;
    // A corpus WITHOUT the ledger or the meta table (an older or foreign file) has
    // nothing marked done and no index state: that is an empty answer, which plans
    // everything as pending — not an error that would stop the step before the
    // ingest could run. Genuine attach and query errors still propagate.
    let has = |table: &str| -> Result<bool> {
        let n: i64 = conn.query_row(
            "SELECT count(*) FROM duckdb_tables() WHERE database_name = 'corp' AND table_name = ?",
            [table],
            |r| r.get(0),
        )?;
        Ok(n > 0)
    };
    if !has("ingest_log")? || !has("schema_meta")? {
        conn.execute_batch("DETACH corp")?;
        return Ok((std::collections::HashSet::new(), String::new()));
    }
    let done = {
        let mut st = conn.prepare(
            "SELECT DISTINCT spec_id, version FROM corp.ingest_log
             WHERE status = 'done' AND pipeline_version = ?",
        )?;
        let rows = st.query_map([PIPELINE_VERSION], |r| {
            Ok((r.get::<_, String>(0)?, r.get::<_, String>(1)?))
        })?;
        rows.collect::<std::result::Result<std::collections::HashSet<_>, _>>()?
    };
    let hnsw: String = conn.query_row(
        "SELECT COALESCE(MAX(value), '') FROM corp.schema_meta WHERE key = 'hnsw_state'",
        [],
        |r| r.get(0),
    )?;
    conn.execute_batch("DETACH corp")?;
    Ok((done, hnsw))
}

/// etsi_plan decides, file by file, exactly what the `--etsi --resume` loop in main
/// would: the same reader (read_html, windows-1252 fallback — the resume key must be
/// the ingest key, see the comment in that loop), the same header, the same done
/// predicate. What it does NOT do is write: a pending file is parsed in memory and its
/// clauses counted.
fn etsi_plan(
    files: &[String],
    done: &std::collections::HashSet<(String, String)>,
) -> EtsiPlan {
    let mut plan = EtsiPlan::default();
    for f in files {
        let Ok(html) = parse3gpp::html_bytes::read_html(f) else {
            // The ingest would log "skip" and write nothing for it either.
            continue;
        };
        let Some(meta) = parse3gpp::etsi::parse_etsi_meta(&html) else {
            continue; // not an ETSI deliverable: the ingest skips it too
        };
        if done.contains(&(meta.spec_id.clone(), meta.version.clone())) {
            continue;
        }
        let (clauses, _, _) =
            parse_html_clauses(&html, &meta.spec_id, &meta.release, &meta.version);
        plan.pending_docs += 1;
        plan.pending_clauses += clauses.len();
    }
    plan
}

/// run_etsi_plan prints the plan as ONE line the pipeline parses:
///
///   ingest-plan: ETSI pending_docs=19 pending_clauses=0 hnsw_state=frozen corpus=present
///
/// A missing corpus is reported, not an error: it simply has everything to add.
fn run_etsi_plan(convert: &str, db: &str) -> Result<()> {
    let files = collect_html_recursive(convert)?;
    let present = std::path::Path::new(db).metadata().map(|m| m.len() > 0).unwrap_or(false);
    let (done, hnsw) = if present {
        etsi_done_set(db)?
    } else {
        (std::collections::HashSet::new(), String::new())
    };
    let plan = etsi_plan(&files, &done);
    println!(
        "ingest-plan: ETSI pending_docs={} pending_clauses={} hnsw_state={} corpus={} files={}",
        plan.pending_docs,
        plan.pending_clauses,
        if plan.hnsw_state.is_empty() { hnsw.as_str() } else { plan.hnsw_state.as_str() },
        if present { "present" } else { "absent" },
        files.len()
    );
    Ok(())
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

/// ingest_one parses one converted HTML by its 3GPP filename and writes it.
fn ingest_one(store: &Store, html_path: &str, offset: u64) -> Result<(SpecMeta, usize)> {
    let meta = parse_filename_meta(html_path).map_err(|e| anyhow::anyhow!(e))?;
    let html =
        parse3gpp::html_bytes::read_html(html_path).with_context(|| format!("read {html_path}"))?;
    let n = write_spec(store, &meta, &html, offset)?;
    Ok((meta, n))
}

/// ingest_etsi_one parses one converted ETSI deliverable by its in-body provenance header
/// (no 3GPP filename) and writes it. None when the file carries no ETSI header.
fn ingest_etsi_one(
    store: &Store,
    html_path: &str,
    offset: u64,
) -> Result<Option<(SpecMeta, usize)>> {
    // Same reasoning as the 3GPP path: an ETSI PDF converted through a Western-encoded
    // intermediate must not be discarded over its punctuation.
    let html =
        parse3gpp::html_bytes::read_html(html_path).with_context(|| format!("read {html_path}"))?;
    let Some(meta) = parse3gpp::etsi::parse_etsi_meta(&html) else {
        return Ok(None);
    };
    let n = write_spec(store, &meta, &html, offset)?;
    Ok(Some((meta, n)))
}

/// write_spec parses the clauses and writes the spec/version/clauses (+ glossary subject),
/// offsetting clause chunk_ids by `offset` so a multi-spec DB keeps a single id space.
fn write_spec(store: &Store, meta: &SpecMeta, html: &str, offset: u64) -> Result<usize> {
    let (clauses, _saw_ch, _degraded) =
        parse_html_clauses(html, &meta.spec_id, &meta.release, &meta.version);

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
                // NOT COUNTED, which is what 0 means. This pass reads ONE spec —
                // TS 21.905 — so it has no agreement to count, and the 3GPP half
                // does not rank on agreement anyway: TS 23.501 §3.2 gives it a
                // precedence rule instead. Stored NULL, read as 1.
                0,
            )?;
        }
    }
    // A DOCUMENT THAT YIELDED NO CLAUSE DOES NOT GET TO CLAIM THE SPEC.
    //
    // One archive can hold several documents for the same (spec, version): TR 30.531
    // v1.64.0 ships a 6 KB plenary cover note beside two 1.6 MB drafts. They are walked
    // in name order, the cover sorts first, it parses to nothing — and marking it
    // "done" made --resume skip the drafts that carry the actual text. The spec was
    // then catalogued with no clauses behind it, which is a missing_content hole
    // produced by the ingest itself.
    //
    // Marking "done" only when something was parsed lets the next candidate for the
    // same (spec, version) have its turn, and leaves the slot open if none of them
    // works — which is the honest outcome, and a visible one.
    if rows.is_empty() {
        eprintln!(
            "ingest: {} {} produced no clause — leaving the slot open for another document",
            meta.spec_id, meta.version
        );
    } else {
        store.log_ingest(&meta.spec_id, &meta.version, "done", PIPELINE_VERSION)?;
    }
    Ok(rows.len())
}

/// collect_html_recursive walks all .html under a root (any depth) — the ETSI corpus has
/// no series/release dir structure; spec id/version come from each file's body header.
fn collect_html_recursive(root: &str) -> Result<Vec<String>> {
    let mut out = Vec::new();
    let mut stack = vec![std::path::PathBuf::from(root)];
    while let Some(dir) = stack.pop() {
        let Ok(rd) = std::fs::read_dir(&dir) else {
            continue;
        };
        for e in rd {
            let p = e?.path();
            if p.is_dir() {
                stack.push(p);
            } else if p.extension().and_then(|x| x.to_str()) == Some("html") {
                if let Some(s) = p.to_str() {
                    out.push(s.to_string());
                }
            }
        }
    }
    out.sort();
    Ok(out)
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

    // BEFORE open_rw, which bootstraps and migrates — i.e. writes. The plan must not.
    if args.plan {
        anyhow::ensure!(args.etsi, "--plan is only defined with --etsi");
        let convert = args
            .convert
            .as_deref()
            .context("--etsi --plan requires --convert <dir>")?;
        return run_etsi_plan(convert, &args.db);
    }

    let store = Store::open_rw(&args.db)?;
    let _ = (&args.quiet, &args.origin); // accepted-for-compat no-ops

    if args.count_only {
        return run_count_only(&store);
    }

    // ETSI corpus mode: walk every .html under --convert, deriving id/version from the body.
    if args.etsi {
        let convert = args
            .convert
            .as_deref()
            .context("--etsi requires --convert <dir>")?;
        let mut specs = 0usize;
        let mut clauses = 0usize;

        // THE TABLE BEING APPENDED TO CARRIES A VECTOR INDEX, AND DUCKDB MAINTAINS
        // IT ROW BY ROW.
        //
        // `ingest --etsi` IS the publish step on this side — there is no merge — so
        // it writes straight into the file that gets served, and that file already
        // carries clauses_hnsw plus three ART indexes from the previous run.
        //
        // Measured 2026-09-01 on the all-versions ingest: 174 287 new clauses took
        // data/etsi.duckdb from 8.0 GiB to 101.8 GiB, about 560 KB of file per row,
        // and still accelerating when it was stopped. It could not have finished on
        // any disk here. The first ETSI ingest never showed it because the index then
        // covered 1 042 vectors rather than 510 384.
        //
        // The ART indexes come back below, over settled data. The vector index does
        // not: rebuilding it needs the embedding identity to stamp, which is
        // freeze-hnsw's job (the `index-etsi` step), and drop_clause_indexes sets
        // hnsw_state to "building" so nothing serves a graph that is no longer there.
        store
            .drop_clause_indexes()
            .context("drop clause indexes before the ETSI bulk load")?;

        for f in collect_html_recursive(convert)? {
            if args.resume {
                // THE SAME READER THE INGEST USES, and that is the whole fix.
                //
                // This asked std::fs::read_to_string, which is STRICT UTF-8 and returns
                // Err on anything else. ingest_etsi_one reads the same file through
                // parse3gpp::html_bytes::read_html, which falls back to windows-1252.
                // So a deliverable that is not valid UTF-8 could be INGESTED but never
                // RECOGNISED as already ingested: `.ok()` turned the read error into
                // None, the `if let` did not match, and the file was written again.
                //
                // Every build. Measured on the published corpus 2026-09-09: exactly two
                // files in 11 822 are not UTF-8 —
                //   data/sources/convert-etsi/ETSI/TR_104_066_v1.1.1.html
                //   data/sources/convert-etsi/ETSI/TS_103_634_v1.1.1.html
                // — and both had been written FIFTEEN times: 1 155 rows for 77 clauses,
                // 7 335 rows for 489. Together 566 clauses per build, which is precisely
                // the +566 the ETSI clause count gained between builds D, E and F on a
                // converted tree that never changed. Every gate stayed green throughout,
                // including the work-list reconciliation added the same day: it asks
                // whether anything is MISSING, and nothing was.
                //
                // The two paths must read bytes the same way or the resume key is not
                // the ingest key. read_html is the one that decides what a deliverable
                // IS, so it is the one that decides whether we have seen it.
                if let Some(m) = parse3gpp::html_bytes::read_html(&f)
                    .ok()
                    .and_then(|h| parse3gpp::etsi::parse_etsi_meta(&h))
                {
                    if store.ingest_done(&m.spec_id, &m.version, PIPELINE_VERSION)? {
                        continue;
                    }
                }
            }
            let off = store.max_chunk_id()?;
            match ingest_etsi_one(&store, &f, off) {
                Ok(Some((_, n))) => {
                    specs += 1;
                    clauses += n;
                }
                Ok(None) => {} // no ETSI header → not an ETSI deliverable, skip
                Err(e) => eprintln!("ingest: skip {f}: {e:#}"),
            }
        }
        eprintln!("ingest: ETSI → {specs} spec(s), {clauses} clause(s)");
        eprintln!("ingest: rebuilding the clause indexes over settled data…");
        store
            .create_clause_indexes()
            .context("rebuild clause indexes after the ETSI bulk load")?;
        store.set_meta("producer", "rust-writeside")?;
        store.set_meta("schema_version", "1")?;
        store.checkpoint()?;

        // BUILD THE BM25 INDEX HERE — nothing downstream will.
        //
        // The corpus carries TWO indexes: dense HNSW and lexical BM25/FTS. On the
        // 3GPP side `merge` builds the FTS because merge is what publishes the
        // corpus; the per-series shards ingest writes are throwaway inputs.
        //
        // ETSI has no merge: `ingest --etsi` IS the publish. Leaving the FTS to a
        // step that never runs shipped an ETSI corpus with only half its indexes —
        // `validate --require-fts` on etsi.duckdb reported fts_available=false while
        // the HNSW was frozen and green. Best-effort like merge: a missing extension
        // degrades search to LIKE, it does not invalidate the corpus.
        if let Err(e) = store.enable_fts() {
            eprintln!("ingest: FTS build skipped ({e})");
        }
        store.checkpoint()?;
        return Ok(());
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

        // RESUME AGAINST THE CORPUS, NOT ONLY AGAINST THE SHARD.
        //
        // ingest_log lives in the SHARD, and a shard is scratch. Delete it, or start a
        // series that never had one, and the ledger is empty — so every converted file
        // of the series is parsed and written again. The 2026-08-25 run re-ingested
        // ~300 000 clauses that way to acquire five specs, and merge then had to decide
        // bucket by bucket that almost none of it had moved.
        //
        // The corpus is the durable record of what is already held. When the caller
        // names it, a document it already carries is skipped whatever the shard
        // remembers. --corpus is optional: without it the old behaviour stands.
        let already: std::collections::HashSet<(String, String)> = match Some(args.corpus.as_str())
        {
            Some(c) if !c.is_empty() && std::path::Path::new(c).exists() => {
                let v = store.corpus_versions_with_text(c)?;
                eprintln!(
                    "ingest: corpus already holds {} (spec, version) pair(s)",
                    v.len()
                );
                v.into_iter().collect()
            }
            _ => std::collections::HashSet::new(),
        };

        let mut specs = 0usize;
        let mut clauses = 0usize;
        for f in &files {
            if args.resume {
                if let Ok(m) = parse_filename_meta(f) {
                    if store.ingest_done(&m.spec_id, &m.version, PIPELINE_VERSION)?
                        || already.contains(&(m.spec_id.clone(), m.version.clone()))
                    {
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

#[cfg(test)]
mod tests {
    use super::*;
}

#[cfg(test)]
mod plan_tests {
    use super::*;

    fn tmp(name: &str) -> std::path::PathBuf {
        let d = std::env::temp_dir().join(format!("ingest-plan-{}-{name}", std::process::id()));
        let _ = std::fs::remove_dir_all(&d);
        std::fs::create_dir_all(&d).unwrap();
        d
    }

    const WITH_TEXT: &str = r#"<!-- ETSI-SPEC: 103 221-1 | 1.21.1 --><html><body><h1>4.1 Architecture</h1><p>the actual text</p></body></html>"#;
    const EMPTY: &str = r#"<!-- ETSI-SPEC: 103 999 | 1.1.1 --><html><body><p>cover note</p></body></html>"#;

    // THE PLAN IS THE INGEST'S OWN DECISION, MINUS THE WRITES. A corpus that already
    // holds the deliverable with text, plus a deliverable that parses to nothing (the
    // 19 the real corpus re-parses on every pass), plans ZERO clauses — and the file
    // is not modified by asking.
    #[test]
    fn a_corpus_holding_everything_plans_zero_clauses_and_is_not_touched() {
        let dir = tmp("held");
        let conv = dir.join("convert");
        std::fs::create_dir_all(&conv).unwrap();
        std::fs::write(conv.join("TS_103_221-1_v1.21.1.html"), WITH_TEXT).unwrap();
        std::fs::write(conv.join("TS_103_999_v1.1.1.html"), EMPTY).unwrap();
        let db = dir.join("etsi.duckdb");
        {
            let store = Store::open_rw(db.to_str().unwrap()).unwrap();
            let meta = parse3gpp::etsi::parse_etsi_meta(WITH_TEXT).unwrap();
            assert!(write_spec(&store, &meta, WITH_TEXT, 0).unwrap() > 0);
            store.set_meta("hnsw_state", "frozen").unwrap();
            store.checkpoint().unwrap();
        }
        let before = std::fs::read(&db).unwrap();

        let files = collect_html_recursive(conv.to_str().unwrap()).unwrap();
        let (done, hnsw) = etsi_done_set(db.to_str().unwrap()).unwrap();
        let plan = etsi_plan(&files, &done);
        assert_eq!(plan.pending_docs, 1, "only the cover note is pending");
        assert_eq!(plan.pending_clauses, 0, "and it parses to nothing");
        assert_eq!(hnsw, "frozen");
        assert_eq!(std::fs::read(&db).unwrap(), before, "planning wrote to the corpus");
        let _ = std::fs::remove_dir_all(&dir);
    }

    // And a deliverable the corpus does not hold, with text, is counted.
    #[test]
    fn a_new_deliverable_with_text_is_pending() {
        let dir = tmp("new");
        let conv = dir.join("convert");
        std::fs::create_dir_all(&conv).unwrap();
        std::fs::write(conv.join("TS_103_221-1_v1.21.1.html"), WITH_TEXT).unwrap();
        let db = dir.join("etsi.duckdb");
        {
            let store = Store::open_rw(db.to_str().unwrap()).unwrap();
            store.checkpoint().unwrap();
        }
        let files = collect_html_recursive(conv.to_str().unwrap()).unwrap();
        let (done, _) = etsi_done_set(db.to_str().unwrap()).unwrap();
        let plan = etsi_plan(&files, &done);
        assert_eq!(plan.pending_docs, 1);
        assert!(plan.pending_clauses > 0, "a new deliverable with text must plan clauses");
        let _ = std::fs::remove_dir_all(&dir);
    }

    // A file that is not a corpus of this schema — no ledger, no meta — plans as
    // "everything pending", it does not fail the step before the ingest can run.
    #[test]
    fn a_corpus_without_the_ledger_plans_everything_pending() {
        let dir = tmp("bare");
        let db = dir.join("etsi.duckdb");
        {
            let mem = Store::in_memory().unwrap();
            mem.raw()
                .execute_batch(&format!(
                    "ATTACH '{}' AS f; CREATE TABLE f.other (x INTEGER); DETACH f;",
                    db.to_str().unwrap().replace('\'', "''")
                ))
                .unwrap();
        }
        let (done, hnsw) = etsi_done_set(db.to_str().unwrap()).unwrap();
        assert!(done.is_empty() && hnsw.is_empty());
        let _ = std::fs::remove_dir_all(&dir);
    }

    // A ledger row from ANOTHER pipeline version does not count as done: the ingest
    // re-ingests it, so the plan must say so.
    #[test]
    fn a_ledger_row_from_another_pipeline_version_is_not_done() {
        let dir = tmp("pv");
        let db = dir.join("etsi.duckdb");
        {
            let store = Store::open_rw(db.to_str().unwrap()).unwrap();
            store
                .log_ingest("ETSI TS 103 221-1", "1.21.1", "done", "some-older-pipeline")
                .unwrap();
            store.checkpoint().unwrap();
        }
        let (done, _) = etsi_done_set(db.to_str().unwrap()).unwrap();
        assert!(done.is_empty(), "a done row under another pipeline version was trusted");
        let _ = std::fs::remove_dir_all(&dir);
    }
}

#[cfg(test)]
mod claim_tests {
    use super::*;

    // A document that yielded no clause must not claim the (spec, version) slot.
    //
    // One archive can hold several documents for the same version: TR 30.531 v1.64.0
    // ships a 6 KB plenary cover note beside two 1.6 MB drafts. They are walked in name
    // order, the cover sorts first, it parses to nothing — and marking it "done" made
    // --resume skip the drafts carrying the actual text. The spec was catalogued with
    // no clauses behind it: a missing_content hole produced by the ingest itself.
    #[test]
    fn an_empty_parse_leaves_the_slot_open() {
        let store = Store::in_memory().unwrap();
        let meta = SpecMeta {
            spec_id: "30.531".into(),
            series: "30".into(),
            doc_type: "TR".into(),
            working_group: "RAN3".into(),
            release: "Rel-10".into(),
            version: "1.64.0".into(),
            docx_url: String::new(),
        };

        // The cover note: valid HTML, no clause structure at all.
        let cover = "<html><body><p>Presentation of TR 30.531 for information</p></body></html>";
        let n = write_spec(&store, &meta, cover, 0).unwrap();
        assert_eq!(n, 0, "the cover carries no clause");
        assert!(
            !store
                .ingest_done("30.531", "1.64.0", PIPELINE_VERSION)
                .unwrap(),
            "an empty parse must NOT mark the version done, or the real draft is skipped"
        );

        // The real draft, next in the walk, now gets its turn.
        let draft = "<html><body><h1>4.1 Architecture</h1><p>the actual text</p></body></html>";
        let n = write_spec(&store, &meta, draft, 100).unwrap();
        assert!(n > 0, "the draft carries clauses");
        assert!(
            store
                .ingest_done("30.531", "1.64.0", PIPELINE_VERSION)
                .unwrap(),
            "a parse that produced clauses DOES claim the version"
        );
    }
}
