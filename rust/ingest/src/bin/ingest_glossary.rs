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
//! WHAT IT COUNTS, AND WHY THE COUNT IS THE PRODUCT. 938 of the 3 042 terms in the
//! ETSI half carry more than one expansion, and ETSI states no precedence rule to
//! choose between them — so before this pass counted agreement, the first row a
//! caller read was the alphabetical one: MSC as "Main Service Channel", IMS as "IP
//! Multimdia Subsystem" (a typo), TS as a sentence about SimulCrypt. See `Tally`.
//!
//! Usage: ingest-glossary --convert <dir> --db <db>
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
}

// --source-series IS GONE, and its absence is the fix rather than a tidy-up.
//
// It stamped the constant "etsi" on all 4 941 rows: a value that names no
// document, so nothing could be cited and — because Store.ResolveTerm ranks on
// what source_series says — nothing could be ranked either. Every ETSI row landed
// in the same bucket and the tie-break decided the answer alphabetically. Each row
// now carries the deliverable that declares it ("ETSI TS 103 221-1"), which still
// tells the halves apart, since every ETSI id begins with "ETSI ".

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

/// What the corpus, taken as a whole, says about one (term, expansion).
///
/// AGREEMENT IS THE ONLY AUTHORITY ETSI OFFERS. 3GPP ranks a term by WHO declares
/// it — TS 23.501 §3.2: "An abbreviation defined in the present document takes
/// precedence over the definition of the same abbreviation, if any, in TR 21.905"
/// — and Store.ResolveTerm implements exactly that. ETSI publishes no TR 21.905
/// and states no precedence: 5 142 deliverables each declare their own vocabulary
/// and none outranks another in general. So the honest signal is how many of them
/// say the same thing, and it has to be counted HERE, because the primary key
/// keeps one row per expansion — after the write, the corpus can no longer tell
/// forty declarations from one.
struct Consensus {
    /// How many DELIVERABLES declare this exact expansion.
    declared_by: i64,
    /// The deliverable cited on the row, and its version.
    ///
    /// The lexicographically smallest declaring id, which is a REPRODUCIBILITY
    /// rule and not a claim of authority: the same corpus must yield the same row
    /// on every run, and "whichever file the walker reached last" does not. The
    /// count is what says how many others agree.
    cite_spec: String,
    cite_version: String,
}

/// Tally folds each deliverable's kept acronyms into the corpus-wide consensus.
///
/// A BTreeMap, so the write order is the corpus's own order rather than a hash
/// seed's: an idempotent pass has to produce the same rows in the same sequence,
/// or "nothing changed" cannot be told from "everything moved".
#[derive(Default)]
struct Tally {
    rows: std::collections::BTreeMap<(String, String), Consensus>,
}

impl Tally {
    /// add_file folds ONE deliverable in.
    ///
    /// DISTINCT PER DELIVERABLE. An Abbreviations clause can list the same pair
    /// twice — ETSI TS 102 221 lists TC under both its Symbols and Abbreviations
    /// headings — and counting both would let one document outvote two.
    fn add_file(&mut self, spec_id: &str, version: &str, acs: &[(String, String)]) {
        let mut seen = std::collections::HashSet::new();
        for (term, expansion) in acs {
            if !seen.insert((term.as_str(), expansion.as_str())) {
                continue;
            }
            let e = self
                .rows
                .entry((term.clone(), expansion.clone()))
                .or_insert_with(|| Consensus {
                    declared_by: 0,
                    cite_spec: spec_id.to_string(),
                    cite_version: version.to_string(),
                });
            e.declared_by += 1;
            if spec_id < e.cite_spec.as_str() {
                e.cite_spec = spec_id.to_string();
                e.cite_version = version.to_string();
            }
        }
    }
}

