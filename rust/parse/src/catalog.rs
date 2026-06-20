//! catalog — Rust port of internal/catalog parse (Phase 7). Extracts authoritative spec
//! metadata from the public 3GPP DynaReport HTML: status-report.htm → per-spec
//! title/TS-or-TR/working-group + the latest (spec, release, version); Releases.aspx →
//! the release calendar + freeze_date (the non-monotonic-ordering fix). It is a metadata
//! OVERLAY — the ingest applies it onto specs/spec_versions rows content ingest already
//! created (cite-or-silent: never invents catalogue-only specs).

use ego_tree::NodeRef;
use regex::Regex;
use scraper::{Html, Node};
use std::sync::OnceLock;

/// Per-spec metadata overlay (== Go catalog.SpecMeta).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SpecMetaOverlay {
    pub spec_id: String,
    pub series: String,
    pub doc_type: String,
    pub title: String,
    pub working_group: String,
}

/// Latest (spec, release, version) line from a status-report section (== Go VersionRel).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct VersionRel {
    pub spec_id: String,
    pub release: String,
    pub version: String,
}

/// A release-calendar row from Releases.aspx (== Go ReleaseMeta). Dates are "YYYY-MM-DD".
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ReleaseMeta {
    pub code: String,
    pub name: String,
    pub status: String,
    pub start_date: Option<String>,
    pub freeze_date: Option<String>,
    pub freeze_meeting: String,
}

fn re_rel_anchor() -> &'static Regex {
    static R: OnceLock<Regex> = OnceLock::new();
    R.get_or_init(|| Regex::new(r"(?:active|dead)(Rel-\d+)").unwrap())
}
fn re_freeze() -> &'static Regex {
    static R: OnceLock<Regex> = OnceLock::new();
    R.get_or_init(|| Regex::new(r"(\d{4}-\d{2}-\d{2})(?:\s*\(([^)]+)\))?").unwrap())
}
fn re_spec_id() -> &'static Regex {
    static R: OnceLock<Regex> = OnceLock::new();
    R.get_or_init(|| Regex::new(r"\b(\d{2}\.\d{3}(?:-\d+)?)\b").unwrap())
}

fn collapse(s: &str) -> String {
    s.replace('\u{00a0}', " ")
        .split_whitespace()
        .collect::<Vec<_>>()
        .join(" ")
}

fn node_text(n: NodeRef<Node>) -> String {
    let mut s = String::new();
    for d in n.descendants() {
        if let Node::Text(t) = d.value() {
            s.push_str(t);
        }
    }
    s
}

/// table_rows extracts a table's rows as trimmed cell text (th/td), stopping at each tr
/// without descending into a row's nested tables — mirrors Go catalog.tableRows.
fn table_rows(t: NodeRef<Node>) -> Vec<Vec<String>> {
    let mut rows = Vec::new();
    fn walk(n: NodeRef<Node>, rows: &mut Vec<Vec<String>>) {
        if let Node::Element(e) = n.value() {
            if e.name() == "tr" {
                let mut cells = Vec::new();
                for c in n.children() {
                    if let Node::Element(ce) = c.value() {
                        if ce.name() == "td" || ce.name() == "th" {
                            cells.push(collapse(&node_text(c)));
                        }
                    }
                }
                if !cells.is_empty() {
                    rows.push(cells);
                }
                return; // do not descend past the tr
            }
        }
        for c in n.children() {
            walk(c, rows);
        }
    }
    walk(t, &mut rows);
    rows
}

fn header_index(header: &[String]) -> std::collections::HashMap<&'static str, i32> {
    let mut idx = std::collections::HashMap::from([
        ("type", -1i32),
        ("spec num", -1),
        ("title", -1),
        ("vers", -1),
        ("wg", -1),
    ]);
    for (i, h) in header.iter().enumerate() {
        let key = collapse(h).to_lowercase();
        if let Some(v) = idx.get_mut(key.as_str()) {
            *v = i as i32;
        }
    }
    idx
}

fn norm_spec_id(s: &str) -> String {
    re_spec_id()
        .captures(s)
        .map(|m| m[1].to_string())
        .unwrap_or_default()
}

fn series_of(spec_id: &str) -> &str {
    match spec_id.find('.') {
        Some(i) if i > 0 => &spec_id[..i],
        _ => "",
    }
}

fn cell(row: &[String], i: i32) -> &str {
    if i < 0 || i as usize >= row.len() {
        return "";
    }
    row[i as usize].trim()
}

fn parse_date(s: &str) -> Option<String> {
    let s = s.trim();
    let m = re_freeze().captures(s)?;
    Some(m[1].to_string())
}

fn split_freeze(s: &str) -> (Option<String>, String) {
    match re_freeze().captures(s) {
        Some(m) => (
            Some(m[1].to_string()),
            m.get(2).map_or("", |x| x.as_str()).to_string(),
        ),
        None => (None, String::new()),
    }
}

