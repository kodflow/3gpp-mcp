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

/// extract_acronyms scans the abbreviations region of TS 21.905's clauses (== Go
/// glossary.Ingest). `release` stamps first/last_release; source_series is the owning
/// series ("21") so the incremental merge can scope-purge these rows.
pub fn extract_acronyms(clauses: &[ParsedClause], release: &str) -> Vec<Acronym> {
    let start = clauses
        .iter()
        .position(|c| c.heading.to_lowercase().contains("abbreviation"));
    let Some(start) = start else {
        return Vec::new();
    };
    let mut out = Vec::new();
    for (i, c) in clauses.iter().enumerate().skip(start) {
        if i > start
            && (c.clause_path.starts_with(|ch: char| ch.is_ascii_digit())
                || c.clause_path.starts_with("Annex"))
        {
            break; // left the abbreviations region
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
}
