//! etsi — Rust port of the ETSI provenance metadata derivation (internal/htmlparse
//! metaFromETSIHeader + internal/model EtsiDeliverURL). ETSI deliverables have no 3GPP
//! "<num>-<code>" filename, so the SDO/id/version travel in an HTML comment the convert
//! pipeline prepends ("<!-- ETSI-SPEC: 103 221-1 | 1.21.1 -->"); the same parser → ingest
//! path then serves both corpora. Citation URL is the deterministic deliver-archive PDF.

use crate::SpecMeta;
use regex::Regex;
use std::sync::OnceLock;

/// The provenance header the convert pipeline prepends, with an OPTIONAL third
/// field carrying the document type:
///
///   <!-- ETSI-SPEC: 103 221-1 | 1.21.1 -->        (older: TS by definition)
///   <!-- ETSI-SPEC: 103 101 | 1.1.1 | TR -->      (typed)
///
/// The field is optional so already-converted HTML on disk keeps parsing; every
/// such file was produced by a TS-only crawl, which is exactly what the default is.
fn re_header() -> &'static Regex {
    static R: OnceLock<Regex> = OnceLock::new();
    R.get_or_init(|| {
        Regex::new(
            r"<!--\s*ETSI-SPEC:\s*([0-9][0-9 ]+[0-9](?:-[0-9]+)*)\s*\|\s*([0-9]+\.[0-9]+\.[0-9]+)\s*(?:\|\s*(TS|TR|EN)\s*)?-->",
        )
        .unwrap()
    })
}
/// re_id parses an ARCHIVE id. Deliberately looser than the Go RECOGNISER
/// (model.reEtsiID, anchored on 1NN): ETSI ENs are numbered 3NN NNN, so a strict
/// 1NN pattern silently produced an EMPTY citation URL for every EN. Mirrors
/// model.reEtsiArchiveID, including multi-part ids.
fn re_id() -> &'static Regex {
    static R: OnceLock<Regex> = OnceLock::new();
    R.get_or_init(|| {
        Regex::new(r"^(?:ETSI\s+)?(?:T[SR]|EN\s+)?\s*(\d{3})\s*(\d{3})((?:-\d+)*)$").unwrap()
    })
}
fn re_ws() -> &'static Regex {
    static R: OnceLock<Regex> = OnceLock::new();
    R.get_or_init(|| Regex::new(r"[ \t\x{00a0}]+").unwrap())
}

