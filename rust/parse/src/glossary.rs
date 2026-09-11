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
/// ```text
/// 3    Definitions and abbreviations   <- matched here
/// 3.1  Definitions                     <- numeric path: STOPPED here
/// 3.2  Abbreviations                   <- the actual list, never read
/// ```
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
///
/// AN UNNUMBERED CLAUSE CONTINUES THE REGION; only a numbered clause that is not
/// under the heading ends it. That is the shape of TS 21.905 itself, the document
/// this extractor was written for, in every one of the 16 versions the corpus
/// holds (Rel-4 to Rel-19):
///
/// ```text
/// 4    Abbreviations     <- matched here, EMPTY body
///      0-9               <- clause_path "": the list, letter by letter
///      A … Z             <- clause_path ""
/// 5    Equations         <- numbered, not under 4: the region ends
/// ```
///
/// The descendant rule above, as first written, walked only NUMBERED
/// sub-clauses: "" is not a descendant of "4", so the walk stopped at "0-9" and
/// the region was the empty heading. Run on the converted v19.2.0 and v10.3.0 on
/// 2026-09-11, it returned ZERO rows where the rule before it returned 1 300 and
/// 1 255 distinct keys — the 1 300 being exactly the 404 rows the corpus stamps
/// "21" plus the 896 a spec has since taken over. The corpus kept them only
/// because `ingest --resume` never parses an ingested version again; the next
/// TS 21.905 to arrive, or a corpus built from nothing, would have been written
/// without a vocabulary. glossaryseed's generalRegion (Go) reads TS 21.905 with
/// this same region rule, and the two must not disagree about what the region is.
///
/// It changes nothing on the ETSI side, and that was measured rather than
/// assumed: the newest version of each of the 5 142 deliverables, extracted with
/// and without the continuation, gives the same 159 475 rows in every file.
pub fn extract_acronyms(clauses: &[ParsedClause], release: &str) -> Vec<Acronym> {
    // The regions to read: each clause whose heading mentions abbreviations, plus
    // what follows it up to the first numbered clause NOT under it — its numbered
    // sub-clauses and any unnumbered ones. A document can have more than one (a Symbols
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
            // Unnumbered (TS 21.905's letter clauses) or numbered under the root.
            if !d.clause_path.is_empty() && !is_descendant(&root, &d.clause_path) {
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

    /// TS 21.905 v19.2.0 AS LIBREOFFICE CONVERTED IT — the lines below are copied
    /// from data/sources/convert/Rel-19/21905-j20.html, tabs included, with each
    /// letter's list cut short. Clause 4 has an empty body and its list sits in
    /// UNNUMBERED letter clauses, the shape of all 16 stored versions. The rule
    /// that read numbered sub-clauses only stopped at "0-9" and returned nothing
    /// for the whole document (0 rows on v19.2.0, where 1 300 keys are printed).
    ///
    /// Through the real parser rather than hand-built clauses, so that the test
    /// also holds the shape itself: if the walker ever numbered these letters the
    /// region would change for a reason this test would name.
    const TS21905_V19_2_0: &str = r#"<html><body>
<h1 class="western"><a name="_Toc232691870"></a>3	Terms and
definitions</h1>
<h2 class="western"><a name="_Toc232691871"></a><a name="_Toc11152815"></a>
0-9</h2>
<p style="margin-bottom: 0.32cm; margin-left: 4cm; text-indent: -4cm">
<b>1.8V technology Smart Card:</b> A Smart Card operating at 1.8V ±
10% and 3V ± 10%.</p>
<h2 class="western"><a name="_Toc232691897"></a><a name="_Toc11152841"></a>
<span lang="fr-FR">Z</span></h2>
<p style="margin-bottom: 0.32cm"><span lang="fr-FR">&lt;void&gt;</span></p>
<h1 class="western"><a name="_Toc232691898"></a><a name="_Toc11152842"></a>
4	Abbreviations</h1>
<h2 class="western"><a name="_Toc232691899"></a><a name="_Toc11152843"></a>
0-9</h2>
<p style="page-break-inside: avoid; text-indent: -2.5cm; margin-left: 3cm; margin-bottom: 0cm">
2G	2<sup>nd</sup> Generation</p>
<p style="page-break-inside: avoid; text-indent: -2.5cm; margin-left: 3cm; margin-bottom: 0cm">
3GPP	Third Generation Partnership Project</p>
<p style="page-break-inside: avoid; text-indent: -2.5cm; margin-left: 3cm; margin-bottom: 0cm">
5GC	Fifth Generation Core network</p>
<h2 class="western"><a name="_Toc232691900"></a><a name="_Toc11152844"></a>
A</h2>
<p style="page-break-inside: avoid; text-indent: -2.5cm; margin-left: 3cm; margin-bottom: 0cm">
A-SGW	Access Signalling Gateway</p>
<p style="page-break-inside: avoid; text-indent: -2.5cm; margin-left: 3cm; margin-bottom: 0cm">
AC	Access Class (C0 to C15)</p>
<p style="page-break-inside: avoid; text-indent: -2.5cm; margin-left: 3cm; margin-bottom: 0cm">
	Access Condition</p>
<p style="page-break-inside: avoid; text-indent: -2.5cm; margin-left: 3cm; margin-bottom: 0cm">
ACC	Automatic Congestion Control</p>
<h2 class="western"><a name="_Toc232691925"></a><a name="_Toc11152869"></a>
<span lang="fr-FR">Z</span></h2>
<p style="page-break-inside: avoid; text-indent: -2.5cm; margin-left: 3cm; margin-bottom: 0cm">
<span lang="fr-FR">ZC	Zone Code </span>
</p>
<h1 class="western"><a name="_Toc232691926"></a><a name="_Toc11152870"></a>
5	Equations</h1>
<p align="left" style="page-break-inside: avoid; page-break-after: auto">
<font face="Arial, sans-serif"><span style="font-weight: normal">The
ratio of the received energy per PN chip of the CPICH to the total
transmit power spectral density at the Node_B (SS) antenna
connector.</span></font></p>
</body></html>"#;

    #[test]
    fn ts_21905_reads_its_unnumbered_letter_clauses() {
        let (clauses, _, _) =
            crate::parse_html_clauses(TS21905_V19_2_0, GLOSSARY_SPEC_ID, "Rel-19", "19.2.0");
        let ac = extract_acronyms(&clauses, "Rel-19");
        let got: Vec<(&str, &str)> = ac
            .iter()
            .map(|a| (a.term.as_str(), a.expansion.as_str()))
            .collect();
        // Exactly the keys the corpus holds for these lines (stamped "21", or
        // taken over by a spec): the continuation line "\tAccess Condition" has
        // no term and stays out, as it always has.
        assert_eq!(
            got,
            vec![
                ("2G", "2nd Generation"),
                ("3GPP", "Third Generation Partnership Project"),
                ("5GC", "Fifth Generation Core network"),
                ("A-SGW", "Access Signalling Gateway"),
                ("AC", "Access Class (C0 to C15)"),
                ("ACC", "Automatic Congestion Control"),
                ("ZC", "Zone Code"),
            ],
            "clauses: {:?}",
            clauses
                .iter()
                .map(|c| (c.clause_path.as_str(), c.heading.as_str()))
                .collect::<Vec<_>>()
        );
        assert!(ac
            .iter()
            .all(|a| a.source_series == "21" && a.first_release == "Rel-19"));
    }

    /// The continuation is for clauses with NO number. A numbered clause that is
    /// not under the heading still ends the region, and what comes after it —
    /// numbered or not — belongs to that clause, not to the list.
    #[test]
    fn a_numbered_sibling_still_ends_the_region() {
        let clauses = vec![
            clause("4", "Abbreviations", ""),
            clause("", "A", "AMF\tAccess and Mobility Management Function"),
            clause("4.1", "Sub-clause", "SMF\tSession Management Function"),
            clause("5", "Equations", ""),
            clause(
                "",
                "Unrelated",
                "AAA\tShould not be captured (after clause 5)",
            ),
        ];
        let ac = extract_acronyms(&clauses, "Rel-19");
        let terms: Vec<&str> = ac.iter().map(|a| a.term.as_str()).collect();
        assert_eq!(terms, vec!["AMF", "SMF"]);
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
