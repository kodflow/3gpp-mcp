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
        // "Changed" means "we do not hold this document", and filing_release decides
        // WHERE we would hold it — so the comparison belongs there, exactly as in
        // emit_repair_worklist. A carrying row the corpus can never mirror (26.510
        // listed at 18.4.0 under Rel-20, the file landing under Rel-18, so no Rel-20
        // key is ever written) would otherwise read as a changed series on EVERY
        // build for ever, and put its whole series back in the matrix each time.
        let have = match filing_release(site, spec, rel, ver, floor_major) {
            Some(own) => idx
                .get(&format!("{spec}|{own}"))
                .map(String::as_str)
                .unwrap_or(""),
            None => idx.get(key).map(String::as_str).unwrap_or(""),
        };
        if full || cmp_ver(ver, have) == Ordering::Greater {
            if let Some(s) = series_prefix(spec) {
                series.insert(s);
            }
        }
    }
    series
}

/// WorklistCounts is what emit_worklist DID, not merely how many lines came out.
/// Every term is about a line that was emitted or a reason one was not, so
///     keys in scope = emitted + unencodable + deduped
/// and `refiled` counts emitted lines, never attempts: reporting a re-filing that
/// was then deduplicated away would claim work the work list does not contain.
#[derive(Debug, Default, Clone, PartialEq, Eq)]
pub struct WorklistCounts {
    pub emitted: usize,
    pub unencodable: usize,
    /// Lines emitted under the release the version's own major names instead of the
    /// key's release — see filing_release.
    pub refiled: usize,
    /// Lines a re-filing made identical to one already emitted, and so dropped.
    pub deduped: usize,
}

/// emit_worklist returns the fetch worklist "<release> <url> <name>" for every
/// (spec,release) the status report lists at/above the floor (by RELEASE, drafts
/// kept), and what it did — see WorklistCounts.
pub fn emit_worklist(
    site: &BTreeMap<String, String>,
    floor_major: i64,
    series_filter: &str,
) -> (String, WorklistCounts) {
    let allow = series_set(series_filter);
    let mut lines = String::new();
    let mut counts = WorklistCounts::default();
    // Re-filing can send two keys of the same spec to the same release with the same
    // version — 33.816 is listed at 10.0.0 under both Rel-10 and Rel-11 — and the two
    // then render the identical line. corpus.sh would fetch it twice; dedupe here.
    let mut seen: BTreeSet<String> = BTreeSet::new();
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
        let mut file_under = rel;
        let owned;
        let refiled = match filing_release(site, spec, rel, ver, floor_major) {
            Some(own) => {
                owned = own;
                file_under = &owned;
                true
            }
            None => false,
        };
        match encode_ver_code(ver) {
            Some(code) => {
                let num = spec.replacen('.', "", 1);
                let name = format!("{num}-{code}.zip");
                let url = format!("{STATUS_BASE}/{pfx}_series/{spec}/{name}");
                let line = format!("{file_under} {url} {name}\n");
                if seen.insert(line.clone()) {
                    lines.push_str(&line);
                    counts.emitted += 1;
                    if refiled {
                        counts.refiled += 1;
                    }
                } else {
                    counts.deduped += 1;
                }
            }
            None => counts.unencodable += 1,
        }
    }
    (lines, counts)
}