/// parse_status_report parses status-report.htm → spec metadata (newest section wins) +
/// every (spec, release) latest version (== Go ParseStatusReport).
pub fn parse_status_report(html: &str) -> (Vec<SpecMetaOverlay>, Vec<VersionRel>) {
    let doc = Html::parse_document(html);
    let mut spec_by_id: std::collections::HashMap<String, SpecMetaOverlay> =
        std::collections::HashMap::new();
    let mut order: Vec<String> = Vec::new();
    let mut vers: Vec<VersionRel> = Vec::new();
    let mut cur_release = String::new();

    for n in doc.tree.root().descendants() {
        let Node::Element(e) = n.value() else {
            continue;
        };
        match e.name() {
            "a" => {
                let probe = format!(
                    "{} {}",
                    e.attr("name").unwrap_or(""),
                    e.attr("id").unwrap_or("")
                );
                if let Some(m) = re_rel_anchor().captures(&probe) {
                    cur_release = m[1].to_string();
                }
            }
            "table" => parse_status_table(n, &cur_release, &mut spec_by_id, &mut order, &mut vers),
            _ => {}
        }
    }

    let specs = order
        .into_iter()
        .filter_map(|id| spec_by_id.get(&id).cloned())
        .collect();
    (specs, vers)
}

fn parse_status_table(
    t: NodeRef<Node>,
    release: &str,
    spec_by_id: &mut std::collections::HashMap<String, SpecMetaOverlay>,
    order: &mut Vec<String>,
    vers: &mut Vec<VersionRel>,
) {
    let rows = table_rows(t);
    if rows.len() < 2 {
        return;
    }
    let idx = header_index(&rows[0]);
    let (ti, si, tti, vi, wi) = (
        idx["type"],
        idx["spec num"],
        idx["title"],
        idx["vers"],
        idx["wg"],
    );
    if si < 0 || ti < 0 {
        return; // not the catalogue table
    }
    let need = ti.max(si).max(tti).max(vi);
    for row in &rows[1..] {
        if (row.len() as i32) <= need {
            continue;
        }
        let spec_id = norm_spec_id(cell(row, si));
        if spec_id.is_empty() {
            continue;
        }
        if !spec_by_id.contains_key(&spec_id) {
            order.push(spec_id.clone());
        }
        let needs_meta = spec_by_id.get(&spec_id).is_none_or(|c| c.title.is_empty());
        if needs_meta {
            spec_by_id.insert(
                spec_id.clone(),
                SpecMetaOverlay {
                    spec_id: spec_id.clone(),
                    series: series_of(&spec_id).to_string(),
                    doc_type: cell(row, ti).to_uppercase(),
                    title: cell(row, tti).to_string(),
                    working_group: cell(row, wi).to_string(),
                },
            );
        }
        if !release.is_empty() && vi >= 0 {
            let v = cell(row, vi);
            if !v.is_empty() {
                vers.push(VersionRel {
                    spec_id: spec_id.clone(),
                    release: release.to_string(),
                    version: v.to_string(),
                });
            }
        }
    }
}

/// parse_releases parses portal Releases.aspx by COLUMN POSITION (the RadGrid header is
/// blank): code | name | status | start | end(+meeting) | closure (== Go ParseReleases).
pub fn parse_releases(html: &str) -> Vec<ReleaseMeta> {
    let doc = Html::parse_document(html);
    let mut out = Vec::new();
    for n in doc.tree.root().descendants() {
        if let Node::Element(e) = n.value() {
            if e.name() == "table" {
                for row in table_rows(n) {
                    if row.len() < 5 {
                        continue;
                    }
                    let code = row[0].trim().to_string();
                    if !code.starts_with("Rel-") {
                        continue;
                    }
                    let (freeze_date, freeze_meeting) = split_freeze(&row[4]);
                    out.push(ReleaseMeta {
                        code,
                        name: row[1].trim().to_string(),
                        status: row[2].trim().to_string(),
                        start_date: parse_date(&row[3]),
                        freeze_date,
                        freeze_meeting,
                    });
                }
            }
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_status_report() {
        let html = r#"<html><body>
            <a name="activeRel-19"></a>
            <table>
              <tr><th>Type</th><th>Spec num</th><th>Title</th><th>Vers</th><th>WG</th></tr>
              <tr><td>TS</td><td><a>23.501</a></td><td>System architecture</td><td>19.5.0</td><td>S2</td></tr>
              <tr><td>TR</td><td>23.700-01</td><td>Study on X</td><td>19.1.0</td><td>S2</td></tr>
            </table></body></html>"#;
        let (specs, vers) = parse_status_report(html);
        assert_eq!(specs.len(), 2);
        assert_eq!(specs[0].spec_id, "23.501");
        assert_eq!(specs[0].doc_type, "TS");
        assert_eq!(specs[0].title, "System architecture");
        assert_eq!(specs[0].working_group, "S2");
        assert_eq!(specs[0].series, "23");
        assert_eq!(vers.len(), 2);
        assert_eq!(
            vers[0],
            VersionRel {
                spec_id: "23.501".into(),
                release: "Rel-19".into(),
                version: "19.5.0".into()
            }
        );
    }

    #[test]
    fn parses_releases() {
        let html = r#"<html><body><table>
            <tr><td>Rel-19</td><td>Release 19</td><td>Open</td><td>2023-03-24</td><td>2025-06-20 (SA#108)</td><td></td></tr>
            <tr><td>junk</td><td>x</td><td>y</td><td>z</td><td>w</td></tr>
        </table></body></html>"#;
        let rels = parse_releases(html);
        assert_eq!(rels.len(), 1);
        assert_eq!(rels[0].code, "Rel-19");
        assert_eq!(rels[0].status, "Open");
        assert_eq!(rels[0].start_date.as_deref(), Some("2023-03-24"));
        assert_eq!(rels[0].freeze_date.as_deref(), Some("2025-06-20"));
        assert_eq!(rels[0].freeze_meeting, "SA#108");
    }
}
