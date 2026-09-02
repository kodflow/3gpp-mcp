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
    // The trailing suffix may be introduced by '-' as well as '_'.
    //
    // 3GPP archives ship editorial variants beside the spec: "-clean", "-rm",
    // "-diff-100", "-cl". Accepting only "_…" rejected them outright, so the ONLY file
    // that parsed out of TR 26.917's archive was its 8 KB cover page — which yields no
    // clause. The spec was catalogued with no text behind it, i.e. a missing_content
    // hole, while 324 KB of the actual document sat unread beside it.
    //
    // The version code is exactly three chars from [0-9a-z] (or six digits), and '-' is
    // in neither class, so a suffix cannot be mistaken for a code: "26917-130-clean"
    // can only split as 26917 / 130 / -clean, and "34123-1-a70" only as 34123-1 / a70.
    let re_file =
        Regex::new(r"^([0-9]{4,5}(?:-[0-9]+)?)-([0-9a-z]{3}|[0-9]{6})(?:[-_].*)?$").unwrap();
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

/// own_page_stamp is the cover/footer stamp an ETSI deliverable puts on ITSELF,
/// e.g. "ETSI TS 102 221 V7.10.0". Empty for a 3GPP spec, whose id ("23.501") is
/// not an ETSI deliverable id — so the 3GPP walk is left byte-for-byte unchanged.
fn own_page_stamp(spec_id: &str, version: &str) -> String {
    if !spec_id.starts_with("ETSI ") || version.is_empty() {
        return String::new();
    }
    format!("{spec_id} V{version}")
}

/// is_own_page_stamp answers whether one line is this document's RUNNING PAGE
/// FURNITURE rather than its text.
///
/// WHY THIS EXISTS. ETSI publishes PDFs; convert_pdf reads the text layer with
/// `pdftotext -layout`, which faithfully keeps what is printed on the page —
/// including the footer ETSI stamps on every single one:
///
/// ```text
/// Release 16    107    ETSI TS 103 666-1 V16.0.0 (2020-10)
/// ```
///
/// That footer carries the VERSION. So a clause whose wording did not change
/// between two published versions still yields two DIFFERENT texts, and the
/// content-addressed corpus — whose whole job is to store one body once and point
/// every version at it — can never collapse them. Measured on the ETSI half before
/// this filter: 33 881 clauses held 460 584 distinct bodies for 391 461 version
/// rows, i.e. essentially one "new" body per version, and `trace_clause` answered
/// that every version introduced a brand-new paragraph and made the previous one
/// obsolete. On TS 102 221 clause 11.1.5.1 that was 117 bodies across 126 versions;
/// with the furniture dropped it is 19 — which is the real revision history, and
/// the thing the corpus exists to show. Corpus-wide the collapse is 44,2 %.
///
/// THE DISCRIMINATOR IS SELF-NAMING, not shape. A bibliographic reference in
/// clause 2 names ANOTHER deliverable ("ETSI TS 102 223 V7.11.0 (2008-02): ..."),
/// while the footer always names THE DOCUMENT IN HAND. Measured across the whole
/// ETSI half: of 232 044 lines carrying a version-stamped ETSI id at end of line,
/// 232 044 named their own spec_id and version and 0 named another. Requiring the
/// trailing "(yyyy-mm)" keeps a sentence that merely ENDS with a cited version
/// ("... as defined in ETSI TS 102 221 V7.10.0.") out of it.
fn is_own_page_stamp(line: &str, stamp: &str) -> bool {
    if stamp.is_empty() {
        return false;
    }
    let l = line.trim_end();
    let Some(rest) = l.strip_suffix(')') else {
        return false;
    };
    let Some(open) = rest.rfind('(') else {
        return false;
    };
    let date = &rest[open + 1..];
    // "yyyy-mm" — the publication date ETSI prints beside the version.
    if date.len() != 7
        || !date[..4].bytes().all(|b| b.is_ascii_digit())
        || date.as_bytes()[4] != b'-'
        || !date[5..].bytes().all(|b| b.is_ascii_digit())
    {
        return false;
    }
    rest[..open].trim_end().ends_with(stamp)
}

