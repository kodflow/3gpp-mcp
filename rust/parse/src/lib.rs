//! parse3gpp — Rust port of the 3GPP spec parser (internal/htmlparse + internal/model
//! helpers), Phase 4 of the write-side migration. This first increment is the
//! PARITY-CRITICAL filename→metadata derivation: a wrong version-code decode mis-files a
//! spec under the wrong release and `discover` then re-flags that (spec, release) key
//! forever, so every function here mirrors its Go counterpart exactly and is golden-tested
//! against known Go outputs. The HTML clause walker lands on top of this seam next.

pub mod asn1;
pub mod catalog;
pub mod etsi;
pub mod glossary;
pub mod openapi;

use regex::Regex;

/// Metadata derived from a `…/convert/<Rel>/<num>-<code>.html` path — the spec catalogue
/// row + its version row, ready for store-rs upsert_spec / upsert_version.
#[derive(Debug, PartialEq, Eq)]
pub struct SpecMeta {
    pub spec_id: String,
    pub series: String,
    pub doc_type: String, // "TS" here; refined by the HTML title pass later
    pub working_group: String,
    pub release: String,
    pub version: String,
    pub docx_url: String,
}

/// decode_char maps one version-code character to its value (== Go decodeChar):
/// '0'..'9' → 0..9, 'a'..'z'/'A'..'Z' → 10.. . Returns None for anything else.
fn decode_char(c: u8) -> Option<i32> {
    match c {
        b'0'..=b'9' => Some((c - b'0') as i32),
        b'a'..=b'z' => Some(10 + (c - b'a') as i32),
        b'A'..=b'Z' => Some(10 + (c - b'A') as i32),
        _ => None,
    }
}

/// encode_char is the inverse of decode_char for 0..=35 (== Go encodeChar).
fn encode_char(n: i32) -> Option<u8> {
    match n {
        0..=9 => Some(b'0' + n as u8),
        10..=35 => Some(b'a' + (n - 10) as u8),
        _ => None,
    }
}

/// release_from_major maps a version major to its release label (== Go ReleaseFromMajor):
/// 3 → "Rel-99" (there is no Rel-98; the count jumps to Rel-4), else "Rel-<major>".
pub fn release_from_major(major: i32) -> String {
    if major == 3 {
        "Rel-99".to_string()
    } else {
        format!("Rel-{major}")
    }
}

/// decode_version_code turns a 3-char compact code ("i60" → 18.6.0) or a 6-digit decimal
/// code ("083700" → 8.37.0) into (release, version). None when malformed (== Go
/// DecodeVersionCode).
pub fn decode_version_code(code: &str) -> Option<(String, String)> {
    let b = code.as_bytes();
    match b.len() {
        3 => {
            let maj = decode_char(b[0])?;
            let v2 = decode_char(b[1])?;
            let v3 = decode_char(b[2])?;
            Some((release_from_major(maj), format!("{maj}.{v2}.{v3}")))
        }
        6 => {
            let maj: i32 = code[0..2].parse().ok()?;
            let v2: i32 = code[2..4].parse().ok()?;
            let v3: i32 = code[4..6].parse().ok()?;
            Some((release_from_major(maj), format!("{maj}.{v2}.{v3}")))
        }
        _ => None,
    }
}

/// encode_version_code is the inverse of decode_version_code for the compact form
/// ("18.6.0" → "i60"), used to rebuild the archive URL (== Go EncodeVersionCode).
pub fn encode_version_code(version: &str) -> Option<String> {
    let parts: Vec<&str> = version.split('.').collect();
    if parts.len() != 3 {
        return None;
    }
    let mut out = [0u8; 3];
    for (i, p) in parts.iter().enumerate() {
        let n: i32 = p.parse().ok()?;
        out[i] = encode_char(n)?;
    }
    String::from_utf8(out.to_vec()).ok()
}

