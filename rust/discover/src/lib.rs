//! discover3gpp — pure logic of the CI-matrix discovery tool (Phase 9 port of
//! cmd/discover). Every function here mirrors its Go counterpart 1:1 and is locked
//! by a parity test that reuses the Go test's fixtures and expectations.
//!
//! Scope of THIS module: the self-contained delta/drift/worklist/ledger/sparse
//! logic that needs only the status report + the published indexes. The
//! build-identity drift and subject-delta signals (which need the embed/model/
//! subjectmeta/enrichmeta identities) are deliberately NOT here yet — they are a
//! follow-up once those identities are exposed Rust-side. Until then the Go
//! cmd/discover stays the production tool (this crate is additive, unwired).

use std::cmp::Ordering;
use std::collections::{BTreeMap, BTreeSet};

/// The 3GPP archive root; the per-spec directory + version-coded zip name
/// reconstruct the exact download URL corpus.sh's process_spec fetches.
pub const STATUS_BASE: &str = "https://www.3gpp.org/ftp/Specs/archive";

/// Legacy GSM Phase-1/2 series the modern status report omits (== Go
/// legacyGSMSeries). Scoped to "03" by design (only 03.071 is a stable doc).
pub const LEGACY_GSM_SERIES: &[&str] = &["03"];

/// parse_status turns status-report HTML into a "spec_id|Rel-NN" -> highest
/// version map, keeping the highest version per (spec, release). Reuses the shared
/// parse3gpp parser (== Go parseStatus over catalog.ParseStatusReport). Errors when
/// it parses zero rows (a layout change), exactly like the Go version.
pub fn parse_status(html: &str) -> Result<BTreeMap<String, String>, String> {
    let (_, vers) = parse3gpp::catalog::parse_status_report(html);
    let mut out: BTreeMap<String, String> = BTreeMap::new();
    for vr in vers {
        if vr.release.is_empty() || vr.version.is_empty() {
            continue;
        }
        let key = format!("{}|{}", vr.spec_id, vr.release);
        let cur = out.get(&key).map(String::as_str).unwrap_or("");
        if cmp_ver(&vr.version, cur) == Ordering::Greater {
            out.insert(key, vr.version);
        }
    }
    if out.is_empty() {
        return Err("parsed 0 (spec,release) rows from status report (layout changed?)".into());
    }
    Ok(out)
}

/// delta_series returns the set of 2-digit series needing (re)indexing, comparing
/// live site versions against the published index PER (spec, release). A missing
/// key (new release, or a lower-release maintenance bump never indexed) counts as
/// changed; the floor is applied per-key on the release (== Go deltaSeries).
pub fn delta_series(
    site: &BTreeMap<String, String>,
    idx: &BTreeMap<String, String>,
    floor_major: i64,
    full: bool,
) -> BTreeSet<String> {
    let mut series = BTreeSet::new();
    for (key, ver) in site {
        let (spec, rel) = split_key(key);
        if major(rel) < floor_major {
            continue;
        }
        let have = idx.get(key).map(String::as_str).unwrap_or("");
        if full || cmp_ver(ver, have) == Ordering::Greater {
            if let Some(s) = series_prefix(spec) {
                series.insert(s);
            }
        }
    }
    series
}

