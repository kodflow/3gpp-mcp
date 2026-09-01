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

/// read_html reads a converted document as text, accepting the encodings LibreOffice
/// actually emits rather than the one we would prefer.
///
/// LibreOffice's HTML export keeps the SOURCE document's charset. A .doc written in
/// Western Europe comes out as windows-1252, not UTF-8, and `read_to_string` refuses
/// it outright: "stream did not contain valid UTF-8". ingest then logs a skip line to
/// stderr and reports SUCCESS for the series, so the spec is fetched, converted, and
/// silently never indexed. All six releases of TS 34.123-1 were lost that way on the
/// 2026-08-25 repair — acquired three times over, dropped three times over, and
/// counted as a corpus hole nobody could explain.
///
/// So: use UTF-8 when the bytes are UTF-8, and otherwise decode as windows-1252,
/// which is what those files are. The mapping is Latin-1 for 0xA0-0xFF and the
/// CP1252 punctuation block for 0x80-0x9F — the quotes and dashes Word litters
/// through prose. Nothing is dropped and no byte can fail to decode, which is the
/// point: a conformance spec must not be discarded over a typographic apostrophe.
fn read_html(path: &str) -> Result<String> {
    let bytes = std::fs::read(path).with_context(|| format!("read {path}"))?;
    match String::from_utf8(bytes) {
        Ok(s) => Ok(s),
        Err(e) => {
            let bytes = e.into_bytes();
            eprintln!("ingest: {path}: not UTF-8, decoding as windows-1252");
            Ok(bytes.iter().map(|&b| cp1252_char(b)).collect())
        }
    }
}

/// cp1252_char maps one windows-1252 byte to its Unicode character. 0x00-0x7F and
/// 0xA0-0xFF are Latin-1, i.e. identical to the code point; 0x80-0x9F is the CP1252
/// punctuation block, which Latin-1 leaves undefined and Word uses constantly.
fn cp1252_char(b: u8) -> char {
    const HIGH: [char; 32] = [
        '\u{20AC}', '\u{FFFD}', '\u{201A}', '\u{0192}', '\u{201E}', '\u{2026}', '\u{2020}',
        '\u{2021}', '\u{02C6}', '\u{2030}', '\u{0160}', '\u{2039}', '\u{0152}', '\u{FFFD}',
        '\u{017D}', '\u{FFFD}', '\u{FFFD}', '\u{2018}', '\u{2019}', '\u{201C}', '\u{201D}',
        '\u{2022}', '\u{2013}', '\u{2014}', '\u{02DC}', '\u{2122}', '\u{0161}', '\u{203A}',
        '\u{0153}', '\u{FFFD}', '\u{017E}', '\u{0178}',
    ];
    if (0x80..=0x9F).contains(&b) {
        HIGH[(b - 0x80) as usize]
    } else {
        b as char
    }
}

/// ingest_one parses one converted HTML by its 3GPP filename and writes it.
fn ingest_one(store: &Store, html_path: &str, offset: u64) -> Result<(SpecMeta, usize)> {
    let meta = parse_filename_meta(html_path).map_err(|e| anyhow::anyhow!(e))?;
    let html = read_html(html_path)?;
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
    let html = read_html(html_path)?;
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
                if let Some(m) = std::fs::read_to_string(&f)
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

    // A converted document must not be discarded over its punctuation.
    //
    // LibreOffice's HTML export keeps the SOURCE document's charset, so a .doc
    // written in Western Europe comes out as windows-1252 and `read_to_string`
    // refuses it: "stream did not contain valid UTF-8". ingest logged a skip line to
    // stderr and reported SUCCESS for the series — so all six releases of
    // TS 34.123-1 were fetched, converted, and silently never indexed, then counted
    // as corpus holes nobody could explain.
    #[test]
    fn a_windows_1252_document_is_read_rather_than_discarded() {
        let dir = std::env::temp_dir().join(format!("ingest-cp1252-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let p = dir.join("doc.html");

        // 0xE9 is 'é' in windows-1252 and an invalid lone byte in UTF-8; 0x92 is the
        // right single quote Word inserts for every apostrophe it touches.
        let mut bytes = b"<html><body><p>proc".to_vec();
        bytes.push(0xE9); // é
        bytes.extend_from_slice(b"dure d");
        bytes.push(0x92); // ’
        bytes.extend_from_slice(b"essai</p></body></html>");
        std::fs::write(&p, &bytes).unwrap();

        let got = read_html(p.to_str().unwrap()).expect("a non-UTF-8 document must still be read");
        assert!(
            got.contains("procédure"),
            "Latin-1 accents must survive: {got}"
        );
        assert!(
            got.contains('\u{2019}'),
            "the CP1252 quote must decode, not vanish: {got}"
        );
        assert!(
            !got.contains('\u{FFFD}'),
            "nothing may be replaced by U+FFFD: {got}"
        );

        let _ = std::fs::remove_dir_all(&dir);
    }

    // Valid UTF-8 must go through untouched — the fallback exists for the files that
    // need it, and must not become a lossy path for the files that do not.
    #[test]
    fn a_utf8_document_is_untouched() {
        let dir = std::env::temp_dir().join(format!("ingest-utf8-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let p = dir.join("doc.html");
        let want = "<p>procédure d’essai — 日本語</p>";
        std::fs::write(&p, want.as_bytes()).unwrap();

        let got = read_html(p.to_str().unwrap()).unwrap();
        assert_eq!(got, want, "valid UTF-8 must be returned byte for byte");

        let _ = std::fs::remove_dir_all(&dir);
    }

    // The CP1252 punctuation block is the whole reason a plain Latin-1 decode is not
    // enough: Latin-1 leaves 0x80-0x9F undefined, and Word fills it constantly.
    #[test]
    fn the_cp1252_punctuation_block_decodes() {
        for (byte, want) in [
            (0x80u8, '\u{20AC}'), // €
            (0x93, '\u{201C}'),   // “
            (0x94, '\u{201D}'),   // ”
            (0x96, '\u{2013}'),   // –
            (0x97, '\u{2014}'),   // —
        ] {
            assert_eq!(cp1252_char(byte), want, "byte {byte:#04x}");
        }
        // Outside that block the byte IS the code point.
        assert_eq!(cp1252_char(0x41), 'A');
        assert_eq!(cp1252_char(0xE9), 'é');
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