/// series_of returns the 2-digit series before the dot ("33.128" → "33") (== Go SeriesOf).
pub fn series_of(spec_id: &str) -> &str {
    match spec_id.find('.') {
        Some(i) if i > 0 => &spec_id[..i],
        _ => "",
    }
}

/// working_group_for_series maps a series to its owning WG, "" if unknown (== Go seriesWG).
pub fn working_group_for_series(series: &str) -> &'static str {
    match series {
        "21" => "SA",
        "22" => "SA1",
        "23" => "SA2",
        "24" => "CT1",
        "25" => "RAN",
        "26" => "SA4",
        "27" => "CT",
        "28" => "SA5",
        "29" => "CT3",
        "31" => "CT6",
        "32" => "SA5",
        "33" => "SA3",
        "35" => "SA3",
        "36" => "RAN",
        "37" => "RAN",
        "38" => "RAN",
        _ => "",
    }
}

/// archive_url reconstructs the canonical 3GPP archive ZIP URL (== Go ArchiveURL).
pub fn archive_url(spec_id: &str, version: &str) -> String {
    let series = series_of(spec_id);
    let num = spec_id.replace('.', "");
    match encode_version_code(version) {
        Some(code) if !series.is_empty() && !num.is_empty() => format!(
            "https://www.3gpp.org/ftp/Specs/archive/{series}_series/{spec_id}/{num}-{code}.zip"
        ),
        _ => String::new(),
    }
}

/// parse_filename_meta derives the spec/version metadata from a 3GPP convert path
/// ("…/convert/<Rel>/<num>-<code>.html") (== Go metaFromFilename). The release is the
/// AUTHORITATIVE convert-dir release when present, falling back to the version-major decode
/// (a draft v1.x of a Rel-20 spec lives under Rel-20, not Rel-1).
pub fn parse_filename_meta(path: &str) -> Result<SpecMeta, String> {
    let re_file = Regex::new(r"^([0-9]{4,5}(?:-[0-9]+)?)-([0-9a-z]{3}|[0-9]{6})(?:_.*)?$").unwrap();
    let re_release_dir = Regex::new(r"^(Rel-[0-9]+|GSM|Phase[0-9]+)$").unwrap();

    let p = std::path::Path::new(path);
    let base = p
        .file_stem()
        .and_then(|s| s.to_str())
        .ok_or_else(|| format!("no file stem in {path:?}"))?;

    let caps = re_file
        .captures(base)
        .ok_or_else(|| format!("filename {base:?} is not <4-5 digits>-<3code>"))?;
    let num = &caps[1];
    let code = &caps[2];
    let spec_id = format!("{}.{}", &num[..2], &num[2..]);
    let (mut release, version) = decode_version_code(code)
        .ok_or_else(|| format!("bad version code {code:?} in {base:?}"))?;

    if let Some(dir) = p
        .parent()
        .and_then(|d| d.file_name())
        .and_then(|d| d.to_str())
    {
        if re_release_dir.is_match(dir) {
            release = dir.to_string();
        }
    }

    let series = series_of(&spec_id).to_string();
    let working_group = working_group_for_series(&series).to_string();
    let docx_url = archive_url(&spec_id, &version);
    Ok(SpecMeta {
        spec_id,
        series,
        doc_type: "TS".to_string(),
        working_group,
        release,
        version,
        docx_url,
    })
}

// ===========================================================================
//  HTML clause walker (== internal/htmlparse walker) — document-order traversal
//  that segments a LibreOffice-converted spec into clause-leaf chunks. chunk_id is
//  a per-spec sequential counter (1-based, one per heading) — the SAME order Go
//  assigns, so the ingest's global offset yields identical ids (resume/merge
//  stability hinges on this). Change-History sections are excluded from clauses
//  here exactly as in Go (their structured CR extraction is a later increment).
// ===========================================================================

use std::sync::OnceLock;

/// A clause-leaf chunk emitted by the walker (chunk_id is per-spec sequential).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ParsedClause {
    pub chunk_id: u64,
    pub clause_path: String,
    pub heading: String,
    pub text: String,
    pub is_normative: bool,
}