/// etsi_number_token maps "103 221-1" → ("10322101","103221") (== Go
/// etsiArchiveToken). Multi-part ids chain their 2-digit parts.
fn etsi_number_token(id: &str) -> Option<(String, String)> {
    let m = re_id().captures(id.trim())?;
    let base6 = format!("{}{}", &m[1], &m[2]);
    let mut token = base6.clone();
    if let Some(parts) = m.get(3) {
        for part in parts.as_str().split('-').filter(|s| !s.is_empty()) {
            let n: i32 = part.parse().ok()?;
            if !(0..=99).contains(&n) {
                return None;
            }
            token += &format!("{n:02}");
        }
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

/// etsi_deliver_url reconstructs the deterministic ETSI deliver-archive PDF URL for
/// a TS (== Go EtsiDeliverURL).
pub fn etsi_deliver_url(id: &str, version: &str) -> String {
    etsi_deliver_url_in("TS", id, version)
}

/// etsi_deliver_url_in is the same for a given document type (== Go
/// EtsiDeliverURLIn). The type picks BOTH the /deliver tree and the file-name
/// prefix; a TR's URL built under etsi_ts with a ts_ prefix is a 404, not a
/// near-miss, so a citation built the old way pointed at nothing for every
/// non-TS deliverable. Falls back to the landing folder when the version is
/// unparseable (cite-the-pointer, never a fabricated version).
pub fn etsi_deliver_url_in(doc_type: &str, id: &str, version: &str) -> String {
    let dir = match doc_type {
        "TR" => "etsi_tr",
        "EN" => "etsi_en",
        _ => "etsi_ts",
    };
    let prefix = dir.trim_start_matches("etsi_");
    let Some((token, base6)) = etsi_number_token(id) else {
        return String::new();
    };
    let Ok(n) = base6.parse::<i32>() else {
        return String::new();
    };
    let floor = (n / 100) * 100;
    let range = format!("{floor:06}_{:06}", floor + 99);
    let base = format!("https://www.etsi.org/deliver/{dir}/{range}/{token}");
    match parse_etsi_version(version) {
        Some((maj, mnr, ed)) => format!(
            "{base}/{maj:02}.{mnr:02}.{ed:02}_60/{prefix}_{token}v{maj:02}{mnr:02}{ed:02}p.pdf"
        ),
        None => format!("{base}/"),
    }
}

/// parse_etsi_meta derives a SpecMeta from the in-body ETSI provenance header (== Go
/// metaFromETSIHeader). None when the header is absent.
pub fn parse_etsi_meta(html: &str) -> Option<SpecMeta> {
    let m = re_header().captures(html)?;
    let id = re_ws().replace_all(m[1].trim(), " ").to_string();
    let version = m[2].to_string();
    // The document type used to be the literal "TS" for every deliverable, so a
    // widened crawl would have filed TR 103 101 as "ETSI TS 103 101" — an id that
    // resolves to nothing, in a corpus whose whole product is provenance.
    let doc_type = m.get(3).map_or("TS", |g| g.as_str()).to_string();
    let spec_id = format!("ETSI {doc_type} {id}");
    let docx_url = etsi_deliver_url_in(&doc_type, &id, &version);
    Some(SpecMeta {
        spec_id: spec_id.clone(),
        series: "ETSI".to_string(),
        doc_type,
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

    /// The typed header: a TR must be filed as a TR, in the TR tree, with the tr_
    /// file prefix. Verified against the live archive — /deliver/etsi_ts/103100_
    /// 103199/103101/ is a 404 while the etsi_tr path is TR 103 101.
    #[test]
    fn parses_typed_header_for_tr_and_en() {
        let tr = parse_etsi_meta(
            r#"<!-- ETSI-SPEC: 103 101 | 1.1.1 | TR --><html><body><h1>1 Scope</h1></body></html>"#,
        )
        .unwrap();
        assert_eq!(tr.spec_id, "ETSI TR 103 101");
        assert_eq!(tr.doc_type, "TR");
        assert_eq!(
            tr.docx_url,
            "https://www.etsi.org/deliver/etsi_tr/103100_103199/103101/01.01.01_60/tr_103101v010101p.pdf"
        );

        // ENs are numbered 3NN NNN, which the 1NN-anchored id pattern rejected —
        // every EN would have carried an EMPTY citation URL.
        let en = parse_etsi_meta(
            r#"<!-- ETSI-SPEC: 301 893 | 2.2.1 | EN --><html><body><h1>1 Scope</h1></body></html>"#,
        )
        .unwrap();
        assert_eq!(en.spec_id, "ETSI EN 301 893");
        assert_eq!(en.doc_type, "EN");
        assert_eq!(
            en.docx_url,
            "https://www.etsi.org/deliver/etsi_en/301800_301899/301893/02.02.01_60/en_301893v020201p.pdf"
        );
    }

    /// An untyped header is what every already-converted file on disk carries, and
    /// they were all produced by a TS-only crawl: it must keep parsing, as TS.
    #[test]
    fn untyped_header_still_parses_as_ts() {
        let m = parse_etsi_meta(r#"<!-- ETSI-SPEC: 103 280 | 2.19.1 --><html></html>"#).unwrap();
        assert_eq!(m.spec_id, "ETSI TS 103 280");
        assert_eq!(m.doc_type, "TS");
    }

    /// Multi-part ids chain their parts, matching etsicat::TokenToID's "-P-Q".
    #[test]
    fn multi_part_ids_chain() {
        assert!(etsi_deliver_url("102 232-3", "3.16.1").ends_with("ts_10223203v031601p.pdf"));
        assert_eq!(
            etsi_number_token("102 232-3-1"),
            Some(("1022320301".to_string(), "102232".to_string()))
        );
    }
}