/// emit_worklist returns the fetch worklist "<release> <url> <name>" for every
/// (spec,release) the status report lists at/above the floor (by RELEASE, drafts
/// kept). Returns (lines, emitted, skipped_unencodable) (== Go emitWorklist).
pub fn emit_worklist(
    site: &BTreeMap<String, String>,
    floor_major: i64,
    series_filter: &str,
) -> (String, usize, usize) {
    let allow = series_set(series_filter);
    let mut lines = String::new();
    let (mut n, mut skipped) = (0usize, 0usize);
    for (key, ver) in site {
        let (spec, rel) = split_key(key);
        if rel.is_empty() || spec.len() < 2 || major(rel) < floor_major {
            continue;
        }
        let pfx = match series_prefix(spec) {
            Some(p) => p,
            None => continue,
        };
        if let Some(set) = &allow {
            if !set.contains(&pfx) {
                continue;
            }
        }
        match encode_ver_code(ver) {
            Some(code) => {
                let num = spec.replacen('.', "", 1);
                let name = format!("{num}-{code}.zip");
                let url = format!("{STATUS_BASE}/{pfx}_series/{spec}/{name}");
                lines.push_str(&format!("{rel} {url} {name}\n"));
                n += 1;
            }
            None => skipped += 1,
        }
    }
    (lines, n, skipped)
}

/// emit_draft_ledger returns absent-index-format JSON for every status-report key
/// at a DRAFT version (major < 3) and an in-scope release (== Go emitDraftLedger).
/// Built via serde_json so the output is always valid JSON.
pub fn emit_draft_ledger(
    site: &BTreeMap<String, String>,
    floor_major: i64,
    series_filter: &str,
) -> String {
    let allow = series_set(series_filter);
    let mut m = serde_json::Map::new();
    for (key, ver) in site {
        let (spec, rel) = split_key(key);
        if rel.is_empty() || spec.len() < 2 || major(rel) < floor_major {
            continue;
        }
        if let Some(set) = &allow {
            match series_prefix(spec) {
                Some(p) if set.contains(&p) => {}
                _ => continue,
            }
        }
        if major(ver) >= 3 {
            continue; // not a draft
        }
        m.insert(key.clone(), serde_json::Value::String(ver.clone()));
    }
    serde_json::to_string_pretty(&serde_json::Value::Object(m)).unwrap_or_else(|_| "{}".into())
}

/// dump_drift returns TSV rows "spec\trel\tsite_ver\tidx_ver\tstate" for every
/// (spec,release) the site has newer than the index, plus (total, missing, stale)
/// (== Go dumpDrift). Rows are sorted by (spec, release).
pub fn dump_drift(
    site: &BTreeMap<String, String>,
    idx: &BTreeMap<String, String>,
    floor_major: i64,
) -> (String, usize, usize) {
    let mut rows: Vec<(String, String, String, String, &str)> = Vec::new();
    let (mut missing, mut stale) = (0usize, 0usize);
    for (key, ver) in site {
        let (spec, rel) = split_key(key);
        if major(rel) < floor_major {
            continue;
        }
        let iv = idx.get(key).map(String::as_str).unwrap_or("");
        if cmp_ver(ver, iv) != Ordering::Greater {
            continue;
        }
        let state = if iv.is_empty() {
            missing += 1;
            "missing"
        } else {
            stale += 1;
            "stale"
        };
        rows.push((
            spec.to_string(),
            rel.to_string(),
            ver.clone(),
            iv.to_string(),
            state,
        ));
    }
    rows.sort_by(|a, b| a.0.cmp(&b.0).then(a.1.cmp(&b.1)));
    let mut out = String::new();
    for (spec, rel, sv, iv, state) in &rows {
        out.push_str(&format!("{spec}\t{rel}\t{sv}\t{iv}\t{state}\n"));
    }
    (out, missing, stale)
}

/// encode_ver_code turns "X.Y.Z" into the 3GPP archive version code: base36 per
/// component when all ≤35, else 6-digit zero-padded decimal when all ≤99, else
/// None (== Go encodeVerCode).
pub fn encode_ver_code(ver: &str) -> Option<String> {
    let parts: Vec<&str> = ver.splitn(3, '.').collect();
    let mut n = [0i64; 3];
    for (i, slot) in n.iter_mut().enumerate() {
        if let Some(p) = parts.get(i) {
            if !p.is_empty() {
                let v: i64 = p.parse().ok()?;
                if v < 0 {
                    return None;
                }
                *slot = v;
            }
        }
    }
    if n[0] <= 35 && n[1] <= 35 && n[2] <= 35 {
        let mut code = String::with_capacity(3);
        for &v in &n {
            let c = if v < 10 {
                (b'0' + v as u8) as char
            } else {
                (b'a' + (v - 10) as u8) as char
            };
            code.push(c);
        }
        return Some(code);
    }
    if n[0] <= 99 && n[1] <= 99 && n[2] <= 99 {
        return Some(format!("{:02}{:02}{:02}", n[0], n[1], n[2]));
    }
    None
}