/// strip_page_furniture drops whole furniture LINES from a text block, keeping the
/// rest verbatim. Per line rather than per node because the salvage path dumps a
/// whole document into one <pre>: a node-level test would either spare every footer
/// there or discard the document.
fn strip_page_furniture(text: &str, stamp: &str) -> String {
    if stamp.is_empty() || !text.contains(stamp) {
        return text.to_string();
    }
    let kept: Vec<&str> = text
        .lines()
        .filter(|l| !is_own_page_stamp(l, stamp))
        .collect();
    kept.join("\n")
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
    /// This document's own cover/footer stamp; see is_own_page_stamp.
    page_stamp: String,
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
                    let txt = strip_page_furniture(&node_text(n), &self.page_stamp);
                    let txt = txt.trim_end().to_string();
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

/// salvage_numbered_text segments plain text on 3GPP clause numbering, for documents
/// whose HTML carries no heading markup at all.
///
/// Used only as a last resort by `parse_html_clauses` when the structured walk found
/// nothing — a document converted by the doc-text-salvage path, which dumps the text
/// into one <pre>. The numbering is still there; only the markup is gone.
///
/// A table-of-contents line is NOT a heading: it ends with the page number the entry
/// points at ("1<tab>Scope<tab>5"). Taking those would produce a duplicate, empty
/// clause for every real one.
fn salvage_numbered_text(text: &str) -> Vec<ParsedClause> {
    static R: OnceLock<Regex> = OnceLock::new();
    let re = R.get_or_init(|| Regex::new(r"^(\d+(?:\.\d+)*)[ \t]+(\S.*)$").unwrap());
    static TOC: OnceLock<Regex> = OnceLock::new();
    let toc = TOC.get_or_init(|| Regex::new(r"[ \t]+\d+$").unwrap());

    let mut out: Vec<ParsedClause> = Vec::new();
    let mut buf = String::new();
    let mut id = 0u64;
    for raw in text.lines() {
        let line = raw.trim_end();
        if let Some(c) = re.captures(line) {
            let heading = c[2].trim();
            if !toc.is_match(heading) {
                if let Some(cur) = out.last_mut() {
                    cur.text = buf.trim().to_string();
                }
                buf.clear();
                id += 1;
                out.push(ParsedClause {
                    chunk_id: id,
                    clause_path: c[1].to_string(),
                    heading: heading.to_string(),
                    text: String::new(),
                    is_normative: true,
                });
                continue;
            }
        }
        if !line.trim().is_empty() {
            buf.push_str(line.trim());
            buf.push('\n');
        }
    }
    if let Some(cur) = out.last_mut() {
        cur.text = buf.trim().to_string();
    }
    out
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
        page_stamp: own_page_stamp(spec_id, version),
        informative_annex: false,
        in_change_history: false,
        saw_change_history: false,
        degraded: false,
    };
    w.walk(doc.tree.root());
    w.flush();
    // silence unused-field warnings on the metadata the ingest will read off each clause.
    let _ = (&w.spec_id, &w.release, &w.version);

    // A DOCUMENT THAT SEGMENTS INTO NOTHING IS A SALVAGE CASE, NOT AN EMPTY ONE.
    //
    // When soffice cannot render a legacy .doc at all, the conversion falls back to
    // dumping the text into a single <pre>. There is no heading markup left, so the
    // walk above yields zero clauses — while ingest still writes the catalogue row,
    // which is exactly a missing_content hole. Six releases of TS 34.123-1 sat in that
    // state.
    //
    // The 3GPP numbering survives in the TEXT ("4.1<tab>Test Methodology"), so segment
    // on that instead of discarding the document. This runs ONLY when the structured
    // walk found nothing, so it cannot change how a well-formed spec is parsed.
    if w.clauses.is_empty() {
        // The salvage path re-reads the whole document as flat text, so it has to
        // drop the page furniture too — otherwise a document with no heading markup
        // keeps the footers the structured walk removes.
        let flat = strip_page_furniture(&node_text(doc.tree.root()), &w.page_stamp);
        let salvaged = salvage_numbered_text(&flat);
        if !salvaged.is_empty() {
            return (salvaged, w.saw_change_history, true);
        }
    }

    (w.clauses, w.saw_change_history, w.degraded)
}

use scraper::Html;

#[cfg(test)]
mod tests {
    use super::*;

    /// THE DEFECT THIS LOCKS DOWN. ETSI prints its own id AND VERSION in the footer
    /// of every page, and `pdftotext -layout` keeps it. Two versions whose wording
    /// is identical therefore produced two different clause texts, so the
    /// content-addressed corpus could never store the body once — measured on the
    /// live ETSI half: 460 584 distinct bodies for 391 461 version rows across
    /// 33 881 clauses, and `trace_clause` reporting every version as a brand-new
    /// paragraph that obsoleted the last.
    ///
    /// Falsified: with the filter removed, the two versions below yield DIFFERENT
    /// clause texts and the assertion on equality fails.
    #[test]
    fn the_footer_that_names_this_document_is_not_its_text() {
        let page = |v: &str, d: &str| {
            format!(
                "<html><body><h1>11.1.5.1	READ RECORD</h1>                 <p>This function reads one complete record.</p>                 <p>Release 7   90    ETSI TS 102 221 V{v} ({d})</p>                 <p>The response is the record content.</p></body></html>"
            )
        };
        let a = parse_html_clauses(
            &page("7.10.0", "2008-02"),
            "ETSI TS 102 221",
            "ETSI",
            "7.10.0",
        );
        let b = parse_html_clauses(
            &page("7.11.0", "2008-07"),
            "ETSI TS 102 221",
            "ETSI",
            "7.11.0",
        );
        assert_eq!(a.0.len(), 1, "one clause, got {:?}", a.0);
        assert_eq!(
            a.0[0].text, b.0[0].text,
            "two versions of an UNCHANGED clause must yield the same text, or the              content-addressed store cannot collapse them"
        );
        assert!(
            !a.0[0].text.contains("ETSI TS 102 221 V7.10.0"),
            "the running footer is still in the clause text: {:?}",
            a.0[0].text
        );
        assert!(
            a.0[0].text.contains("reads one complete record")
                && a.0[0].text.contains("response is the record content"),
            "the real text either side of the page break must survive: {:?}",
            a.0[0].text
        );
    }