fn re_ws() -> &'static Regex {
    static R: OnceLock<Regex> = OnceLock::new();
    R.get_or_init(|| Regex::new(r"[ \t\x{00a0}]+").unwrap())
}
fn re_numeric() -> &'static Regex {
    static R: OnceLock<Regex> = OnceLock::new();
    R.get_or_init(|| Regex::new(r"^([0-9]+(?:\.[0-9A-Za-z]+)*)\s+(.+)$").unwrap())
}
fn re_annex_sub() -> &'static Regex {
    static R: OnceLock<Regex> = OnceLock::new();
    R.get_or_init(|| Regex::new(r"^([A-Z]\.[0-9]+(?:\.[0-9A-Za-z]+)*)\s+(.+)$").unwrap())
}
fn re_annex() -> &'static Regex {
    static R: OnceLock<Regex> = OnceLock::new();
    R.get_or_init(|| Regex::new(r"(?i)^Annex\s+([A-Z][0-9]*)\s*[:.)]?\s*(.*)$").unwrap())
}

/// collapse normalises whitespace exactly like Go's collapse: NBSP→space, runs of
/// space/tab/NBSP→one space, newlines→space, trimmed.
fn collapse(s: &str) -> String {
    let s = s.replace('\u{00a0}', " ");
    let s = re_ws().replace_all(&s, " ");
    s.replace('\n', " ").trim().to_string()
}

fn is_change_history(text: &str) -> bool {
    let t = collapse(text).to_lowercase();
    t.contains("change history")
}

/// classify_heading splits "6.2.3 Title" → ("6.2.3","Title",informative?) (== Go).
fn classify_heading(text: &str) -> (String, String, bool) {
    if let Some(m) = re_annex().captures(text) {
        let inf = text.to_lowercase().contains("informative");
        let h = m.get(2).map_or("", |x| x.as_str()).trim().to_string();
        let label = format!("Annex {}", &m[1]);
        let heading = if h.is_empty() { label.clone() } else { h };
        return (label, heading, inf);
    }
    if let Some(m) = re_annex_sub().captures(text) {
        return (m[1].to_string(), m[2].trim().to_string(), false);
    }
    if let Some(m) = re_numeric().captures(text) {
        return (m[1].to_string(), m[2].trim().to_string(), false);
    }
    (String::new(), text.to_string(), false)
}

/// node_text concatenates all descendant text nodes (== Go nodeText).
fn node_text(n: ego_tree::NodeRef<scraper::Node>) -> String {
    let mut sb = String::new();
    for d in n.descendants() {
        if let scraper::Node::Text(t) = d.value() {
            sb.push_str(t);
        }
    }
    sb
}

/// table_rows extracts tr → td/th cells, collapse-normalised (== Go tableRows).
fn table_rows(n: ego_tree::NodeRef<scraper::Node>) -> Vec<Vec<String>> {
    let mut rows = Vec::new();
    for d in n.descendants() {
        if let scraper::Node::Element(e) = d.value() {
            if e.name() == "tr" {
                let mut cells = Vec::new();
                for c in d.children() {
                    if let scraper::Node::Element(ce) = c.value() {
                        if ce.name() == "td" || ce.name() == "th" {
                            cells.push(collapse(&node_text(c)));
                        }
                    }
                }
                if !cells.is_empty() {
                    rows.push(cells);
                }
            }
        }
    }
    rows
}

struct Walker {
    spec_id: String,
    release: String,
    version: String,
    clauses: Vec<ParsedClause>,
    cur: Option<ParsedClause>,
    buf: String,
    id: u64,
    informative_annex: bool,
    in_change_history: bool,
    pub saw_change_history: bool,
    pub degraded: bool,
}