/// series_set parses a "23 33" / "23,33" / "24|Rel-19" filter into 2-digit
/// prefixes. Empty input => None (no filter) (== Go seriesSet).
pub fn series_set(filter: &str) -> Option<BTreeSet<String>> {
    let filter = filter.trim();
    if filter.is_empty() {
        return None;
    }
    let mut set = BTreeSet::new();
    for tok in filter.split([' ', ',']) {
        let tok = match tok.find('|') {
            Some(i) => &tok[..i],
            None => tok,
        };
        if tok.len() >= 2 {
            set.insert(tok[..2].to_string());
        }
    }
    if set.is_empty() {
        None
    } else {
        Some(set)
    }
}

/// split_key splits "spec_id|Rel-NN" into parts; a key without '|' yields an empty
/// release (== Go splitKey).
pub fn split_key(key: &str) -> (&str, &str) {
    match key.find('|') {
        Some(i) => (&key[..i], &key[i + 1..]),
        None => (key, ""),
    }
}

/// series_prefix returns the 2-char series of a "SS.NNN" spec id, or None if too short.
fn series_prefix(spec: &str) -> Option<String> {
    if spec.len() >= 2 {
        Some(spec[..2].to_string())
    } else {
        None
    }
}

/// series_in_index reports whether the index holds at least one spec of the
/// 2-digit series (== Go seriesInIndex).
pub fn series_in_index(idx: &BTreeMap<String, String>, series: &str) -> bool {
    idx.keys().any(|key| {
        let (spec, _) = split_key(key);
        spec.len() >= 2 && &spec[..2] == series
    })
}

/// load_index parses a "spec|Rel -> version" JSON map; missing/unreadable/malformed
/// => empty (== Go loadIndex). Path "" => empty.
pub fn load_index(path: &str) -> BTreeMap<String, String> {
    if path.is_empty() {
        return BTreeMap::new();
    }
    match std::fs::read(path) {
        Ok(b) => serde_json::from_slice(&b).unwrap_or_default(),
        Err(_) => BTreeMap::new(),
    }
}

/// load_merged_index merges comma-separated ledger files, highest version winning;
/// a missing file is skipped (== Go loadMergedIndex).
pub fn load_merged_index(spec: &str) -> BTreeMap<String, String> {
    let mut out: BTreeMap<String, String> = BTreeMap::new();
    for p in spec.split(',') {
        let p = p.trim();
        if p.is_empty() {
            continue;
        }
        for (k, v) in load_index(p) {
            let cur = out.get(&k).map(String::as_str).unwrap_or("");
            if cmp_ver(&v, cur) == Ordering::Greater {
                out.insert(k, v);
            }
        }
    }
    out
}

/// sparse_needed reports whether a sparse-only pass should run: only when the build
/// is sparse-capable (expected != "") AND the published layer differs (== Go).
pub fn sparse_needed(expected: &str, published: &str) -> bool {
    !expected.is_empty() && expected != published
}

/// load_sparse_model_id reads {"sparse_model":"<id>"}; missing/malformed => ""
/// (== Go loadSparseModelID).
pub fn load_sparse_model_id(path: &str) -> String {
    if path.is_empty() {
        return String::new();
    }
    let Ok(b) = std::fs::read(path) else {
        return String::new();
    };
    let v: serde_json::Value = match serde_json::from_slice(&b) {
        Ok(v) => v,
        Err(_) => return String::new(),
    };
    v.get("sparse_model")
        .and_then(|x| x.as_str())
        .unwrap_or("")
        .to_string()
}