/// A DOCUMENT IS ACQUIRED UNDER THE RELEASE ITS OWN VERSION NAMES.
///
/// The 3GPP archive URL carries no release at all —
/// `…/archive/26_series/26.510/26510-i40.zip` — so `spec_versions.release` is
/// decided HERE and nowhere else: the release SECTION of the DynaReport status
/// report becomes this worklist's first column, becomes the
/// `data/sources/convert/<Rel-NN>/` directory (scripts/corpus.sh), and
/// `parse_filename_meta` reads it straight back off the parent directory. Whatever
/// this function lets through is what the corpus will say and what the server will
/// cite.
///
/// The report's per-release sections CARRY A VERSION FORWARD when a release has not
/// re-issued a spec — `cmd/anchorcheck` calls the same thing by name: "3GPP
/// routinely lists a spec's Rel-N entry at the Rel-(N-1) version, so this is
/// bookkeeping, not a gap" (its NonContent verdict). Bookkeeping is all it is: the
/// DOCUMENT still belongs to the release its version number names, and when the
/// report files the same spec under that release too, that is where the file must
/// be acquired. Acquiring it under the carrying release instead gives the WRONG
/// release the only copy of the text.
///
/// Measured on the published corpus, 2026-09-12: 74 (spec, release, version) rows
/// have a release the version's major contradicts. 51 are Rel-4 rows holding a
/// 3.x.y Rel-99 document, and today's report still says so — the corpus has no
/// Rel-99 section at all, so Rel-4 is their only home and they are left alone by
/// conditions 2 and 3 below. 16 are Rel-20 rows that today's report
/// does not carry at all, and 12 of those hold 4 112 clauses of Rel-18/Rel-19 text
/// that exists NOWHERE ELSE in the corpus: `get_spec(26.510, release=Rel-20)`
/// serves 364 clauses of a document whose own cover says Release 18. Those 12 were
/// acquired by the holes loop of emit_repair_worklist, which took the corpus's own
/// key as proof of the release.
///
/// Returns the release the document belongs to, when ALL of these hold — and every
/// one of the three refusals below exists to make sure re-filing can only ever move
/// a document, never lose one:
///
///  1. the version is a published one. A DRAFT (major < 3) is legitimately older
///     than the release it is drafted for: today's report carries 23.873 Rel-5
///     2.0.0, 33.900 Rel-5 0.4.1 and 36.833-1 Rel-13 0.4.0, and "Rel-0" is not a
///     release. 3GPP has no Rel-0/1/2 section, so conditions 2 and 3 would refuse
///     these anyway; the check is stated rather than relied upon, because what
///     protects them must not be an accident of the report's shape.
///  2. the report files this spec under that release too. Without it, the 51 Rel-4
///     rows holding a 3.x.y document would be sent to Rel-99 — a section that does
///     not exist — and 3 181 clauses with no other home would simply stop being
///     acquired.
///  3. that release is at or above the floor. The floor is applied to the KEY's
///     release by `in_scope`; re-filing chooses a DIFFERENT one, and a target below
///     the floor is dropped by the fetch. The day a `deadRel-99` section appears in
///     the report, condition 2 stops protecting those same 51 rows and this is what
///     still does.
///
/// This is the invariant `scripts/corpus.sh`'s download fallback has enforced since
/// 812e7e1 — "NEVER a higher release's version that would then be mis-filed under
/// Rel-6 … the version-major IS the release ordinal" — held one step earlier, where
/// the release is actually chosen rather than where a failed download is patched.
fn filing_release(
    site: &BTreeMap<String, String>,
    spec: &str,
    rel: &str,
    ver: &str,
    floor_major: i64,
) -> Option<String> {
    let vmaj = major(ver);
    if vmaj < 3 || vmaj == major(rel) {
        return None;
    }
    let own = release_from_major(vmaj);
    if own == rel || major(&own) < floor_major {
        return None;
    }
    if !site.contains_key(&format!("{spec}|{own}")) {
        return None;
    }
    Some(own)
}