impl Walker {
    fn walk(&mut self, n: ego_tree::NodeRef<scraper::Node>) {
        match n.value() {
            scraper::Node::Comment(c) => {
                if c.contains("3GPP-MCP-DEGRADED") {
                    self.degraded = true;
                }
            }
            scraper::Node::Element(e) => match e.name() {
                "h1" | "h2" | "h3" | "h4" | "h5" | "h6" => {
                    self.on_heading(&node_text(n));
                    return;
                }
                "table" => {
                    self.on_table(n);
                    return;
                }
                "p" | "li" | "pre" | "dd" | "dt" => {
                    // A HEADING NESTED IN A TEXT BLOCK IS STILL A HEADING.
                    //
                    // LibreOffice renders Word's auto-numbered headings as an ordered
                    // list — <ol><li><h1>Scope</h1></li></ol> — because that is what
                    // Word's numbering IS. Treating <li> as a leaf swallowed the <h1>
                    // into the running text, so the walker never opened a clause and
                    // the document parsed to ZERO clauses.
                    //
                    // upsert_version still wrote the catalogue row, which is exactly a
                    // missing_content hole: the corpus promises a spec it holds no text
                    // for. TR 25.890, TR 25.933, TS 34.123-1 and a dozen more sat in
                    // that state through four repair runs, re-fetched and re-converted
                    // every time, because the file was always fine and the walk was not.
                    if contains_heading(n) {
                        for c in n.children() {
                            self.walk(c);
                        }
                        return;
                    }
                    let txt = node_text(n);
                    if !txt.is_empty() {
                        self.buf.push_str(&txt);
                        self.buf.push('\n');
                    }
                    return;
                }
                _ => {}
            },
            _ => {}
        }
        for c in n.children() {
            self.walk(c);
        }
    }

    fn on_heading(&mut self, raw: &str) {
        let text = collapse(raw);
        if text.is_empty() {
            return;
        }
        if is_change_history(&text) {
            self.flush();
            self.in_change_history = true;
            self.saw_change_history = true;
            self.cur = None;
            return;
        }
        self.in_change_history = false;
        let (path, heading, informative) = classify_heading(&text);
        if re_annex().is_match(&text) {
            self.informative_annex = informative;
        } else if !path.is_empty() && !path.contains('.') {
            self.informative_annex = false;
        }
        self.flush();
        self.id += 1;
        self.cur = Some(ParsedClause {
            chunk_id: self.id,
            clause_path: path,
            heading,
            text: String::new(),
            is_normative: !self.informative_annex,
        });
    }

    fn on_table(&mut self, n: ego_tree::NodeRef<scraper::Node>) {
        for r in table_rows(n) {
            self.buf.push_str(&r.join("\t"));
            self.buf.push('\n');
        }
    }

    fn flush(&mut self) {
        let Some(mut cur) = self.cur.take() else {
            self.buf.clear();
            return;
        };
        cur.text = self.buf.trim().to_string();
        self.buf.clear();
        if !cur.clause_path.is_empty() || !cur.heading.is_empty() || !cur.text.is_empty() {
            self.clauses.push(cur);
        }
    }
}

/// contains_heading reports whether `n`'s subtree holds an h1-h6.
///
/// Used to tell a text block that merely LOOKS like a leaf from one that wraps a
/// heading. LibreOffice emits Word's auto-numbered headings as <ol><li><h1>…</h1></li>,
/// so a walker that stops at <li> never sees them.
fn contains_heading(n: ego_tree::NodeRef<scraper::Node>) -> bool {
    for c in n.descendants() {
        if let scraper::Node::Element(e) = c.value() {
            if matches!(e.name(), "h1" | "h2" | "h3" | "h4" | "h5" | "h6") {
                return true;
            }
        }
    }
    false
}