/// major returns the leading integer of "Rel-19" or "19.6.0" (0 on parse error).
/// Special case: Rel-99 IS version major 3 in 3GPP's scheme (== Go major).
pub fn major(s: &str) -> i64 {
    if s == "Rel-99" {
        return 3;
    }
    let s = s.strip_prefix("Rel-").unwrap_or(s);
    let head = s.split(['.', '-']).next().unwrap_or("");
    head.parse().unwrap_or(0)
}

/// cmp_ver compares "X.Y.Z" numerically; empty sorts lowest (== Go cmpVer).
pub fn cmp_ver(a: &str, b: &str) -> Ordering {
    triple(a).cmp(&triple(b))
}

fn triple(s: &str) -> [i64; 3] {
    let mut t = [0i64; 3];
    if s.is_empty() {
        return t;
    }
    for (i, p) in s.splitn(3, '.').enumerate() {
        if i > 2 {
            break;
        }
        t[i] = p.parse().unwrap_or(0);
    }
    t
}

/// BuildIndex holds the three canonical identities published alongside the corpus
/// (== Go model.BuildIndex). A drift in any of them is corpus-global: discover
/// forces every above-floor series back into the matrix so the affected refresh
/// runs even though no spec version moved.
#[derive(Debug, Default, Clone, PartialEq, Eq, serde::Deserialize)]
pub struct BuildIndex {
    #[serde(default)]
    pub spec_ingest_identity: String,
    #[serde(default)]
    pub global_enrichment_identity: String,
    #[serde(default)]
    pub embed_identity: String,
}

/// current_footprints returns all subjects at their CURRENT code footprint (every
/// series advanced), sorted — the input the current code would stamp (== Go
/// subjectmeta.IngestFootprints). Computed from the shared, golden-matched
/// identity3gpp::SUBJECTS so it can never drift from the write-side.
pub fn current_footprints() -> Vec<String> {
    let all: BTreeSet<String> = identity3gpp::SUBJECTS
        .iter()
        .flat_map(|(_, _, _, series)| series.iter().map(|s| s.to_string()))
        .collect();
    identity3gpp::effective_subject_footprints(&all, &std::collections::HashMap::new())
        .into_iter()
        .map(|(_, fp)| fp)
        .collect()
}

/// current_build_index composes the three identities for the CURRENT code (== Go
/// model.CurrentBuildIndex). `model_id` is the resolved embedder model id the caller
/// supplies (Go resolves it from --embed-model; "" compares against an empty embed
/// identity). The digests come from identity3gpp, golden-matched to the Go side.
pub fn current_build_index(model_id: &str) -> BuildIndex {
    BuildIndex {
        spec_ingest_identity: identity3gpp::spec_ingest_identity(&current_footprints()),
        global_enrichment_identity: identity3gpp::global_enrichment_identity(),
        embed_identity: identity3gpp::embed_identity(model_id),
    }
}

/// build_index_differs returns the names of the identities in `published` that
/// disagree with `current` (== Go BuildIndex.Differs). Empty = up to date.
pub fn build_index_differs(published: &BuildIndex, current: &BuildIndex) -> Vec<String> {
    let mut out = Vec::new();
    if published.spec_ingest_identity != current.spec_ingest_identity {
        out.push("spec_ingest_identity".to_string());
    }
    if published.global_enrichment_identity != current.global_enrichment_identity {
        out.push("global_enrichment_identity".to_string());
    }
    if published.embed_identity != current.embed_identity {
        out.push("embed_identity".to_string());
    }
    out
}