    /// The discriminator is SELF-naming. Clause 2 of any deliverable cites OTHER
    /// deliverables with the same shape, and those lines are the document's text.
    /// Dropping them would delete the normative reference list — a far worse defect
    /// than the one being fixed.
    #[test]
    fn a_reference_to_another_deliverable_is_kept() {
        let html = "<html><body><h1>2	References</h1>            <p>[1] ETSI TS 102 223 V7.11.0 (2008-02)</p>            <p>[2] ETSI TS 102 221 V6.3.0 (2004-09)</p>            <p>as defined in ETSI TS 102 221 V7.10.0.</p>            <p>Release 7   9    ETSI TS 102 221 V7.10.0 (2008-02)</p></body></html>";
        let (cl, _, _) = parse_html_clauses(html, "ETSI TS 102 221", "ETSI", "7.10.0");
        let t = &cl[0].text;
        assert!(t.contains("ETSI TS 102 223 V7.11.0"), "another spec: {t:?}");
        assert!(
            t.contains("ETSI TS 102 221 V6.3.0"),
            "another VERSION of this spec: {t:?}"
        );
        assert!(
            t.contains("as defined in ETSI TS 102 221 V7.10.0."),
            "prose ending in a citation: {t:?}"
        );
        assert!(!t.contains("Release 7"), "the footer must be gone: {t:?}");
    }

    /// The 3GPP half is already deduplicated and must not be re-ingested: a 3GPP
    /// spec_id is not an ETSI deliverable id, so the filter is inert there and the
    /// walk stays byte-for-byte what it was.
    #[test]
    fn a_3gpp_spec_is_untouched_by_the_etsi_footer_filter() {
        assert_eq!(own_page_stamp("23.501", "18.5.0"), "");
        let html = "<html><body><h1>6.2.1	AMF</h1>            <p>Release 18   47    ETSI TS 123 501 V18.5.0 (2024-06)</p></body></html>";
        let (cl, _, _) = parse_html_clauses(html, "23.501", "Rel-18", "18.5.0");
        assert!(
            cl[0].text.contains("ETSI TS 123 501 V18.5.0"),
            "the 3GPP walk must be unchanged: {:?}",
            cl[0].text
        );
    }

    /// Shape alone is not enough, and neither is the id alone: the trailing
    /// "(yyyy-mm)" is what separates a footer from a sentence that cites a version.
    #[test]
    fn the_stamp_test_is_exact() {
        let s = "ETSI TS 102 221 V7.10.0";
        assert!(is_own_page_stamp(
            "Release 7   90    ETSI TS 102 221 V7.10.0 (2008-02)",
            s
        ));
        assert!(
            is_own_page_stamp("ETSI TS 102 221 V7.10.0 (2008-02)  ", s),
            "the cover page"
        );
        assert!(!is_own_page_stamp(
            "see ETSI TS 102 221 V7.10.0 (2008-02) for details",
            s
        ));
        assert!(
            !is_own_page_stamp("ETSI TS 102 221 V7.10.0", s),
            "no date: not a stamp"
        );
        assert!(
            !is_own_page_stamp("ETSI TS 102 221 V7.10.0 (2008-2)", s),
            "malformed date"
        );
        assert!(
            !is_own_page_stamp("ETSI TS 102 221 V7.11.0 (2008-07)", s),
            "another version"
        );
        assert!(
            !is_own_page_stamp("anything at all", ""),
            "3GPP: no stamp, no drops"
        );
    }

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
            clauses
                .iter()
                .any(|c| c.text.contains("capture the discussions")),
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

#[cfg(test)]
mod salvage_tests {
    use super::*;