/// parse_html_clauses walks a converted spec's HTML into clause-leaf chunks, in the same
/// document order and with the same per-spec chunk_id sequence as Go's htmlparse walker.
/// spec_id/release/version come from parse_filename_meta. Returns (clauses, saw_change_history,
/// degraded).
pub fn parse_html_clauses(
    html: &str,
    spec_id: &str,
    release: &str,
    version: &str,
) -> (Vec<ParsedClause>, bool, bool) {
    let doc = Html::parse_document(html);
    let mut w = Walker {
        spec_id: spec_id.to_string(),
        release: release.to_string(),
        version: version.to_string(),
        clauses: Vec::new(),
        cur: None,
        buf: String::new(),
        id: 0,
        informative_annex: false,
        in_change_history: false,
        saw_change_history: false,
        degraded: false,
    };
    w.walk(doc.tree.root());
    w.flush();
    // silence unused-field warnings on the metadata the ingest will read off each clause.
    let _ = (&w.spec_id, &w.release, &w.version);
    (w.clauses, w.saw_change_history, w.degraded)
}

use scraper::Html;

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn version_code_decode_matches_go() {
        assert_eq!(
            decode_version_code("i60"),
            Some(("Rel-18".into(), "18.6.0".into()))
        );
        assert_eq!(
            decode_version_code("j50"),
            Some(("Rel-19".into(), "19.5.0".into()))
        );
        assert_eq!(
            decode_version_code("h60"),
            Some(("Rel-17".into(), "17.6.0".into()))
        );
        assert_eq!(
            decode_version_code("083700"),
            Some(("Rel-8".into(), "8.37.0".into()))
        );
        // major 3 → Rel-99 (the no-Rel-98 jump).
        assert_eq!(
            decode_version_code("300"),
            Some(("Rel-99".into(), "3.0.0".into()))
        );
        assert_eq!(decode_version_code("zz"), None);
    }

    #[test]
    fn encode_is_inverse_of_decode() {
        assert_eq!(encode_version_code("18.6.0").as_deref(), Some("i60"));
        assert_eq!(encode_version_code("19.5.0").as_deref(), Some("j50"));
        assert_eq!(encode_version_code("18.36.0"), None); // 36 not encodable in one char
    }

    #[test]
    fn archive_url_matches_go() {
        assert_eq!(
            archive_url("33.128", "18.6.0"),
            "https://www.3gpp.org/ftp/Specs/archive/33_series/33.128/33128-i60.zip"
        );
    }

    #[test]
    fn filename_meta_uses_convert_dir_release() {
        // 23501-j50 under Rel-19/ → 23.501, SA2, Rel-19 (dir), 19.5.0.
        let m = parse_filename_meta("/x/convert/Rel-19/23501-j50.html").unwrap();
        assert_eq!(
            m,
            SpecMeta {
                spec_id: "23.501".into(),
                series: "23".into(),
                doc_type: "TS".into(),
                working_group: "SA2".into(),
                release: "Rel-19".into(),
                version: "19.5.0".into(),
                docx_url: "https://www.3gpp.org/ftp/Specs/archive/23_series/23.501/23501-j50.zip"
                    .into(),
            }
        );
    }

    #[test]
    fn filename_meta_falls_back_to_version_major_without_release_dir() {
        // No release dir → release from the version-major decode (h60 → Rel-17).
        let m = parse_filename_meta("/tmp/38331-h60.html").unwrap();
        assert_eq!(m.spec_id, "38.331");
        assert_eq!(m.release, "Rel-17");
        assert_eq!(m.working_group, "RAN");
    }

    #[test]
    fn legacy_gsm_four_digit_number() {
        // 4-digit GSM number: 0408 → 04.08 (admitted by the {4,5} regex, issue #129).
        let m = parse_filename_meta("/x/convert/GSM/0408-500.html").unwrap();
        assert_eq!(m.spec_id, "04.08");
        assert_eq!(m.release, "GSM");
        assert_eq!(m.version, "5.0.0");
    }

    #[test]
    fn bad_filename_errors() {
        assert!(parse_filename_meta("/x/not-a-spec.html").is_err());
    }

    #[test]
    fn walker_segments_clauses_like_go() {
        let html = r#"<html><body>
            <h1>1 Scope</h1><p>This document specifies the system.</p>
            <h2>1.1 General</h2><p>General text.</p>
            <h1>Annex A (informative): Examples</h1><p>Example text.</p>
            <h2>A.1 Sub</h2><p>Sub annex text.</p>
            <h1>Change history</h1><table><tr><td>2024-01</td><td>edit</td></tr></table>
        </body></html>"#;
        let (clauses, saw_ch, degraded) = parse_html_clauses(html, "23.501", "Rel-19", "19.5.0");
        assert!(saw_ch, "change history heading detected");
        assert!(!degraded);
        // Change-History section is excluded → exactly 4 clauses, chunk_ids 1..4 in order.
        assert_eq!(clauses.len(), 4, "got {:?}", clauses);
        let got: Vec<(u64, &str, &str, bool)> = clauses
            .iter()
            .map(|c| {
                (
                    c.chunk_id,
                    c.clause_path.as_str(),
                    c.heading.as_str(),
                    c.is_normative,
                )
            })
            .collect();
        assert_eq!(
            got,
            vec![
                (1, "1", "Scope", true),
                (2, "1.1", "General", true),
                (3, "Annex A", "(informative): Examples", false),
                (4, "A.1", "Sub", false), // inherits the informative-annex context
            ]
        );
        assert_eq!(clauses[0].text, "This document specifies the system.");
        assert_eq!(clauses[3].text, "Sub annex text.");
    }
}