/// load_build_index reads build-index.json; a missing/unreadable/malformed file
/// yields the default (all-empty) BuildIndex, which Differs reports as a full drift
/// — so a legacy publish with no build index self-heals on the next discover
/// (== Go loadBuildIndex).
pub fn load_build_index(path: &str) -> BuildIndex {
    if path.is_empty() {
        return BuildIndex::default();
    }
    match std::fs::read(path) {
        Ok(b) => serde_json::from_slice(&b).unwrap_or_default(),
        Err(_) => BuildIndex::default(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    // Mirrors internal/catalog/testdata layout: 23.501 in Rel-19/18/16 so a LOWER
    // release bump is observable while a HIGHER release stays put.
    const STATUS_HTML: &str = r#"<!DOCTYPE html><html><body>
<a name="activeRel-19"></a>
<table>
  <tr><th>type</th><th>spec&nbsp;num</th><th>title</th><th>vers</th><th>WG</th></tr>
  <tr><td>TS</td><td><a href="/x">23.501</a></td><td>5GS arch</td><td>19.7.0</td><td>S2</td></tr>
</table>
<a name="activeRel-18"></a>
<table>
  <tr><th>type</th><th>spec&nbsp;num</th><th>title</th><th>vers</th><th>WG</th></tr>
  <tr><td>TS</td><td><a href="/x">23.501</a></td><td>5GS arch</td><td>18.13.0</td><td>S2</td></tr>
</table>
<a name="deadRel-16"></a>
<table>
  <tr><th>type</th><th>spec&nbsp;num</th><th>title</th><th>vers</th><th>WG</th></tr>
  <tr><td>TS</td><td><a href="/x">23.501</a></td><td>5GS arch</td><td>16.16.0</td><td>S2</td></tr>
</table>
</body></html>"#;

    fn idx(pairs: &[(&str, &str)]) -> BTreeMap<String, String> {
        pairs
            .iter()
            .map(|(k, v)| (k.to_string(), v.to_string()))
            .collect()
    }

    #[test]
    fn parse_status_per_spec_release() {
        let site = parse_status(STATUS_HTML).expect("parse");
        let want = idx(&[
            ("23.501|Rel-19", "19.7.0"),
            ("23.501|Rel-18", "18.13.0"),
            ("23.501|Rel-16", "16.16.0"),
        ]);
        assert_eq!(site, want);
    }

    #[test]
    fn delta_lower_release_bump_detected() {
        let site = parse_status(STATUS_HTML).unwrap();
        let i = idx(&[
            ("23.501|Rel-19", "19.7.0"),
            ("23.501|Rel-18", "18.13.0"),
            ("23.501|Rel-16", "16.15.0"), // site is 16.16.0 -> changed
        ]);
        let got: Vec<String> = delta_series(&site, &i, major("Rel-4"), false)
            .into_iter()
            .collect();
        assert_eq!(got, vec!["23".to_string()]);
    }

    #[test]
    fn delta_nothing_changed_empty() {
        let site = parse_status(STATUS_HTML).unwrap();
        let i = idx(&[
            ("23.501|Rel-19", "19.7.0"),
            ("23.501|Rel-18", "18.13.0"),
            ("23.501|Rel-16", "16.16.0"),
        ]);
        assert!(delta_series(&site, &i, major("Rel-4"), false).is_empty());
    }

    #[test]
    fn delta_new_release_detected() {
        let site = parse_status(STATUS_HTML).unwrap();
        let i = idx(&[("23.501|Rel-19", "19.7.0"), ("23.501|Rel-18", "18.13.0")]);
        let got: Vec<String> = delta_series(&site, &i, major("Rel-4"), false)
            .into_iter()
            .collect();
        assert_eq!(got, vec!["23".to_string()]);
    }

    #[test]
    fn delta_floor_per_release() {
        let site = parse_status(STATUS_HTML).unwrap();
        let i = idx(&[
            ("23.501|Rel-19", "19.7.0"),
            ("23.501|Rel-18", "18.13.0"),
            ("23.501|Rel-16", "16.15.0"), // bumped but below a Rel-17 floor
        ]);
        assert!(delta_series(&site, &i, major("Rel-17"), false).is_empty());
    }

    #[test]
    fn legacy_gsm_series_pinned() {
        assert!(!LEGACY_GSM_SERIES.is_empty());
        let mut seen = BTreeSet::new();
        for s in LEGACY_GSM_SERIES {
            assert_eq!(s.len(), 2, "legacy series {s} not 2 digits");
            assert!(*s < "21", "legacy series {s} in the modern range");
            assert!(seen.insert(*s), "duplicate legacy series {s}");
        }
    }

    #[test]
    fn series_in_index_gate() {
        let i = idx(&[("23.501|Rel-18", "18.5.0"), ("03.88|GSM", "5.0.0")]);
        assert!(series_in_index(&i, "03"));
        assert!(series_in_index(&i, "23"));
        assert!(!series_in_index(&i, "04"));
        assert!(!series_in_index(&BTreeMap::new(), "03"));
    }

    #[test]
    fn encode_ver_code_cases() {
        let cases = [
            ("1.0.0", Some("100")),
            ("0.7.0", Some("070")),
            ("1.1.1", Some("111")),
            ("17.6.0", Some("h60")),
            ("10.5.0", Some("a50")),
            ("35.35.35", Some("zzz")),
            ("3.0.0", Some("300")),
            ("19.0", Some("j00")),
            ("8.37.0", Some("083700")),
            ("1.62.0", Some("016200")),
            ("36.0.0", Some("360000")),
            ("99.99.99", Some("999999")),
            ("100.0.0", None),
            ("x.0.0", None),
        ];
        for (inp, want) in cases {
            assert_eq!(
                encode_ver_code(inp).as_deref(),
                want,
                "encode_ver_code({inp})"
            );
        }
    }

    #[test]
    fn series_set_parses() {
        assert!(series_set("").is_none());
        let s = series_set("23 33").unwrap();
        assert!(s.contains("23") && s.contains("33") && s.len() == 2);
        let s = series_set("24|Rel-19,29").unwrap();
        assert!(s.contains("24") && s.contains("29") && s.len() == 2);
    }

    #[test]
    fn absent_ledger_stops_reflag() {
        let floor = major("Rel-99"); // 3
        let site = idx(&[("21.100|Rel-19", "19.6.0")]);
        let have = idx(&[("21.100|Rel-19", "19.6.0")]);
        assert!(delta_series(&site, &have, floor, false).is_empty());
        let have = idx(&[("21.100|Rel-19", "19.5.0")]);
        assert!(delta_series(&site, &have, floor, false).contains("21"));
    }

    #[test]
    fn emit_draft_ledger_selects_drafts_only() {
        let site = idx(&[
            ("21.802|Rel-20", "1.1.1"),
            ("21.919|Rel-19", "2.0.0"),
            ("23.501|Rel-19", "19.6.0"),
            ("24.008|Rel-4", "4.8.0"),
        ]);
        let out = emit_draft_ledger(&site, major("Rel-99"), "");
        let m: BTreeMap<String, String> = serde_json::from_str(&out).expect("valid JSON");
        assert_eq!(m.len(), 2, "exactly the 2 draft keys: {m:?}");
        assert_eq!(m.get("21.802|Rel-20").map(String::as_str), Some("1.1.1"));
        assert_eq!(m.get("21.919|Rel-19").map(String::as_str), Some("2.0.0"));
        assert!(!m.contains_key("23.501|Rel-19"));
    }

    #[test]
    fn draft_ledger_stops_reflag() {
        let floor = major("Rel-99");
        let mut site = idx(&[("21.802|Rel-20", "1.1.1")]);
        let have = idx(&[("21.802|Rel-20", "1.1.1")]);
        assert!(delta_series(&site, &have, floor, false).is_empty());
        site.insert("21.802|Rel-20".into(), "1.2.0".into());
        assert!(delta_series(&site, &have, floor, false).contains("21"));
    }

    #[test]
    fn sparse_needed_cases() {
        assert!(!sparse_needed("", ""));
        assert!(!sparse_needed("", "x"));
        assert!(sparse_needed("abc123", ""));
        assert!(sparse_needed("abc123", "old999"));
        assert!(!sparse_needed("abc123", "abc123"));
    }

    #[test]
    fn build_index_differs_reports_each() {
        let cur = current_build_index("");
        // A published index equal to current => no drift.
        assert!(build_index_differs(&cur, &cur).is_empty());
        // A stale enrichment identity => exactly that name.
        let mut pub_stale = cur.clone();
        pub_stale.global_enrichment_identity = "stale00000000".into();
        assert_eq!(
            build_index_differs(&pub_stale, &cur),
            vec!["global_enrichment_identity".to_string()]
        );
        // All three stale => all three names, in order.
        let allstale = BuildIndex::default();
        assert_eq!(
            build_index_differs(&allstale, &cur),
            vec![
                "spec_ingest_identity".to_string(),
                "global_enrichment_identity".to_string(),
                "embed_identity".to_string(),
            ]
        );
    }

    #[test]
    fn current_build_index_matches_identity_goldens() {
        // The composed identities must equal the golden digests pinned in
        // identity3gpp (which are byte-matched to the Go cmd/merge output).
        let cur = current_build_index("");
        assert_eq!(cur.global_enrichment_identity, "5fb3a6c87488");
        assert_eq!(cur.embed_identity, "2d36b4425d54");
        // spec_ingest is the all-advanced-footprints digest — recompute and compare.
        assert_eq!(
            cur.spec_ingest_identity,
            identity3gpp::spec_ingest_identity(&current_footprints())
        );
    }

    #[test]
    fn load_build_index_missing_is_full_drift() {
        let bi = load_build_index("");
        assert_eq!(bi, BuildIndex::default());
        // default vs current => all three drift (legacy publish self-heals).
        assert_eq!(build_index_differs(&bi, &current_build_index("")).len(), 3);
    }

    #[test]
    fn load_merged_index_highest_wins() {
        let dir = std::env::temp_dir().join(format!("disc_{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let a = dir.join("a.json");
        let b = dir.join("b.json");
        std::fs::write(&a, r#"{"21.100|Rel-19":"19.5.0","30.531|Rel-18":"18.0.0"}"#).unwrap();
        std::fs::write(&b, r#"{"21.100|Rel-19":"19.6.0","21.802|Rel-20":"1.1.1"}"#).unwrap();
        let spec = format!(
            "{},{},{}",
            a.display(),
            b.display(),
            dir.join("missing.json").display()
        );
        let m = load_merged_index(&spec);
        assert_eq!(m.get("21.100|Rel-19").map(String::as_str), Some("19.6.0"));
        assert_eq!(m.get("30.531|Rel-18").map(String::as_str), Some("18.0.0"));
        assert_eq!(m.get("21.802|Rel-20").map(String::as_str), Some("1.1.1"));
        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn load_sparse_model_id_cases() {
        assert_eq!(load_sparse_model_id(""), "");
        let dir = std::env::temp_dir().join(format!("disc_sp_{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        assert_eq!(
            load_sparse_model_id(dir.join("nope.json").to_str().unwrap()),
            ""
        );
        let good = dir.join("sparse-index.json");
        std::fs::write(&good, r#"{"sparse_model":"deadbeef12"}"#).unwrap();
        assert_eq!(load_sparse_model_id(good.to_str().unwrap()), "deadbeef12");
        let bad = dir.join("bad.json");
        std::fs::write(&bad, "not json").unwrap();
        assert_eq!(load_sparse_model_id(bad.to_str().unwrap()), "");
        let _ = std::fs::remove_dir_all(&dir);
    }
}
