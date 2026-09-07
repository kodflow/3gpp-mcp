//! glossary — Rust port of internal/subject/glossary (Phase 8). The reference domain
//! subject: it extracts the acronym vocabulary from TS 21.905's abbreviations region
//! (run during the ingest of that spec) into model.Acronym rows the generic resolve_term
//! tool serves. A TAB / 2+-space separator keeps prose out; cite-or-silent.

use crate::ParsedClause;
use regex::Regex;
use std::sync::OnceLock;

/// The 3GPP vocabulary spec the glossary owns.
pub const GLOSSARY_SPEC_ID: &str = "21.905";

/// An acronym row (== Go model.Acronym; domain is "" for the general glossary).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Acronym {
    pub term: String,
    pub expansion: String,
    pub first_release: String,
    pub last_release: String,
    pub source_series: String,
}

fn re_abbrev() -> &'static Regex {
    static R: OnceLock<Regex> = OnceLock::new();
    // "ABBR<TAB|2+ spaces>Expansion" — the separator keeps "This document defines …" out.
    R.get_or_init(|| {
        Regex::new(r"^([A-Za-z0-9][A-Za-z0-9._/-]{0,19})(?:\t+|\s{2,})(\S.{2,})$").unwrap()
    })
}

fn is_all_digits(s: &str) -> bool {
    !s.is_empty() && s.bytes().all(|b| b.is_ascii_digit())
}

/// is_descendant asks whether `path` sits UNDER `root` in the clause numbering:
/// "3.2.1" is under "3.2", "3.20" is not, and neither is "4".
///
/// The prefix test alone gets "3.20" wrong, which is why the separator is checked:
/// a descendant continues with a dot.
fn is_descendant(root: &str, path: &str) -> bool {
    path.len() > root.len() && path.starts_with(root) && path[root.len()..].starts_with('.')
}

