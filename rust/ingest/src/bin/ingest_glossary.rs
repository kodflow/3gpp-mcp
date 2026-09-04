//! ingest-glossary — mine the ETSI half's vocabulary into the acronyms table.
//!
//! WHY A SEPARATE PASS. The 3GPP glossary comes from ONE spec (TS 21.905) and is
//! extracted during that spec's ingest. ETSI has no vocabulary deliverable: every
//! deliverable carries its own "Abbreviations" clause, so the vocabulary is spread
//! across the whole archive. Following ingest-catalog / ingest-li / ingest-openapi,
//! that is an enrichment pass over the converted HTML, not a change to ingest — it
//! writes ONLY acronyms, so it can be re-run at any time without touching a clause.
//!
//! WHAT IT MINES. parse3gpp::glossary::extract_acronyms is already generic: it finds
//! the clause whose heading contains "abbreviation" and reads
//! "ABBR<TAB|2+ spaces>Expansion" lines until the region ends. It was gated to
//! 21.905 only because that is where the 3GPP vocabulary lives.
//!
//! Measured on TS 102 221 v18.4.0 before writing this: 122 entries from clause 3.3
//! "Abbreviations" and 12 from 3.2 "Symbols" (Di = Baud rate adjustment integer,
//! Fi = clock rate conversion factor — vocabulary too), and ZERO from the prose of
//! 3.1 "Definitions". The separator requirement is what keeps prose out, and it
//! holds on pdftotext -layout output.
//!
//! ONE VERSION PER DELIVERABLE. The archive holds 11 822 versions of 5 142
//! deliverables and their abbreviation lists barely move, so this reads the NEWEST
//! version of each: the current meaning of a term, at a fraction of the work.
//!
//! Usage: ingest-glossary --convert <dir> --db <db> [--source-series etsi]
use anyhow::{Context, Result};
use clap::Parser;
use std::collections::HashMap;

#[derive(Parser)]
#[command(
    name = "ingest-glossary",
    about = "Mine every ETSI deliverable's Abbreviations clause into the acronyms table"
)]
struct Args {
    /// Converted-corpus root holding the ETSI HTML (data/sources/convert-etsi).
    #[arg(long)]
    convert: String,
    /// DuckDB to write the acronyms into (the ETSI half).
    #[arg(long)]
    db: String,
    /// Stamped on every row so a later purge can scope these without touching the
    /// 3GPP vocabulary, which carries its own series ("21").
    #[arg(long, default_value = "etsi")]
    source_series: String,
}

/// version_key turns "18.4.0" into a comparable tuple, so 18.10.0 sorts after
/// 18.9.0. A string compare puts them the other way round, which is the same defect
/// versionOrderSQL exists to avoid on the serve side.
fn version_key(v: &str) -> (u32, u32, u32) {
    let mut it = v.split('.').map(|p| p.parse::<u32>().unwrap_or(0));
    (
        it.next().unwrap_or(0),
        it.next().unwrap_or(0),
        it.next().unwrap_or(0),
    )
}

/// newest_per_deliverable keys on the file name up to "_v", which is exactly the
/// deliverable identity the converter writes (TS_102_221_v18.4.0.html).
fn newest_per_deliverable(files: Vec<String>) -> Vec<String> {
    let mut best: HashMap<String, (u32, u32, u32, String)> = HashMap::new();
    for f in files {
        let name = std::path::Path::new(&f)
            .file_name()
            .and_then(|n| n.to_str())
            .unwrap_or_default()
            .to_string();
        let Some(idx) = name.rfind("_v") else {
            continue;
        };
        let key = name[..idx].to_string();
        let ver = name[idx + 2..].trim_end_matches(".html").to_string();
        let k = version_key(&ver);
        match best.get(&key) {
            Some((a, b, c, _)) if (*a, *b, *c) >= k => {}
            _ => {
                best.insert(key, (k.0, k.1, k.2, f));
            }
        }
    }
    let mut out: Vec<String> = best.into_values().map(|(_, _, _, f)| f).collect();
    out.sort();
    out
}