#[cfg(test)]
mod nested_heading_tests {
    use super::*;

    // A heading wrapped in a list item is still a heading.
    //
    // LibreOffice renders Word's auto-numbered headings as an ordered list —
    // <ol><li><h1>Scope</h1></li></ol> — because that is what Word's numbering IS.
    // The walker treated <li> as a leaf and returned without descending, so the <h1>
    // was swallowed into the running text, no clause was ever opened, and the whole
    // document parsed to ZERO clauses. upsert_version still wrote the catalogue row,
    // which is exactly a missing_content hole: the corpus promising text it does not
    // hold. TR 25.890, TR 25.933 and TS 33.900 sat in that state through four repair
    // runs — re-fetched and re-converted every time, because the file was always fine
    // and the walk was not.
    #[test]
    fn a_heading_inside_a_list_item_still_opens_a_clause() {
        let html = r#"<html><body>
            <ol>
              <li><h1><a name="x"></a>Scope</h1></li>
              <li><h2>General</h2></li>
            </ol>
            <p>The purpose of this document is to capture the discussions.</p>
        </body></html>"#;
        let (clauses, _, _) = parse_html_clauses(html, "25.890", "Rel-5", "1.0.0");
        assert!(
            clauses.len() >= 2,
            "both nested headings must open clauses, got {}: {:?}",
            clauses.len(),
            clauses.iter().map(|c| &c.heading).collect::<Vec<_>>()
        );
        assert!(
            clauses.iter().any(|c| c.heading.contains("Scope")),
            "the first heading must survive: {:?}",
            clauses.iter().map(|c| &c.heading).collect::<Vec<_>>()
        );
        assert!(
            clauses.iter().any(|c| c.text.contains("capture the discussions")),
            "the prose after the headings must land in a clause"
        );
    }

    // A list item that is only text must still behave as one: descending into every
    // <li> would turn ordinary bullet lists into clause boundaries.
    #[test]
    fn a_plain_list_item_is_still_body_text() {
        let html = r#"<html><body>
            <h1>6.1 Requirements</h1>
            <ul><li>the first requirement</li><li>the second requirement</li></ul>
        </body></html>"#;
        let (clauses, _, _) = parse_html_clauses(html, "23.501", "Rel-19", "19.7.0");
        assert_eq!(clauses.len(), 1, "a bullet list must not split the clause");
        assert!(clauses[0].text.contains("the first requirement"));
        assert!(clauses[0].text.contains("the second requirement"));
    }
}
