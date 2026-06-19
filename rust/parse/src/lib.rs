//! parse3gpp — Rust port of the 3GPP spec parser (internal/htmlparse + internal/model
//! helpers), Phase 4 of the write-side migration. This first increment is the
//! PARITY-CRITICAL filename→metadata derivation: a wrong version-code decode mis-files a
//! spec under the wrong release and `discover` then re-flags that (spec, release) key
//! forever, so every function here mirrors its Go counterpart exactly and is golden-tested
//! against known Go outputs. The HTML clause walker lands on top of this seam next.

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
}