/// initials_match asks whether an expansion can actually BE what the term
/// abbreviates: the term's letters must appear, in order, as word initials of the
/// expansion. "IMSI" / "International Mobile Subscriber Identity" passes; "IMSI" /
/// "International Organization for Standardization" does not.
///
/// WHY THIS GUARD EXISTS, measured before it did. extract_acronyms pairs a line's
/// first token with the rest of the line, which is exactly right on the 3GPP side,
/// where the abbreviation list survives .doc conversion as a table. ETSI ships PDFs,
/// and `pdftotext -layout` prints a tall cell's expansion ABOVE its term:
///
/// ```text
/// AND   Boolean "and"
/// Conditional requirement (to be observed if the relevant conditions apply)
/// C     Digital Subscriber Signalling System No. one
/// DSS1  Information Elements Received
/// ```
///
/// From there every line pairs term N with expansion N+1, for the rest of the list.
/// Run without this guard over the whole ETSI corpus: 8 995 rows, of which 3 264
/// (36,3 %) carried an expansion belonging to another term — including
/// "IMSI = International Organization for Standardization". resolve_term would have
/// answered those with a straight face. A wrong definition is worse than a missing
/// one: it is an answer the caller cannot check.
///
/// The guard is conservative in the safe direction. It also drops syllabic
/// abbreviations that are genuinely correct — CAPEX/Capital Expenditure,
/// N/A/not supported — so the vocabulary it keeps is smaller than the truth and
/// never wider than it.
fn initials_match(term: &str, expansion: &str) -> bool {
    let letters: Vec<char> = term
        .chars()
        .filter(|c| c.is_alphabetic())
        .flat_map(|c| c.to_uppercase())
        .collect();
    if letters.is_empty() {
        return false;
    }
    let mut i = 0;
    for word in expansion.split(|c: char| !c.is_alphanumeric()) {
        let Some(first) = word.chars().next() else {
            continue;
        };
        if i < letters.len() && first.to_uppercase().next() == Some(letters[i]) {
            i += 1;
        }
    }
    i == letters.len()
}

/// A whole file whose columns are shifted produces rows that are individually
/// wrong, and a handful may still pass initials_match by coincidence. So a file has
/// to look self-consistent as a WHOLE before any of its rows are trusted: below this
/// share of passing candidates, the pairing itself is suspect and the file is
/// dropped entirely.
const MIN_FILE_CONSISTENCY: f64 = 0.6;

fn collect_html(root: &str) -> Result<Vec<String>> {
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
    Ok(out)
}