/// extract_acronyms scans the abbreviations region of a document's clauses (== Go
/// glossary.Ingest). `release` stamps first/last_release; source_series is the owning
/// series ("21") so the incremental merge can scope-purge these rows.
///
/// EVERY MATCHING HEADING, AND ITS SUB-CLAUSES — not the first match and nothing
/// else. That earlier rule anchored on the FIRST heading whose text mentions
/// abbreviations and then stopped at the next clause whose path begins with a
/// digit, which is every numbered sibling AND every sub-clause. In the shape ETSI
/// and 3GPP both use most of the time:
///
///     3    Definitions and abbreviations   <- matched here
///     3.1  Definitions                     <- numeric path: STOPPED here
///     3.2  Abbreviations                   <- the actual list, never read
///
/// the region was the parent heading alone, which holds no abbreviation lines. The
/// list two clauses down was never reached, and the pass reported success over it.
///
/// MEASURED on the converted ETSI archive, 2026-09-07: 4 938 of the 5 142
/// deliverables carry a heading that mentions abbreviations, and the old rule
/// extracted from 402 of them — 8 %. The ones that worked were exactly those whose
/// PARENT heading omits the word ("3 Definitions" + "3.2 Abbreviations"), so the
/// first match happened to be the list itself. It was working by accident of
/// wording.
///
/// Walking the sub-clauses of a matched parent pulls "3.1 Definitions" into the
/// region too, and that is safe rather than merely tolerable: the separator rule
/// (TAB or 2+ spaces) is what keeps prose out, and a definition line is
/// "term: prose" with single spaces. Measured on TS 102 221 v18.4.0 when this
/// extractor was written: ZERO rows from the prose of 3.1.
pub fn extract_acronyms(clauses: &[ParsedClause], release: &str) -> Vec<Acronym> {
    // The regions to read: each clause whose heading mentions abbreviations, plus
    // everything numbered under it. A document can have more than one (a Symbols
    // clause and an Abbreviations clause under the same parent), and the rows are
    // deduplicated by the caller's primary key anyway.
    let mut in_region = vec![false; clauses.len()];
    for (i, c) in clauses.iter().enumerate() {
        if !c.heading.to_lowercase().contains("abbreviation") {
            continue;
        }
        in_region[i] = true;
        let root = c.clause_path.clone();
        // An unnumbered or annex heading owns only itself: there is no numbering to
        // walk, and consuming the rest of the document from there is how a single
        // stray heading could swallow a whole specification.
        if root.is_empty() || root.starts_with("Annex") {
            continue;
        }
        for (j, d) in clauses.iter().enumerate().skip(i + 1) {
            if !is_descendant(&root, &d.clause_path) {
                break;
            }
            in_region[j] = true;
        }
    }
    let mut out = Vec::new();
    for (i, c) in clauses.iter().enumerate() {
        if !in_region[i] {
            continue;
        }
        for line in c.text.split('\n') {
            let line = line.trim_end_matches([' ', '\t']);
            let Some(m) = re_abbrev().captures(line) else {
                continue;
            };
            let term = m[1].trim().to_string();
            let exp = m[2].trim().replace('\t', " ");
            if exp.len() < 4 || term.eq_ignore_ascii_case(&exp) || is_all_digits(&exp) {
                continue;
            }
            out.push(Acronym {
                term,
                expansion: exp,
                first_release: release.to_string(),
                last_release: release.to_string(),
                source_series: GLOSSARY_SPEC_ID[..2].to_string(),
            });
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    fn clause(path: &str, heading: &str, text: &str) -> ParsedClause {
        ParsedClause {
            chunk_id: 0,
            clause_path: path.into(),
            heading: heading.into(),
            text: text.into(),
            is_normative: true,
        }
    }

    #[test]
    fn extracts_abbreviations_until_next_section() {
        let clauses = vec![
            clause("3.1", "Definitions", "some prose that is not an acronym line"),
            clause("3.2", "Abbreviations", "AMF\tAccess and Mobility Management Function\nSMF  Session Management Function\nThis document is not an entry"),
            clause("4", "Architecture", "AAA\tShould not be captured (left the region)"),
        ];
        let ac = extract_acronyms(&clauses, "Rel-19");
        assert_eq!(ac.len(), 2, "{ac:?}");
        assert_eq!(ac[0].term, "AMF");
        assert_eq!(ac[0].expansion, "Access and Mobility Management Function");
        assert_eq!(ac[0].first_release, "Rel-19");
        assert_eq!(ac[0].source_series, "21");
        assert_eq!(ac[1].term, "SMF");
    }

    #[test]
    fn no_abbreviations_heading_yields_nothing() {
        let clauses = vec![clause("1", "Scope", "AMF\tnot in an abbreviations clause")];
        assert!(extract_acronyms(&clauses, "Rel-19").is_empty());
    }

    /// THE SHAPE THAT SILENTLY YIELDED NOTHING, taken verbatim from
    /// ETSI EN 300 299 v1.3.2 and used by most of both archives.
    ///
    /// The PARENT heading mentions abbreviations, so the old rule anchored there —
    /// and then stopped at "3.1", because its path starts with a digit. The list in
    /// 3.2 was never read, and the pass reported success. Measured over the
    /// converted ETSI archive on 2026-09-07: 4 938 of 5 142 deliverables carry such
    /// a heading and only 402 yielded a row.
    #[test]
    fn a_parent_heading_does_not_hide_the_list_below_it() {
        let clauses = vec![
            clause(
                "3",
                "Definitions and abbreviations",
                "For the purposes of the present document, the following apply:",
            ),
            clause(
                "3.1",
                "Definitions",
                "access conditions: set of security attributes associated with a file",
            ),
            clause(
                "3.2",
                "Abbreviations",
                "UICC\tUniversal Integrated Circuit Card\nADF  Application Dedicated File",
            ),
            clause(
                "4",
                "Requirements",
                "AAA\tShould not be captured (a sibling, not a sub-clause)",
            ),
        ];
        let ac = extract_acronyms(&clauses, "1.3.2");
        let terms: Vec<&str> = ac.iter().map(|a| a.term.as_str()).collect();
        assert_eq!(terms, vec!["UICC", "ADF"], "{ac:?}");
    }

    /// The sibling boundary is a PREFIX plus a dot, not a prefix. "3.20" is not
    /// under "3.2", and reading it as such would let a later clause's prose into a
    /// region the separator rule was never asked to defend.
    #[test]
    fn a_longer_number_is_not_a_sub_clause() {
        let clauses = vec![
            clause(
                "3.2",
                "Abbreviations",
                "UICC\tUniversal Integrated Circuit Card",
            ),
            clause("3.20", "Something else", "AAA\tShould not be captured"),
        ];
        let ac = extract_acronyms(&clauses, "1.0.0");
        let terms: Vec<&str> = ac.iter().map(|a| a.term.as_str()).collect();
        assert_eq!(terms, vec!["UICC"]);
    }

    /// An unnumbered or annex heading owns ITSELF and nothing after it: there is no
    /// numbering to walk, so consuming the rest of the document from there is how
    /// one stray heading swallows a whole specification.
    #[test]
    fn an_annex_heading_does_not_swallow_the_document() {
        let clauses = vec![
            clause(
                "Annex A",
                "Abbreviations",
                "UICC\tUniversal Integrated Circuit Card",
            ),
            clause("Annex B", "Test vectors", "AAA\tShould not be captured"),
        ];
        let ac = extract_acronyms(&clauses, "1.0.0");
        let terms: Vec<&str> = ac.iter().map(|a| a.term.as_str()).collect();
        assert_eq!(terms, vec!["UICC"]);
    }

    /// Two matching headings under one parent — a Symbols clause and an
    /// Abbreviations clause — are BOTH read. Anchoring on the first match alone
    /// dropped whichever came second.
    #[test]
    fn every_matching_heading_is_read_not_just_the_first() {
        let clauses = vec![
            clause("3", "Symbols and abbreviations", ""),
            clause("3.1", "Symbols", "Fi\tclock rate conversion factor"),
            clause(
                "3.2",
                "Abbreviations",
                "APDU\tApplication Protocol Data Unit",
            ),
        ];
        let ac = extract_acronyms(&clauses, "1.0.0");
        let terms: Vec<&str> = ac.iter().map(|a| a.term.as_str()).collect();
        assert_eq!(terms, vec!["Fi", "APDU"]);
    }
}
