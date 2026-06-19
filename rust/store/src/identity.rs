//! identity — Rust port of the build-identity + subject-footprint subsystem shared by the
//! Go merge and cmd/discover (internal/model/pipeline.go, internal/subjectmeta,
//! internal/enrichmeta, internal/evolseed). These 12-char sha256 digests gate the
//! incremental build: a SpecIngestIdentity drift forces re-ingest, GlobalEnrichmentIdentity
//! forces re-enrich, EmbedIdentity forces re-embed. They MUST stay byte-identical to the Go
//! side (discover still reads them) — every value here is golden-tested against the Go output.

use sha2::{Digest, Sha256};
use std::collections::BTreeSet;

/// digest12 = first 12 hex chars of sha256(parts joined by "|") (== Go digest12).
pub fn digest12(parts: &[&str]) -> String {
    let mut h = Sha256::new();
    h.update(parts.join("|").as_bytes());
    hex::encode(h.finalize())[..12].to_string()
}

// Canonical constants (== internal/model + subjectmeta + enrichmeta).
const PARSER_VERSION: &str = "html-v2";
const CHUNKING_VERSION: &str = "clause-leaf-v1";
const SCHEMA_VERSION: &str = "1";
const ASN1_SCANNER_VERSION: &str = "asn1-v1";
const CATALOG_VERSION: &str = "catalog-v1";
const OPENAPI_INGEST_VERSION: &str = "openapi-v1";

/// Subject metadata (== subjectmeta.All): (name, version, source_hash, series).
pub const SUBJECTS: &[(&str, &str, &str, &[&str])] = &[
    ("li", "li-v1", ASN1_SCANNER_VERSION, &["33"]),
    ("glossary", "glossary-v1", "", &["21"]),
];

/// footprint = sha256(name|version|sourceHash)[:12] (== subjectmeta.Footprint).
pub fn footprint(name: &str, version: &str, source_hash: &str) -> String {
    digest12(&[name, version, source_hash])
}

// Evolutions seed edges (== internal/evolseed.Seed): (from, to, type, clause, conf).
// JustificationSpec is the constant "23.501".
const SEED_ARCH: &str = "23.501";
const SEED_EDGES: &[(&str, &str, &str, &str, f64)] = &[
    ("MME", "AMF", "SPLIT", "4.2.2", 0.90),
    ("MME", "SMF", "SPLIT", "4.2.2", 0.75),
    ("SGW", "UPF", "REPLACED_BY", "4.2.3", 0.80),
    ("PGW", "UPF", "SPLIT", "4.2.3", 0.80),
    ("PGW", "SMF", "SPLIT", "4.2.3", 0.80),
    ("PGW-C", "SMF", "RENAME", "4.2.3", 0.85),
    ("PGW-U", "UPF", "RENAME", "4.2.3", 0.85),
    ("HSS", "UDM", "REPLACED_BY", "4.2.4", 0.80),
    ("HSS", "UDR", "SPLIT", "4.2.4", 0.65),
    ("HSS", "AUSF", "SPLIT", "4.2.4", 0.65),
    ("PCRF", "PCF", "RENAME", "4.2.5", 0.90),
    ("eNB", "gNB", "RENAME", "4.2.6", 0.85),
    ("ePDG", "N3IWF", "REPLACED_BY", "4.2.8", 0.80),
    ("SGSN", "AMF", "REPLACED_BY", "4.2.2", 0.55),
    ("", "NRF", "EXTENDED_BY", "6.2.6", 0.70),
    ("", "NSSF", "EXTENDED_BY", "6.2.14", 0.70),
];

/// seed_hash = sha256(sorted "from|to|type|spec|clause|%.4f"-tuples joined by \n)[:12]
/// (== evolseed.SeedHash; order-independent set of edges).
pub fn seed_hash() -> String {
    let mut tuples: Vec<String> = SEED_EDGES
        .iter()
        .map(|(f, t, ty, cl, c)| format!("{f}|{t}|{ty}|{SEED_ARCH}|{cl}|{c:.4}"))
        .collect();
    tuples.sort();
    let mut h = Sha256::new();
    h.update(tuples.join("\n").as_bytes());
    hex::encode(h.finalize())[..12].to_string()
}

