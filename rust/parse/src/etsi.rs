//! etsi — Rust port of the ETSI provenance metadata derivation (internal/htmlparse
//! metaFromETSIHeader + internal/model EtsiDeliverURL). ETSI deliverables have no 3GPP
//! "<num>-<code>" filename, so the SDO/id/version travel in an HTML comment the convert
//! pipeline prepends ("<!-- ETSI-SPEC: 103 221-1 | 1.21.1 -->"); the same parser → ingest
//! path then serves both corpora. Citation URL is the deterministic deliver-archive PDF.

use crate::SpecMeta;
use regex::Regex;
use std::sync::OnceLock;

fn re_header() -> &'static Regex {
    static R: OnceLock<Regex> = OnceLock::new();
    R.get_or_init(|| {
        Regex::new(r"<!--\s*ETSI-SPEC:\s*([0-9][0-9 ]+[0-9](?:-[0-9]+)?)\s*\|\s*([0-9]+\.[0-9]+\.[0-9]+)\s*-->").unwrap()
    })
}
fn re_id() -> &'static Regex {
    static R: OnceLock<Regex> = OnceLock::new();
    R.get_or_init(|| {
        Regex::new(r"^(?:ETSI\s+)?(?:T[SR]\s+)?(1\d{2})\s*(\d{3})(?:-(\d+))?$").unwrap()
    })
}
fn re_ws() -> &'static Regex {
    static R: OnceLock<Regex> = OnceLock::new();
    R.get_or_init(|| Regex::new(r"[ \t\x{00a0}]+").unwrap())
}

/// etsi_number_token maps "103 221-1" → ("10322101","103221") (== Go etsiNumberToken).
fn etsi_number_token(id: &str) -> Option<(String, String)> {
    let m = re_id().captures(id.trim())?;
    let base6 = format!("{}{}", &m[1], &m[2]);
    let mut token = base6.clone();
    if let Some(p) = m.get(3) {
        let n: i32 = p.as_str().parse().ok()?;
        token += &format!("{n:02}");
    }
    Some((token, base6))
}

/// parse_etsi_version splits "1.21.1" / "V1.21.1" → (1,21,1) (== Go parseEtsiVersion).
fn parse_etsi_version(version: &str) -> Option<(i32, i32, i32)> {
    let v = version.trim().trim_start_matches('V');
    let p: Vec<&str> = v.split('.').collect();
    if p.len() != 3 {
        return None;
    }
    Some((p[0].parse().ok()?, p[1].parse().ok()?, p[2].parse().ok()?))
}

/// etsi_deliver_url reconstructs the deterministic ETSI deliver-archive PDF URL (== Go
/// EtsiDeliverURL). Falls back to the landing folder when the version is unparseable.
pub fn etsi_deliver_url(id: &str, version: &str) -> String {
    let Some((token, base6)) = etsi_number_token(id) else {
        return String::new();
    };
    let Ok(n) = base6.parse::<i32>() else {
        return String::new();
    };
    let floor = (n / 100) * 100;
    let range = format!("{floor:06}_{:06}", floor + 99);
    let base = format!("https://www.etsi.org/deliver/etsi_ts/{range}/{token}");
    match parse_etsi_version(version) {
        Some((maj, mnr, ed)) => {
            format!("{base}/{maj:02}.{mnr:02}.{ed:02}_60/ts_{token}v{maj:02}{mnr:02}{ed:02}p.pdf")
        }
        None => format!("{base}/"),
    }
}

/// parse_etsi_meta derives a SpecMeta from the in-body ETSI provenance header (== Go
/// metaFromETSIHeader). None when the header is absent.
pub fn parse_etsi_meta(html: &str) -> Option<SpecMeta> {
    let m = re_header().captures(html)?;
    let id = re_ws().replace_all(m[1].trim(), " ").to_string();
    let version = m[2].to_string();
    let spec_id = format!("ETSI TS {id}");
    let docx_url = etsi_deliver_url(&id, &version);
    Some(SpecMeta {
        spec_id: spec_id.clone(),
        series: "ETSI".to_string(),
        doc_type: "TS".to_string(),
        working_group: "ETSI".to_string(),
        release: "ETSI".to_string(),
        version,
        docx_url,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn deliver_url_matches_go() {
        assert_eq!(
            etsi_deliver_url("103 221-1", "1.21.1"),
            "https://www.etsi.org/deliver/etsi_ts/103200_103299/10322101/01.21.01_60/ts_10322101v012101p.pdf"
        );
    }

    #[test]
    fn parses_etsi_header() {
        let html =
            r#"<!-- ETSI-SPEC: 103 221-1 | 1.21.1 --><html><body><h1>1 Scope</h1></body></html>"#;
        let m = parse_etsi_meta(html).unwrap();
        assert_eq!(m.spec_id, "ETSI TS 103 221-1");
        assert_eq!(m.series, "ETSI");
        assert_eq!(m.release, "ETSI");
        assert_eq!(m.version, "1.21.1");
        assert!(m.docx_url.ends_with("ts_10322101v012101p.pdf"));
        assert!(parse_etsi_meta("<html>no header</html>").is_none());
    }
}