fn main() -> Result<()> {
    let args = Args::parse();
    let store = store_rs::Store::open_rw(&args.db)?;

    let files = newest_per_deliverable(collect_html(&args.convert)?);
    eprintln!(
        "ingest-glossary: {} deliverable(s) (newest version of each)",
        files.len()
    );

    let (mut specs, mut rows, mut written) = (0usize, 0usize, 0usize);
    let (mut candidates, mut dropped_files, mut dropped_rows) = (0usize, 0usize, 0usize);
    for (i, f) in files.iter().enumerate() {
        let html = parse3gpp::html_bytes::read_html(f).with_context(|| format!("read {f}"))?;
        let Some(meta) = parse3gpp::etsi::parse_etsi_meta(&html) else {
            continue; // not an ETSI deliverable: no provenance header
        };
        let (clauses, _, _) =
            parse3gpp::parse_html_clauses(&html, &meta.spec_id, &meta.release, &meta.version);
        // first/last carry the VERSION, not meta.release: an ETSI deliverable's
        // release is the constant "ETSI", so stamping it would put zero information
        // in a field the caller reads to know when a term applied.
        let acs = parse3gpp::glossary::extract_acronyms(&clauses, &meta.version);
        if acs.is_empty() {
            continue;
        }
        candidates += acs.len();
        let kept: Vec<_> = acs
            .iter()
            .filter(|a| initials_match(&a.term, &a.expansion))
            .collect();
        if (kept.len() as f64) < MIN_FILE_CONSISTENCY * (acs.len() as f64) {
            dropped_files += 1;
            dropped_rows += acs.len();
            continue;
        }
        dropped_rows += acs.len() - kept.len();
        specs += 1;
        rows += kept.len();
        for a in kept {
            store.upsert_acronym(
                &a.term,
                &a.expansion,
                "",
                &a.first_release,
                &a.last_release,
                &args.source_series,
            )?;
            written += 1;
        }
        if (i + 1) % 500 == 0 {
            eprintln!(
                "ingest-glossary: {}/{} file(s), {rows} row(s) so far",
                i + 1,
                files.len()
            );
        }
    }
    eprintln!(
        "ingest-glossary: {candidates} candidate row(s); dropped {dropped_rows}          (of which {dropped_files} whole file(s) whose columns did not line up);          kept {written} row(s) from {specs} deliverable(s)"
    );
    // A pass that writes nothing is a regression, not "no work": every ETSI TS/EN
    // carries clause 3. Fail loudly rather than leave resolve_term silently
    // 3GPP-only, which is the state this pass exists to end.
    if written == 0 {
        anyhow::bail!("no acronym was extracted from {} — the abbreviations heuristic or the corpus is broken", args.convert);
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    /// THE DEFECT THE GUARD EXISTS FOR, taken verbatim from EN 300 286-2 v1.2.4.
    /// `pdftotext -layout` prints a tall cell's expansion ABOVE its term, so from
    /// there every line pairs term N with expansion N+1 for the rest of the list.
    /// Measured over the whole ETSI corpus without the guard: 8 995 rows, 3 264
    /// (36,3 %) belonging to another term — "IMSI = International Organization for
    /// Standardization" among them.
    #[test]
    fn a_shifted_column_does_not_pass() {
        for (t, e) in [
            ("DSS1", "Information Elements Received"),
            ("IER", "Information Elements Transmitted"),
            ("ISDN", "Implementation Under Test"),
            ("IMSI", "International Organization for Standardization"),
        ] {
            assert!(!initials_match(t, e), "{t} / {e} must be refused");
        }
    }

    /// And it must not refuse the real thing, including a term carrying digits or
    /// punctuation, and an expansion with a parenthetical tail.
    #[test]
    fn a_real_abbreviation_passes() {
        for (t, e) in [
            ("IMSI", "International Mobile Subscriber Identity"),
            ("UICC", "Universal Integrated Circuit Card"),
            (
                "STM-1",
                "Synchronous Transport Module Level 1 (155,52 Mbit/s)",
            ),
            ("TD-CDMA", "Time Division Code Division Multiple Access"),
            ("BBERF", "Bearer Binding and Event Reporting Function"),
        ] {
            assert!(initials_match(t, e), "{t} / {e} must be kept");
        }
    }

    /// The guard is conservative in the SAFE direction and this pins that, so the
    /// loss is a decision on the record rather than a surprise: a syllabic
    /// abbreviation is correct and is still dropped.
    #[test]
    fn the_guard_is_known_to_be_conservative() {
        assert!(!initials_match("CAPEX", "Capital Expenditure"));
        assert!(!initials_match("N/A", "not supported"));
    }

    /// 18.10.0 is NEWER than 18.9.0, and a string sort says otherwise. Picking the
    /// wrong file would mine a stale vocabulary while looking entirely correct.
    #[test]
    fn newest_is_numeric_not_lexical() {
        let got = newest_per_deliverable(vec![
            "d/TS_102_221_v18.9.0.html".into(),
            "d/TS_102_221_v18.10.0.html".into(),
            "d/TS_102_221_v3.1.0.html".into(),
            "d/TR_103_101_v1.1.1.html".into(),
        ]);
        assert_eq!(
            got,
            vec![
                "d/TR_103_101_v1.1.1.html".to_string(),
                "d/TS_102_221_v18.10.0.html".to_string()
            ],
            "one file per deliverable, and the newest by NUMBER"
        );
    }
}