/// release_from_major maps a version major to the release that publishes it:
/// 3 → "Rel-99" (there is no Rel-98; the count jumps to Rel-4), else "Rel-<major>".
/// The twin of `major`, and of rust/parse's release_from_major.
pub fn release_from_major(vmaj: i64) -> String {
    if vmaj == 3 {
        "Rel-99".to_string()
    } else {
        format!("Rel-{vmaj}")
    }
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

/// changed_subject_series returns the sorted series owned by any subject whose
/// published footprint differs from the current code's (or is absent) — the delta
/// path's subject signal (== Go subjectmeta.ChangedSeries). An empty published map
/// returns every subject's series (once-only re-index after the index first ships).
/// Footprints come from the shared identity3gpp::SUBJECTS, byte-matched to Go.
pub fn changed_subject_series(published: &BTreeMap<String, String>) -> BTreeSet<String> {
    let mut out = BTreeSet::new();
    for (name, version, source_hash, series) in identity3gpp::SUBJECTS {
        let cur = identity3gpp::footprint(name, version, source_hash);
        if published.get(*name).map(String::as_str).unwrap_or("") != cur {
            for s in *series {
                out.insert(s.to_string());
            }
        }
    }
    out
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

    /// A CARRYING ROW THE CORPUS CAN NEVER MIRROR IS NOT A CHANGED SERIES. The
    /// report lists 26.510 at 18.4.0 under Rel-20; the file lands under Rel-18, so
    /// no `26.510|Rel-20` key is ever written and the key's own entry stays empty
    /// for ever. Asked of the key, that is a changed series on every build, and
    /// series 26 goes back into the ingest matrix each time for nothing.
    #[test]
    fn delta_is_measured_at_the_release_the_document_lands_in() {
        let site = idx(&[
            ("26.510|Rel-18", "18.5.0"),
            ("26.510|Rel-19", "19.2.0"),
            ("26.510|Rel-20", "18.4.0"),
        ]);
        let i = idx(&[("26.510|Rel-18", "18.5.0"), ("26.510|Rel-19", "19.2.0")]);
        assert!(
            delta_series(&site, &i, 4, false).is_empty(),
            "the corpus already holds 18.4.0's home release at a newer version"
        );

        // One version behind at the landing release: the document really is missing.
        let i2 = idx(&[("26.510|Rel-18", "18.3.0"), ("26.510|Rel-19", "19.2.0")]);
        let got: Vec<String> = delta_series(&site, &i2, 4, false).into_iter().collect();
        assert_eq!(got, vec!["26".to_string()]);
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
    fn changed_subject_series_cases() {
        // Empty published => every subject's series (21 glossary + 33 li).
        let all = changed_subject_series(&BTreeMap::new());
        assert!(all.contains("21") && all.contains("33"));
        // Published == current => nothing changed.
        let mut pubd = BTreeMap::new();
        for (n, v, sh, _) in identity3gpp::SUBJECTS {
            pubd.insert(n.to_string(), identity3gpp::footprint(n, v, sh));
        }
        assert!(changed_subject_series(&pubd).is_empty());
        // A stale li footprint => only li's series (33), not glossary's (21).
        pubd.insert("li".to_string(), "stale0000".to_string());
        let ch = changed_subject_series(&pubd);
        assert!(ch.contains("33") && !ch.contains("21"));
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

/// emit_repair_worklist is the primitive the operator should reach for instead of
/// crossing two lists by hand.
///
/// The fetch set is NOT "what upstream changed". It is:
///
///   repair = upstream_drift  ∪  corpus_holes
///
/// Those two terms answer different questions and neither implies the other.
/// `upstream_drift` (site version > anchor version) misses a spec the anchor
/// claims at the version the site still serves — 24 of the 56 known holes are
/// exactly that shape, and no amount of re-running discover will ever surface
/// them. `corpus_holes` comes from the DB, via `anchorcheck --emit-repair`, and is
/// passed in as `holes`.
///
/// Returns (lines, counts) where lines are "<release> <url> <name>", the same
/// format `--emit-worklist` produces, so scripts/corpus.sh consumes it unchanged.
pub fn emit_repair_worklist(
    site: &BTreeMap<String, String>,
    idx: &BTreeMap<String, String>,
    holes: &BTreeSet<String>,
    floor_major: i64,
    series_filter: &str,
) -> (String, RepairCounts) {
    let allow = series_set(series_filter);
    let mut counts = RepairCounts::default();
    let mut lines = String::new();
    let mut seen: BTreeSet<String> = BTreeSet::new();

    for (key, ver) in site {
        let (spec, rel) = split_key(key);
        let pfx = match in_scope(spec, rel, floor_major, &allow) {
            Some(p) => p,
            None => continue,
        };

        // THE DRIFT QUESTION IS "DO WE ALREADY HOLD THIS DOCUMENT?", AND RE-FILING
        // CHANGES WHERE IT WOULD LAND — so it has to be asked of the release the
        // line will actually carry. Asked of the key instead, a report row whose
        // key the corpus can never mirror (the file lands under Rel-18, so no
        // Rel-20 row is ever written) reads as drift on EVERY build: the work list
        // never empties and `fetch` re-downloads the same archive for ever, because
        // purgeConvertedZips deletes it after each conversion. The converted tree
        // would not grow, so `fetch` still declines and nothing downstream replays —
        // but it is a download per row per build, for nothing.
        //
        // `have` itself stays the KEY's version: it is what a hole was anchored at,
        // and that is a statement about the key, not about where the file belongs.
        let have = idx.get(key).map(String::as_str).unwrap_or("");
        let lands_in = filing_release(site, spec, rel, ver, floor_major);
        let have_there = match &lands_in {
            Some(own) => idx
                .get(&format!("{spec}|{own}"))
                .map(String::as_str)
                .unwrap_or(""),
            None => have,
        };
        let drifted = cmp_ver(ver, have_there) == Ordering::Greater;
        let holed = holes.contains(key);
        if !drifted && !holed {
            continue;
        }
        // Count each population in FULL, plus the overlap, so the identity
        //     emitted = (missing + stale) + holes - overlap - deduped
        // holds and is checkable by eye. Reporting only the disjoint parts would
        // hide the term that matters: an overlap collapsing towards zero means the
        // hole detector and the drift computation have stopped agreeing about what
        // the corpus contains, and that is a defect, not an improvement.
        //
        // `deduped` joined the identity with re-filing: two keys of one spec can now
        // resolve to the same release at the same version and render the SAME line
        // (33.816 is listed at 10.0.0 under both Rel-10 and Rel-11), and corpus.sh
        // fetches the manifest in PARALLEL — two workers on one `$zip.part` is a
        // download race, not merely a wasted request.
        if drifted {
            if have_there.is_empty() {
                counts.upstream_missing += 1;
            } else {
                counts.upstream_stale += 1;
            }
        }
        if holed {
            counts.corpus_holes += 1;
        }
        if drifted && holed {
            counts.overlap += 1;
        }

        // A hole must be re-acquired at the version the ANCHOR claims when the site
        // has nothing newer: fetching the site version would re-download something
        // the corpus already believes it has and leave the hole open.
        let want = if drifted { ver.as_str() } else { have };
        let mut file_under = rel;
        let owned;
        if let Some(own) = filing_release(site, spec, rel, want, floor_major) {
            owned = own;
            file_under = &owned;
            counts.refiled += 1;
        }
        match archive_line(spec, file_under, &pfx, want) {
            Some(line) => {
                if seen.insert(line.clone()) {
                    lines.push_str(&line);
                    counts.emitted += 1;
                } else {
                    counts.deduped += 1;
                }
            }
            None => counts.unencodable += 1,
        }
    }

    // A HOLE THE STATUS REPORT DOES NOT LIST IS STILL FETCHABLE.
    //
    // The report carries ONE row per spec — its current version — and the release is
    // read off that version's major. So a hole at an OLDER version has no row to
    // match: 29.558 reports 19.7.0, giving the key `29.558|Rel-19`, while the hole is
    // `29.558|Rel-20` anchored at 19.5.0. The loop above can never see it.
    //
    // Counting it and moving on was the bug — the comment here used to say silence is
    // how the 56 became invisible, then dropped the key anyway. The anchor already
    // names the exact version and the archive keeps every version it ever published,
    // so the URL needs no report: 29.558|Rel-20 -> 29.558/29558-j50.zip, which serves.
    // All twelve such holes on the local corpus were verified to resolve to a real
    // file, and re-acquiring the anchored version is precisely what flips a key from
    // missing_content to non_content in cmd/anchorcheck.
    //
    // The count stays: it is the population the report cannot describe, and watching
    // it grow says something the emitted total does not.
    for key in holes {
        if site.contains_key(key) {
            continue;
        }
        counts.holes_not_in_report += 1;
        let (spec, rel) = split_key(key);
        let pfx = match in_scope(spec, rel, floor_major, &allow) {
            Some(p) => p,
            None => continue,
        };
        // With no anchor version there is nothing to ask for: the report has no row
        // and the corpus makes no claim, so the key names no document at all.
        let want = idx.get(key).map(String::as_str).unwrap_or("");
        if want.is_empty() {
            counts.unencodable += 1;
            continue;
        }
        // THIS IS THE LOOP THAT WROTE THE MIS-FILINGS. The paragraph above reads the
        // corpus's own key as proof that the document belongs to that release — "the
        // hole is 29.558|Rel-20 anchored at 19.5.0" — but the key is the corpus
        // quoting itself, and 19.5.0 is a Rel-19 document. Acquiring it under Rel-20
        // gave Rel-20 the ONLY copy of 1 097 clauses of Rel-19 text; twelve holes
        // were closed that way and all twelve are still in the published corpus.
        //
        // The hole is real and must still be closed — the fix is WHERE the file
        // lands, not whether it is fetched. Filed under Rel-19, the same download
        // makes 29.558@19.5.0 indexed, and anchorcheck reclassifies 29.558|Rel-20
        // from MissingContent to NonContent: the bookkeeping row it always was.
        let mut file_under = rel;
        let owned;
        if let Some(own) = filing_release(site, spec, rel, want, floor_major) {
            owned = own;
            file_under = &owned;
            counts.refiled += 1;
        }
        counts.corpus_holes += 1;
        match archive_line(spec, file_under, &pfx, want) {
            Some(line) => {
                if seen.insert(line.clone()) {
                    lines.push_str(&line);
                    counts.emitted += 1;
                } else {
                    counts.deduped += 1;
                }
            }
            None => counts.unencodable += 1,
        }
    }
    (lines, counts)
}

/// in_scope resolves the series prefix for `spec`, or None when the key falls below
/// the release floor or outside the series filter. Shared so the two passes of
/// emit_repair_worklist cannot drift apart on what they consider in range.
fn in_scope(
    spec: &str,
    rel: &str,
    floor_major: i64,
    allow: &Option<BTreeSet<String>>,
) -> Option<String> {
    if rel.is_empty() || spec.len() < 2 || major(rel) < floor_major {
        return None;
    }
    let pfx = series_prefix(spec)?;
    if let Some(set) = allow {
        if !set.contains(&pfx) {
            return None;
        }
    }
    Some(pfx)
}

/// archive_line renders one "<release> <url> <name>" entry, or None when the version
/// cannot be encoded into an archive file name.
fn archive_line(spec: &str, rel: &str, pfx: &str, version: &str) -> Option<String> {
    let code = encode_ver_code(version)?;
    let num = spec.replacen('.', "", 1);
    let name = format!("{num}-{code}.zip");
    Some(format!(
        "{rel} {STATUS_BASE}/{pfx}_series/{spec}/{name} {name}\n"
    ))
}

/// RepairCounts breaks the repair set into its populations so the operator can see
/// where the work came from, and so a sudden collapse in one term is visible
/// instead of looking like progress.
#[derive(Debug, Default, Clone, PartialEq, Eq)]
pub struct RepairCounts {
    pub upstream_missing: usize,
    pub upstream_stale: usize,
    pub corpus_holes: usize,
    pub overlap: usize,
    pub emitted: usize,
    pub unencodable: usize,
    pub holes_not_in_report: usize,
    /// Lines emitted under the release the version's own major names instead of the
    /// key's release — see filing_release.
    pub refiled: usize,
    /// Lines a re-filing made identical to one already emitted, and so dropped. Part
    /// of the union identity; see the comment where the populations are counted.
    pub deduped: usize,
}

/// load_holes reads `anchorcheck --emit-repair` output: one "spec|Rel" per line.
/// A missing file is an EMPTY set, never an error — but the caller must say so
/// out loud, because "no holes" and "never looked" produce the same repair set
/// and only one of them is good news.
pub fn load_holes(path: &str) -> BTreeSet<String> {
    let mut out = BTreeSet::new();
    if path.is_empty() {
        return out;
    }
    if let Ok(text) = std::fs::read_to_string(path) {
        for line in text.lines() {
            let l = line.trim();
            if !l.is_empty() && !l.starts_with('#') {
                out.insert(l.to_string());
            }
        }
    }
    out
}

#[cfg(test)]
mod repair_tests {
    use super::*;

    fn m(pairs: &[(&str, &str)]) -> BTreeMap<String, String> {
        pairs
            .iter()
            .map(|(k, v)| (k.to_string(), v.to_string()))
            .collect()
    }

    /// The repair set is a UNION, and the identity
    ///     emitted = (missing + stale) + holes - overlap
    /// is what makes it auditable. This pins all four terms at once against a
    /// fixture holding one of each population.
    #[test]
    fn repair_set_is_the_union_and_the_counts_reconcile() {
        let site = m(&[
            ("23.501|Rel-19", "19.6.0"), // stale: index behind
            ("23.502|Rel-19", "19.1.0"), // missing: not in index at all
            ("29.502|Rel-20", "19.5.0"), // hole only: index == site
            ("24.501|Rel-19", "19.3.0"), // both: index behind AND no clause text
            ("38.331|Rel-19", "19.2.0"), // clean: nothing to do
        ]);
        let idx = m(&[
            ("23.501|Rel-19", "19.5.0"),
            ("29.502|Rel-20", "19.5.0"),
            ("24.501|Rel-19", "19.1.0"),
            ("38.331|Rel-19", "19.2.0"),
        ]);
        let holes: BTreeSet<String> = ["29.502|Rel-20", "24.501|Rel-19"]
            .iter()
            .map(|s| s.to_string())
            .collect();

        let (lines, c) = emit_repair_worklist(&site, &idx, &holes, 0, "");

        assert_eq!(c.upstream_stale, 2, "23.501 and 24.501 are stale");
        assert_eq!(c.upstream_missing, 1, "23.502 is absent from the index");
        assert_eq!(c.corpus_holes, 2, "29.502 and 24.501 have no text");
        assert_eq!(c.overlap, 1, "24.501 is both stale and a hole");
        assert_eq!(
            c.emitted,
            c.upstream_missing + c.upstream_stale + c.corpus_holes - c.overlap - c.deduped,
            "the union identity must hold, or the plan is silently over- or under-counting"
        );
        assert_eq!(c.deduped, 0, "nothing converges in this fixture");
        assert_eq!(c.emitted, 4);
        assert!(
            !lines.contains("38331"),
            "a clean spec must not be re-fetched"
        );
        assert!(lines.contains("29502"), "a hole-only spec must be fetched");
    }

    /// A hole whose index version the site still serves must be fetched at the
    /// ANCHOR's version. Fetching the site version would re-download what the
    /// corpus already believes it holds and leave the hole open — which is exactly
    /// how 24 of the 56 stayed invisible.
    #[test]
    fn a_hole_with_no_drift_is_fetched_at_the_anchored_version() {
        let site = m(&[("29.520|Rel-20", "19.6.0")]);
        let idx = m(&[("29.520|Rel-20", "19.6.0")]);
        let holes: BTreeSet<String> = ["29.520|Rel-20".to_string()].into_iter().collect();

        let (lines, c) = emit_repair_worklist(&site, &idx, &holes, 0, "");
        assert_eq!(c.corpus_holes, 1);
        assert_eq!(c.upstream_stale + c.upstream_missing, 0, "nothing drifted");
        assert_eq!(c.emitted, 1, "the hole must still be in the plan");
        // 19.6.0 -> base36 per component: j=19, 6, 0
        assert!(
            lines.contains("29520-j60.zip"),
            "expected the anchored version code, got: {lines}"
        );
    }

    /// Without a holes file the plan degrades to drift-only. That is a legitimate
    /// mode, but it must not silently look like a full repair — the caller is
    /// warned, and this test pins that the two are genuinely different sets.
    #[test]
    fn drift_only_plan_omits_the_holes() {
        let site = m(&[("29.502|Rel-20", "19.5.0")]);
        let idx = m(&[("29.502|Rel-20", "19.5.0")]);
        let (_, c) = emit_repair_worklist(&site, &idx, &BTreeSet::new(), 0, "");
        assert_eq!(c.emitted, 0, "drift-only sees nothing here");

        let holes: BTreeSet<String> = ["29.502|Rel-20".to_string()].into_iter().collect();
        let (_, c2) = emit_repair_worklist(&site, &idx, &holes, 0, "");
        assert_eq!(c2.emitted, 1, "with holes it is one spec");
    }

    /// A hole the status report does not list is STILL fetchable, because the anchor
    /// names the version and the archive keeps every version it published.
    ///
    /// This is the real shape, taken from the corpus: the report carries 29.558 at
    /// 19.7.0 under Rel-19, while the hole is 29.558|Rel-20 anchored at 19.5.0. No
    /// report row can ever match that key, and counting it and moving on left the
    /// corpus permanently short.
    ///
    /// It is fetched — AND FILED UNDER Rel-19, because 19.5.0 is a Rel-19 document
    /// and the report files 29.558 under Rel-19. Filing it under Rel-20 is what put
    /// 1 097 clauses of Rel-19 text into the published corpus as Rel-20, the only
    /// copy of that version anywhere in it (measured 2026-09-12). Once 29.558@19.5.0
    /// is indexed under Rel-19, anchorcheck reclassifies 29.558|Rel-20 as NonContent
    /// — "3GPP routinely lists a spec's Rel-N entry at the Rel-(N-1) version, so this
    /// is bookkeeping, not a gap" — so the hole closes either way.
    #[test]
    fn a_hole_absent_from_the_report_is_fetched_under_the_release_its_version_names() {
        let site = m(&[("29.558|Rel-19", "19.7.0")]);
        let idx = m(&[("29.558|Rel-19", "19.7.0"), ("29.558|Rel-20", "19.5.0")]);
        let holes: BTreeSet<String> = ["29.558|Rel-20".to_string()].into_iter().collect();

        let (lines, c) = emit_repair_worklist(&site, &idx, &holes, 0, "");
        assert_eq!(c.holes_not_in_report, 1, "the population must stay visible");
        assert_eq!(c.emitted, 1, "and it must actually be fetched");
        assert_eq!(c.refiled, 1, "and the re-filing must be visible too");
        assert!(
            lines.contains("29558-j50.zip"),
            "expected the ANCHOR's version (19.5.0 -> j50), not the report's 19.7.0; got: {lines}"
        );
        assert!(
            lines.starts_with("Rel-19 "),
            "19.5.0 is a Rel-19 document; got: {lines}"
        );
    }

    /// The hole's own release is kept when the report does NOT file that spec under
    /// the release its version names. The corpus holds no Rel-99 section at all
    /// (floor Rel-4), so its 51 Rel-4 rows at a 3.x.y version — 21.810 3.0.0, 29.198
    /// 3.4.0, 32.005 3.7.0 … — are the ONLY copy of those documents, and today's
    /// report still files them exactly there. Re-filing them would send 2 500+
    /// clauses to a release below the floor, i.e. delete them.
    #[test]
    fn a_carried_forward_version_with_no_home_release_stays_where_it_is() {
        let site = m(&[("21.810|Rel-4", "3.0.0")]);
        let idx = m(&[("21.810|Rel-4", "")]);
        let holes: BTreeSet<String> = ["21.810|Rel-4".to_string()].into_iter().collect();

        let (lines, c) = emit_repair_worklist(&site, &idx, &holes, 0, "");
        assert_eq!(
            c.refiled, 0,
            "Rel-99 is not in the report: nothing to re-file"
        );
        assert!(
            lines.starts_with("Rel-4 "),
            "the only home this document has is Rel-4; got: {lines}"
        );
    }

    /// 26.510, the case that named this defect. The report files it under Rel-18 at
    /// 18.5.0 and under Rel-20 at 18.4.0 (the shape the published corpus recorded on
    /// 2026-08-25; today's report has withdrawn the Rel-20 row). The Rel-20 request
    /// must go to the archive AS Rel-18: v18.4.0's cover says Release 18, and 3GPP's
    /// archive URL carries no release to contradict it.
    #[test]
    fn the_wholesale_worklist_files_a_version_under_the_release_it_names() {
        let site = m(&[
            ("26.510|Rel-18", "18.5.0"),
            ("26.510|Rel-19", "19.2.0"),
            ("26.510|Rel-20", "18.4.0"),
        ]);
        let (lines, c) = emit_worklist(&site, 4, "");
        assert_eq!(
            (c.emitted, c.unencodable, c.refiled, c.deduped),
            (3, 0, 1, 0),
            "lines: {lines}"
        );
        assert!(
            lines.contains("Rel-18 https://www.3gpp.org/ftp/Specs/archive/26_series/26.510/26510-i40.zip 26510-i40.zip\n"),
            "18.4.0 must be requested as Rel-18; got: {lines}"
        );
        assert!(
            !lines.contains("Rel-20 "),
            "nothing here is a Rel-20 document; got: {lines}"
        );
    }

    /// A DRAFT is legitimately older than the release it is drafted for: 36.833-1 is
    /// listed at 0.4.0 under Rel-13, and "Rel-0" is not a release. Today's live report
    /// carries three rows in that shape and the guard must move none of them.
    ///
    /// The fixture puts a "Rel-2" section in the report and drops the floor to 0, so
    /// that the draft check is the ONLY thing refusing the move. The realistic
    /// fixture (no such section, floor Rel-4) passes whether the draft check exists
    /// or not — it is refused by the other two conditions — and would therefore have
    /// proved nothing about the line it claims to cover.
    #[test]
    fn a_draft_keeps_the_release_it_is_drafted_for() {
        let site = m(&[("36.833-1|Rel-2", "2.9.0"), ("36.833-1|Rel-13", "2.0.0")]);
        let (lines, c) = emit_worklist(&site, 0, "");
        assert_eq!(c.refiled, 0, "a draft is not mis-filed; got: {lines}");
        assert!(
            lines.contains(
                "Rel-13 https://www.3gpp.org/ftp/Specs/archive/36_series/36.833-1/36833-1-200.zip"
            ),
            "the draft keeps the release it is drafted for; got: {lines}"
        );
    }

    /// RE-FILING MUST NEVER MOVE A DOCUMENT OUT OF THE CORPUS. The floor is applied
    /// to the KEY's release; re-filing picks a different one. If a `deadRel-99`
    /// section ever appears in the report, the 51 Rel-4 rows holding a 3.x.y document
    /// — 3 181 clauses with no other copy — would all resolve to Rel-99, below the
    /// Rel-4 floor, and `in_scope` would drop every one of them from the fetch.
    #[test]
    fn refiling_never_sends_a_document_below_the_floor() {
        let site = m(&[("21.810|Rel-99", "3.0.0"), ("21.810|Rel-4", "3.0.0")]);
        let (lines, c) = emit_worklist(&site, major("Rel-4"), "");
        assert_eq!(c.refiled, 0, "Rel-99 is below the floor; got: {lines}");
        assert_eq!(c.emitted, 1, "only the Rel-4 key is in scope; got: {lines}");
        assert!(
            lines.starts_with("Rel-4 "),
            "the document must still be fetched, as Rel-4; got: {lines}"
        );
    }

    /// A REPORT ROW THE CORPUS CAN NEVER MIRROR MUST NOT READ AS DRIFT FOR EVER.
    /// 26.510 is listed at 18.4.0 under Rel-20; the file lands under Rel-18, so no
    /// `26.510|Rel-20` row is ever written and the key's own entry stays empty. Asked
    /// of the key, that is drift on every build — and `purgeConvertedZips` deletes
    /// the archive after each conversion, so it is a fresh download every time. Asked
    /// of Rel-18, where the corpus already holds 18.5.0, there is nothing to fetch.
    #[test]
    fn drift_is_measured_at_the_release_the_line_lands_in() {
        let site = m(&[
            ("26.510|Rel-18", "18.5.0"),
            ("26.510|Rel-19", "19.2.0"),
            ("26.510|Rel-20", "18.4.0"),
        ]);
        let idx = m(&[("26.510|Rel-18", "18.5.0"), ("26.510|Rel-19", "19.2.0")]);
        let (lines, c) = emit_repair_worklist(&site, &idx, &BTreeSet::new(), 4, "");
        assert_eq!(c.emitted, 0, "the corpus already holds it; got: {lines}");
        assert!(lines.is_empty(), "got: {lines}");
    }

    /// The same shape, one version behind: the corpus's Rel-19 filing is older than
    /// what the Rel-20 row asks for, so the document IS missing and must be fetched —
    /// under Rel-19, and counted as drift there.
    #[test]
    fn a_refiled_row_the_corpus_lacks_is_still_fetched() {
        let site = m(&[("29.558|Rel-19", "19.4.0"), ("29.558|Rel-20", "19.5.0")]);
        let idx = m(&[("29.558|Rel-19", "19.4.0")]);
        let (lines, c) = emit_repair_worklist(&site, &idx, &BTreeSet::new(), 4, "");
        assert_eq!(c.refiled, 1, "got: {lines}");
        assert!(
            lines.contains(
                "Rel-19 https://www.3gpp.org/ftp/Specs/archive/29_series/29.558/29558-j50.zip"
            ),
            "19.5.0 must be fetched as Rel-19; got: {lines}"
        );
        assert!(!lines.contains("Rel-20 "), "got: {lines}");
    }

    /// The repair plan can converge two keys onto one line too, and corpus.sh fetches
    /// the manifest in PARALLEL: two workers on the same `$zip.part` is a download
    /// race. The union identity carries the dropped line as its own term rather than
    /// quietly not adding up.
    #[test]
    fn the_repair_plan_does_not_emit_a_line_twice() {
        let site = m(&[("33.816|Rel-10", "10.0.0"), ("33.816|Rel-11", "10.0.0")]);
        let idx = m(&[]);
        let (lines, c) = emit_repair_worklist(&site, &idx, &BTreeSet::new(), 4, "");
        assert_eq!(c.deduped, 1, "lines: {lines}");
        assert_eq!(c.emitted, 1, "lines: {lines}");
        assert_eq!(
            c.emitted,
            c.upstream_missing + c.upstream_stale + c.corpus_holes - c.overlap - c.deduped,
            "the identity must still hold once a line converges"
        );
        assert_eq!(
            lines,
            "Rel-10 https://www.3gpp.org/ftp/Specs/archive/33_series/33.816/33816-a00.zip 33816-a00.zip
"
        );
    }

    /// TWO `filing_release` CALLS, BECAUSE TWO DIFFERENT VERSIONS ARE AT STAKE.
    /// `drifted` asks about the version the REPORT carries; `want` may instead be
    /// the version the ANCHOR carries, when the key is a hole the report has not
    /// moved past. Each is filed under the release ITS OWN major names, and the two
    /// can legitimately differ.
    ///
    /// The mixed-major fixture: the report lists 26.510 at 18.4.0 under Rel-20, the
    /// corpus holds 18.5.0 under Rel-18 and claims 17.2.0 under Rel-20 with no text.
    /// 18.4.0 is not drift — Rel-18 already holds something newer — so the only work
    /// is the hole, and the hole is 17.2.0. Fetching 17.2.0 as Rel-17 is what closes
    /// it: once 26.510@17.2.0 is indexed anywhere, anchorcheck reclassifies
    /// 26.510|Rel-20 from MissingContent to NonContent. Fetching it as Rel-20 would
    /// close the same hole by putting a Release-17 document under Rel-20, which is
    /// the defect this whole change exists to stop.
    #[test]
    fn a_hole_is_filed_by_the_version_the_anchor_names_not_the_report() {
        let site = m(&[
            ("26.510|Rel-17", "17.4.0"),
            ("26.510|Rel-18", "18.5.0"),
            ("26.510|Rel-20", "18.4.0"),
        ]);
        let idx = m(&[
            ("26.510|Rel-17", "17.4.0"),
            ("26.510|Rel-18", "18.5.0"),
            ("26.510|Rel-20", "17.2.0"),
        ]);
        let holes: BTreeSet<String> = ["26.510|Rel-20".to_string()].into_iter().collect();

        let (lines, c) = emit_repair_worklist(&site, &idx, &holes, 4, "");
        assert_eq!(
            c.upstream_stale + c.upstream_missing,
            0,
            "18.4.0 is not drift: Rel-18 holds 18.5.0; {lines}"
        );
        assert_eq!(c.corpus_holes, 1, "{lines}");
        assert_eq!(c.refiled, 1, "{lines}");
        assert_eq!(
            lines,
            "Rel-17 https://www.3gpp.org/ftp/Specs/archive/26_series/26.510/26510-h20.zip 26510-h20.zip
",
            "the ANCHOR's 17.2.0, filed under the release 17.x names"
        );
    }

    /// AND THE COLLISION CROSSES THE TWO LOOPS. The second loop only sees keys the
    /// report does not carry, so no KEY reaches both — but a hole key and a report
    /// key of the same spec can still render the same LINE once re-filing sends them
    /// to the same release. `seen` is therefore shared by both loops, not per-loop.
    #[test]
    fn the_two_repair_loops_share_one_seen_set() {
        let site = m(&[("33.816|Rel-10", "10.0.0")]);
        let idx = m(&[("33.816|Rel-11", "10.0.0")]);
        let holes: BTreeSet<String> = ["33.816|Rel-11".to_string()].into_iter().collect();
        let (lines, c) = emit_repair_worklist(&site, &idx, &holes, 4, "");
        assert_eq!(
            c.holes_not_in_report, 1,
            "the hole key is not in the report"
        );
        assert_eq!(
            c.deduped, 1,
            "the two loops rendered the same line; {lines}"
        );
        assert_eq!(
            lines,
            "Rel-10 https://www.3gpp.org/ftp/Specs/archive/33_series/33.816/33816-a00.zip 33816-a00.zip
"
        );
    }

    /// Two keys of one spec that re-file onto the same release at the same version
    /// render the identical line — 33.816 is listed at 10.0.0 under both Rel-10 and
    /// Rel-11 in today's report. corpus.sh would fetch it twice.
    #[test]
    fn refiling_does_not_duplicate_a_line() {
        let site = m(&[("33.816|Rel-10", "10.0.0"), ("33.816|Rel-11", "10.0.0")]);
        let (lines, c) = emit_worklist(&site, 4, "");
        assert_eq!(
            (c.emitted, c.refiled, c.deduped),
            (1, 0, 1),
            "the line that survives is the Rel-10 key's own, not the re-filing; {lines}"
        );
        assert_eq!(
            lines,
            "Rel-10 https://www.3gpp.org/ftp/Specs/archive/33_series/33.816/33816-a00.zip 33816-a00.zip\n"
        );
    }

    /// A hole in neither the report nor the anchor names no document at all: there is
    /// no version to ask the archive for. Count it, do not invent a URL.
    #[test]
    fn a_hole_with_no_anchor_names_no_document() {
        let site = m(&[("23.501|Rel-19", "19.5.0")]);
        let idx = m(&[("23.501|Rel-19", "19.5.0")]);
        let holes: BTreeSet<String> = ["99.999|Rel-20".to_string()].into_iter().collect();
        let (lines, c) = emit_repair_worklist(&site, &idx, &holes, 0, "");
        assert_eq!(c.holes_not_in_report, 1);
        assert_eq!(c.emitted, 0, "nothing to fetch without a version");
        assert_eq!(
            c.unencodable, 1,
            "and it must be reported, not silently gone"
        );
        assert!(lines.is_empty());
    }

    /// The series filter and the release floor apply to anchor-recovered holes too.
    /// The second pass must not become a hole in the scoping the first pass enforces.
    #[test]
    fn an_anchor_recovered_hole_still_obeys_scope() {
        // 23.501 is anchored at what the site serves, so it neither drifts nor holes
        // and contributes nothing — leaving 29.558 as the only candidate.
        let site = m(&[("23.501|Rel-19", "19.5.0")]);
        let idx = m(&[("23.501|Rel-19", "19.5.0"), ("29.558|Rel-20", "19.5.0")]);
        let holes: BTreeSet<String> = ["29.558|Rel-20".to_string()].into_iter().collect();

        let (lines, c) = emit_repair_worklist(&site, &idx, &holes, 0, "23");
        assert!(
            !lines.contains("29558"),
            "series 29 is outside the '23' filter, got: {lines}"
        );
        assert_eq!(c.emitted, 0);

        let (lines, c) = emit_repair_worklist(&site, &idx, &holes, 99, "");
        assert!(
            !lines.contains("29558"),
            "Rel-20 is below a Rel-99-major floor, got: {lines}"
        );
        assert_eq!(c.emitted, 0);

        // And with neither restriction it IS emitted — otherwise the two assertions
        // above would pass on a function that never emits anything.
        let (lines, c) = emit_repair_worklist(&site, &idx, &holes, 0, "");
        assert_eq!(c.emitted, 1);
        assert!(lines.contains("29558-j50.zip"), "got: {lines}");
    }
}