fn main() -> Result<()> {
    let args = Args::parse();
    let store = store_rs::Store::open_rw(&args.db)?;

    let files = newest_per_deliverable(collect_html(&args.convert)?);
    eprintln!(
        "ingest-glossary: {} deliverable(s) (newest version of each)",
        files.len()
    );

    let (mut specs, mut rows) = (0usize, 0usize);
    let (mut candidates, mut dropped_files, mut dropped_rows) = (0usize, 0usize, 0usize);
    let mut tally = Tally::default();
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
        let kept: Vec<(String, String)> = acs
            .iter()
            .filter(|a| initials_match(&a.term, &a.expansion))
            .map(|a| (a.term.clone(), a.expansion.clone()))
            .collect();
        if (kept.len() as f64) < MIN_FILE_CONSISTENCY * (acs.len() as f64) {
            dropped_files += 1;
            dropped_rows += acs.len();
            continue;
        }
        dropped_rows += acs.len() - kept.len();
        specs += 1;
        rows += kept.len();
        tally.add_file(&meta.spec_id, &meta.version, &kept);
        if (i + 1) % 500 == 0 {
            eprintln!(
                "ingest-glossary: {}/{} file(s), {rows} row(s) so far",
                i + 1,
                files.len()
            );
        }
    }

    // THE WRITE IS THE SECOND PASS, and it is what makes this idempotent.
    //
    // Row-at-a-time writing could not carry the count, and it could not carry an
    // honest citation either: the same (term, expansion) is upserted by every
    // deliverable that declares it, so source_series ended up naming whichever
    // file the walker happened to reach last. That was hidden while every row was
    // stamped with the constant "etsi" — a value that names no document, cites
    // nothing, and left the ETSI half unrankable.
    // AND IT REPLACES RATHER THAN ADDS. The pass reads the newest version of every
    // deliverable, so an expansion a later version corrected — or dropped — kept
    // its row for ever beside the current one and stayed visible through
    // resolve_term. The vocabulary is a snapshot of what the archive says now, and
    // an additive writer cannot express that. The scope is the provenance itself,
    // which is why this could not have been written while every row said "etsi".
    let mined: Vec<store_rs::MinedAcronym> = tally
        .rows
        .iter()
        .map(|((term, expansion), c)| store_rs::MinedAcronym {
            term: term.clone(),
            expansion: expansion.clone(),
            version: c.cite_version.clone(),
            source: c.cite_spec.clone(),
            declared_by: c.declared_by,
        })
        .collect();
    let written = store.replace_mined_acronyms(&mined)?;

    let agreed = tally.rows.values().filter(|c| c.declared_by > 1).count();
    eprintln!(
        "ingest-glossary: {candidates} candidate row(s); dropped {dropped_rows} \
         (of which {dropped_files} whole file(s) whose columns did not line up); \
         kept {rows} declaration(s) from {specs} deliverable(s) -> {written} row(s), \
         {agreed} of them declared by more than one deliverable"
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

    fn decls(pairs: &[(&str, &str)]) -> Vec<(String, String)> {
        pairs
            .iter()
            .map(|(t, e)| (t.to_string(), e.to_string()))
            .collect()
    }

    /// THE ANSWER THE ETSI HALF USED TO GIVE, and why counting changes it.
    ///
    /// Measured on the shipped corpus 2026-09-07: MSC carries twelve expansions
    /// and Store.ResolveTerm ordered them `domain, expansion` with domain empty on
    /// every ETSI row — so the first row a caller read was "Main Service Channel",
    /// alphabetically first and, for anyone asking about a mobile network, wrong.
    /// The switching centre is what the archive overwhelmingly declares, and that
    /// is a countable fact rather than a preference.
    #[test]
    fn the_expansion_most_deliverables_declare_wins_over_the_alphabet() {
        let mut t = Tally::default();
        for spec in ["ETSI TS 101 200", "ETSI TS 102 221", "ETSI TS 103 221-1"] {
            t.add_file(
                spec,
                "1.1.1",
                &decls(&[("MSC", "Mobile-services Switching Centre")]),
            );
        }
        t.add_file(
            "ETSI EN 300 175-1",
            "2.7.1",
            &decls(&[("MSC", "Main Service Channel")]),
        );

        let switching = &t.rows[&("MSC".into(), "Mobile-services Switching Centre".into())];
        let channel = &t.rows[&("MSC".into(), "Main Service Channel".into())];
        assert_eq!(switching.declared_by, 3);
        assert_eq!(channel.declared_by, 1);
        // BOTH ARE KEPT. Ranking is not filtering: "Main Service Channel" is a real
        // DECT term and the corpus reproduces its sources rather than choosing for
        // them. What changes is which one a caller reads first.
        assert_eq!(t.rows.len(), 2);
    }

    /// ONE DELIVERABLE, ONE VOTE. An Abbreviations clause can list the same pair
    /// twice — a Symbols heading and an Abbreviations heading in the same region —
    /// and counting both would let one document outvote two.
    #[test]
    fn a_pair_listed_twice_in_one_file_counts_once() {
        let mut t = Tally::default();
        t.add_file(
            "ETSI TS 102 221",
            "18.4.0",
            &decls(&[
                ("TC", "Transmission Convergence"),
                ("TC", "Transmission Convergence"),
            ]),
        );
        assert_eq!(
            t.rows[&("TC".into(), "Transmission Convergence".into())].declared_by,
            1
        );
    }

    /// THE CITATION IS REPRODUCIBLE, and that is all it claims.
    ///
    /// The row names the lexicographically smallest declaring deliverable, with
    /// its version, because the same corpus must produce the same row on every
    /// run — the previous pass stamped whichever file the walker reached last,
    /// under a constant "etsi" that hid it. It is not a claim that this
    /// deliverable is more authoritative; `declared_by` is what says how many
    /// others agree.
    #[test]
    fn the_cited_deliverable_does_not_depend_on_walk_order() {
        let files: [(&str, &str); 3] = [
            ("ETSI TS 103 221-1", "1.12.1"),
            ("ETSI TS 102 221", "18.4.0"),
            ("ETSI EN 300 175-1", "2.7.1"),
        ];
        let mut forward = Tally::default();
        for (spec, ver) in files {
            forward.add_file(spec, ver, &decls(&[("AID", "Application IDentifier")]));
        }
        let mut backward = Tally::default();
        for (spec, ver) in files.iter().rev() {
            backward.add_file(spec, ver, &decls(&[("AID", "Application IDentifier")]));
        }

        let key = ("AID".to_string(), "Application IDentifier".to_string());
        assert_eq!(forward.rows[&key].cite_spec, "ETSI EN 300 175-1");
        assert_eq!(forward.rows[&key].cite_version, "2.7.1");
        assert_eq!(backward.rows[&key].cite_spec, forward.rows[&key].cite_spec);
        assert_eq!(
            backward.rows[&key].cite_version, forward.rows[&key].cite_version,
            "the version must travel with the deliverable it cites, not with the last file read"
        );
        assert_eq!(forward.rows[&key].declared_by, 3);
    }

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