/// spec_ingest_identity (== SpecIngestParts.Identity). subj_footprints are sorted + joined
/// with "," ; subject source-hashes are unused here (empty) as the Go merge path does.
pub fn spec_ingest_identity(subject_footprints: &[String]) -> String {
    let mut subj: Vec<&str> = subject_footprints.iter().map(String::as_str).collect();
    subj.sort_unstable();
    let joined = subj.join(",");
    digest12(&[
        "spec-ingest-v1",
        PARSER_VERSION,
        "",
        CHUNKING_VERSION,
        SCHEMA_VERSION,
        ASN1_SCANNER_VERSION,
        &joined,
        "",
    ])
}

/// global_enrichment_identity (== GlobalEnrichmentParts.Identity for enrichmeta.Current()).
pub fn global_enrichment_identity() -> String {
    digest12(&[
        "global-enrich-v1",
        CATALOG_VERSION,
        "",
        OPENAPI_INGEST_VERSION,
        "",
        &seed_hash(),
    ])
}

/// embed_identity (== EmbedParts{ModelID}.Identity); all other EmbedParts fields empty,
/// matching how the merge composes the build index.
pub fn embed_identity(model_id: &str) -> String {
    digest12(&["embed-v1", model_id, "", "", "", "", "", "", ""])
}

/// effective_subject_footprints (== merge.effectiveSubjectFootprints): a subject ADVANCES to
/// its current footprint only if every one of its series was rebuilt (present in `rebuilt`);
/// otherwise it carries the base value (`base_fps`, "" when absent). Returned sorted by name.
pub fn effective_subject_footprints(
    rebuilt: &BTreeSet<String>,
    base_fps: &std::collections::HashMap<String, String>,
) -> Vec<(String, String)> {
    let mut out = Vec::new();
    for (name, version, source_hash, series) in SUBJECTS {
        let advance = series.iter().all(|s| rebuilt.contains(*s));
        let fp = if advance {
            footprint(name, version, source_hash)
        } else {
            base_fps.get(*name).cloned().unwrap_or_default()
        };
        out.push((name.to_string(), fp));
    }
    out.sort();
    out
}

/// cmp_ver compares two "a.b.c" version triples numerically (== merge.cmpVer). Missing/garbage
/// components sort low. Returns Ordering.
pub fn cmp_ver(a: &str, b: &str) -> std::cmp::Ordering {
    let triple = |v: &str| -> [i64; 3] {
        let mut t = [0i64; 3];
        for (i, p) in v.split('.').take(3).enumerate() {
            t[i] = p.parse().unwrap_or(0);
        }
        t
    };
    triple(a).cmp(&triple(b))
}

#[cfg(test)]
mod tests {
    use super::*;

    // Golden values captured from the Go cmd/merge build-index on a lexical shard.
    #[test]
    fn digests_match_go_golden() {
        // spec-ingest with subject footprints ["",""] (mono-series shard → both carried "").
        assert_eq!(
            spec_ingest_identity(&["".to_string(), "".to_string()]),
            "8bcf012e3f10"
        );
        assert_eq!(embed_identity(""), "2d36b4425d54");
        assert_eq!(seed_hash(), "0e6460e22755");
        assert_eq!(global_enrichment_identity(), "5fb3a6c87488");
    }

    #[test]
    fn footprints_advance_only_when_all_series_rebuilt() {
        // mono-series 23 shard: neither li(33) nor glossary(21) advances → "".
        let mut r = BTreeSet::new();
        r.insert("23".to_string());
        let fps = effective_subject_footprints(&r, &std::collections::HashMap::new());
        assert_eq!(
            fps,
            vec![
                ("glossary".to_string(), "".to_string()),
                ("li".to_string(), "".to_string())
            ]
        );
        // full corpus (21+33 present): both advance to their footprints.
        let mut full = BTreeSet::new();
        full.insert("21".to_string());
        full.insert("33".to_string());
        let fps2 = effective_subject_footprints(&full, &std::collections::HashMap::new());
        assert_eq!(fps2[1].1, footprint("li", "li-v1", "asn1-v1"));
        assert!(!fps2[0].1.is_empty());
    }

    #[test]
    fn cmp_ver_orders_numerically() {
        assert_eq!(cmp_ver("19.5.0", "19.0.0"), std::cmp::Ordering::Greater);
        assert_eq!(cmp_ver("19.0.0", ""), std::cmp::Ordering::Greater);
    }
}