    // A document with no heading markup at all must still segment.
    //
    // When soffice cannot render a legacy .doc, the conversion dumps the text into a
    // single <pre>. The structured walk finds no heading and yields zero clauses, while
    // ingest still writes the catalogue row — which is exactly a missing_content hole.
    // Six releases of TS 34.123-1 sat in that state through four repair runs.
    #[test]
    fn a_headingless_salvage_still_segments_on_the_numbering() {
        let html = "<html><body><pre>\n\
            3GPP TS 34.123-1 V10.7.0 (2013-12)\n\
            \n\
            1\tScope\t5\n\
            4\tOverview\t6\n\
            \n\
            3\tDefinitions and abbreviations\n\
            Void\n\
            \n\
            4\tOverview\n\
            \n\
            4.1\tTest Methodology\n\
            The requirements are provided in Release 11.\n\
            </pre></body></html>";
        let (clauses, _, degraded) = parse_html_clauses(html, "34.123-1", "Rel-10", "10.7.0");

        assert!(degraded, "a salvaged parse must be flagged degraded");
        let paths: Vec<&str> = clauses.iter().map(|c| c.clause_path.as_str()).collect();
        assert_eq!(
            paths,
            vec!["3", "4", "4.1"],
            "the table of contents must not become clauses: {paths:?}"
        );
        assert_eq!(clauses[0].heading, "Definitions and abbreviations");
        assert!(
            clauses[2].text.contains("provided in Release 11"),
            "prose must land under the clause that precedes it: {:?}",
            clauses[2].text
        );
    }

    // The salvage must NEVER pre-empt a document the structured walk can read: it runs
    // only when that walk produced nothing.
    #[test]
    fn a_well_formed_document_is_not_salvaged() {
        let html =
            "<html><body><h1>6.1 Requirements</h1><p>1 is not a heading here.</p></body></html>";
        let (clauses, _, degraded) = parse_html_clauses(html, "23.501", "Rel-19", "19.7.0");
        assert_eq!(clauses.len(), 1, "the structured walk owns this document");
        assert!(!degraded, "a clean parse must not be flagged degraded");
        assert!(clauses[0].text.contains("1 is not a heading here"));
    }

    // Text with no numbering at all yields nothing rather than one giant clause: a
    // document we genuinely cannot segment must stay visible as a hole.
    #[test]
    fn unnumbered_text_is_not_forced_into_a_clause() {
        let html = "<html><body><pre>just some prose\nand more prose\n</pre></body></html>";
        let (clauses, _, _) = parse_html_clauses(html, "23.501", "Rel-19", "19.7.0");
        assert!(clauses.is_empty(), "got {clauses:?}");
    }
}

#[cfg(test)]
mod suffix_tests {
    use super::*;

    // 3GPP ships editorial variants beside the spec, introduced by '-' as well as '_':
    // "-clean", "-rm", "-diff-100", "-cl". Accepting only "_…" rejected them, so the
    // only file that parsed out of TR 26.917's archive was its 8 KB cover page — which
    // yields no clause. The spec was catalogued with no text behind it while 324 KB of
    // the actual document sat unread beside it.
    #[test]
    fn a_hyphenated_editorial_suffix_is_still_the_same_spec() {
        for name in [
            "26917-130-clean",
            "26917-130-diff-100",
            "30531-016400-rm",
            "26917-130_S4-AHI729-TR26-917-v130-cover-page",
        ] {
            let m = parse_filename_meta(&format!("convert/Rel-14/{name}.html"))
                .unwrap_or_else(|e| panic!("{name}: {e}"));
            assert_eq!(
                m.spec_id,
                if name.starts_with("30531") {
                    "30.531"
                } else {
                    "26.917"
                }
            );
        }
    }

    // A sub-part spec must still split on the PART, not on the suffix rule: the version
    // code is three chars from [0-9a-z] and '-' is in neither class, so there is only
    // one way to read these.
    #[test]
    fn a_sub_part_spec_is_unaffected_by_the_suffix_rule() {
        let m = parse_filename_meta("convert/Rel-10/34123-1-a70.html").unwrap();
        assert_eq!(m.spec_id, "34.123-1");
        assert_eq!(m.version, "10.7.0");

        let m = parse_filename_meta("convert/Rel-20/38760-1-030.html").unwrap();
        assert_eq!(m.spec_id, "38.760-1");
        assert_eq!(m.version, "0.3.0");

        // And a sub-part spec WITH an editorial suffix reads correctly too.
        let m = parse_filename_meta("convert/Rel-10/34123-1-a70-clean.html").unwrap();
        assert_eq!(m.spec_id, "34.123-1");
        assert_eq!(m.version, "10.7.0");
    }

    // Genuinely malformed names must still be rejected: the suffix rule must not turn
    // the pattern into "anything goes".
    #[test]
    fn a_malformed_name_is_still_rejected() {
        for bad in ["notaspec", "123-abc", "26917", "26917-1234"] {
            assert!(
                parse_filename_meta(&format!("convert/Rel-14/{bad}.html")).is_err(),
                "{bad} must not parse"
            );
        }
    }
}
