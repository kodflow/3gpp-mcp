//! ingest-li (Rust) — Phase 8b. The Lawful-Interception subject: parse TS 33.128's ASN.1
//! module (TS33128Payloads.asn) with parse3gpp::asn1 and write the authoritative LI event
//! registry (li_events / li_event_fields / li_nf_clauses) + the full asn1 type catalogue
//! (asn1_types) for one release via store-rs. Additive + idempotent (clears the (spec,
//! release) rows first). spec_id is always 33.128.
//!
//! Usage: ingest-li --asn <TS33128Payloads.asn> --db <db> [--release Rel-NN]
use anyhow::{Context, Result};
use clap::Parser;
use parse3gpp::asn1::{members_json, parse_module};
use store_rs::{Asn1TypeIn, LiEventIn, LiFieldIn, LiNfClauseIn, Store};

const LI_SPEC_ID: &str = "33.128";

/// LI_CONTENT_KEY_META prefixes the meta key recording which registry the li_* and
/// asn1_types tables were built from. The RELEASE is appended, because clear_li works per
/// (spec, release) and the corpus can therefore hold several at once: one global key would
/// make two releases invalidate each other on every run and neither would ever skip.
const LI_CONTENT_KEY_META: &str = "li_content_key";

#[derive(Parser)]
#[command(
    name = "ingest-li",
    about = "Parse TS 33.128 ASN.1 into the LI registry (parse3gpp::asn1 + store-rs)"
)]
struct Args {
    #[arg(long)]
    db: String,
    /// TS33128Payloads.asn text.
    #[arg(long)]
    asn: String,
    /// Override the release (default: the r<NN> from the module OID).
    #[arg(long)]
    release: Option<String>,
}

/// FNV-1a 64, written out rather than pulled in.
///
/// `DefaultHasher` would be one line, and it is the wrong tool: its output is explicitly
/// not stable across Rust releases, so a toolchain upgrade would silently invalidate a key
/// that describes unchanged data and re-ingest the registry once for no reason. This key is
/// persisted in the corpus and compared across builds, so it has to mean the same thing in
/// five years as it does today.
struct Fnv(u64);

impl Fnv {
    fn new() -> Self {
        Fnv(0xcbf2_9ce4_8422_2325)
    }
    /// The length prefix is what makes this a serialisation and not a concatenation:
    /// without it ("ab", "c") and ("a", "bc") hash the same, and two different registries
    /// could claim the same key.
    fn add(&mut self, s: &str) {
        self.push_u64(s.len() as u64);
        for b in s.as_bytes() {
            self.push_u8(*b);
        }
    }
    fn add_i64(&mut self, v: i64) {
        self.push_u64(v as u64);
    }
    fn add_bool(&mut self, v: bool) {
        self.push_u8(u8::from(v));
    }
    fn push_u8(&mut self, b: u8) {
        self.0 ^= b as u64;
        self.0 = self.0.wrapping_mul(0x0000_0100_0000_01b3);
    }
    fn push_u64(&mut self, v: u64) {
        for i in 0..8 {
            self.push_u8((v >> (i * 8)) as u8);
        }
    }
}

/// content_key identifies what this run WOULD write, not what it read.
///
/// The distinction is the whole point. Keying on the .asn file — its bytes, its mtime, its
/// path — would answer "is the input the same?", and that is the wrong question: the rows
/// come from `parse3gpp::asn1`, so a change in the PARSER produces different rows from an
/// identical file. An input key would call that corpus "already ingested" and the corpus
/// would keep the old rows for good, silently.
///
/// Hashing the parsed rows costs one parse of one text file (~0.2 s) and is immune to that
/// by construction: if a single field of a single row would differ, the key differs.
fn content_key(
    release: &str,
    module_version: &str,
    events: &[LiEventIn],
    fields: &[LiFieldIn],
    nf_clauses: &[LiNfClauseIn],
    types: &[Asn1TypeIn],
) -> String {
    let mut h = Fnv::new();
    h.add(LI_SPEC_ID);
    h.add(release);
    h.add(module_version);
    h.add_i64(events.len() as i64);
    for e in events {
        h.add(&e.interface);
        h.add(&e.event_name);
        h.add(&e.asn1_type);
        h.add_i64(e.asn1_tag);
        h.add(&e.originating_nf);
        h.add(&e.domain);
        h.add(&e.spec_clause);
        h.add_i64(e.field_count);
    }
    h.add_i64(fields.len() as i64);
    for f in fields {
        h.add(&f.interface);
        h.add(&f.event_name);
        h.add(&f.field_name);
        h.add(&f.asn1_type);
        h.add_i64(f.asn1_tag);
        h.add_bool(f.is_optional);
        h.add_i64(f.ordinal);
    }
    h.add_i64(nf_clauses.len() as i64);
    for c in nf_clauses {
        h.add(&c.originating_nf);
        h.add(&c.interface);
        h.add(&c.spec_clause);
    }
    h.add_i64(types.len() as i64);
    for t in types {
        h.add(&t.type_name);
        h.add(&t.kind);
        h.add(&t.members_json);
    }
    format!("{:016x}", h.0)
}

fn main() -> Result<()> {
    let args = Args::parse();
    let text = std::fs::read_to_string(&args.asn).with_context(|| format!("read {}", args.asn))?;
    let m = parse_module(&text).map_err(|e| anyhow::anyhow!(e))?;
    let release = args.release.unwrap_or_else(|| m.release.clone());

    let events: Vec<LiEventIn> = m
        .events
        .iter()
        .map(|e| LiEventIn {
            interface: e.interface.clone(),
            event_name: e.name.clone(),
            asn1_type: e.asn1_type.clone(),
            asn1_tag: e.tag as i64,
            originating_nf: e.nf.clone(),
            domain: e.domain.clone(),
            spec_clause: e.clause.clone(),
            field_count: e.field_count as i64,
        })
        .collect();
    let fields: Vec<LiFieldIn> = m
        .fields
        .iter()
        .map(|f| LiFieldIn {
            interface: f.interface.clone(),
            event_name: f.event_name.clone(),
            field_name: f.field_name.clone(),
            asn1_type: f.asn1_type.clone(),
            asn1_tag: f.tag as i64,
            is_optional: f.optional,
            ordinal: f.ordinal as i64,
        })
        .collect();
    let nf_clauses: Vec<LiNfClauseIn> = m
        .nf_clauses
        .iter()
        .map(|c| LiNfClauseIn {
            originating_nf: c.nf.clone(),
            interface: c.interface.clone(),
            spec_clause: c.clause.clone(),
        })
        .collect();
    let types: Vec<Asn1TypeIn> = m
        .types
        .iter()
        .map(|t| Asn1TypeIn {
            type_name: t.name.clone(),
            kind: t.kind.clone(),
            members_json: members_json(&t.members),
        })
        .collect();

    let store = Store::open_rw(&args.db)?;

    // REWRITING AN IDENTICAL REGISTRY IS NOT FREE. clear_li + write_li_registry replaces
    // 405 events, 1 697 fields, 43 nf-clauses and 1 039 types wholesale, and DuckDB writes
    // every one of them. Measured 2026-09-06 on the shipped 23 GB corpus: a run with
    // nothing new to say grew the file by 4.5 MiB.
    //
    // Four and a half megabytes is not the cost. The corpus is one layer of the published
    // image, and scripts/local/imgtar zeroes tar mtimes so a layer digest depends on
    // CONTENT alone: an unchanged corpus is answered "existing blob" and never crosses the
    // wire, one changed byte is a 21.9 GiB upload. `enrich` runs this on every build —
    // including the builds where `discover` only re-ran because its 6-hour TTL expired.
    //
    // PR #282 made the other four writers of `enrich` no-ops and left this one. Build 19
    // (2026-09-06) is the measurement: every other writer announced "corpus untouched",
    // this one wrote, the corpus moved by exactly 4.5 MiB inside this binary's window, and
    // 21.9 GiB were re-pushed for it.
    let key = content_key(
        &release,
        &m.module_version,
        &events,
        &fields,
        &nf_clauses,
        &types,
    );
    let meta_key = format!("{LI_CONTENT_KEY_META}:{release}");
    if store.get_meta(&meta_key)? == key {
        eprintln!(
            "ingest-li: {release} {} — the registry already carries these rows, corpus untouched",
            m.module_version
        );
        return Ok(());
    }

    store.clear_li(LI_SPEC_ID, &release)?;
    store.write_li_registry(
        LI_SPEC_ID,
        &release,
        &m.module_version,
        &events,
        &fields,
        &nf_clauses,
        &types,
    )?;
    // Stamped only after the rows are actually in. An interrupted run leaves a key that
    // does not match, and the next one rebuilds — the failure mode to avoid is the
    // opposite one, where a key outlives the rows it describes and the hole is permanent.
    store.set_meta(&meta_key, &key)?;
    store.checkpoint()?;
    eprintln!(
        "ingest-li: {release} {} → {} event(s), {} field(s), {} nf-clause(s), {} type(s)",
        m.module_version,
        events.len(),
        fields.len(),
        nf_clauses.len(),
        types.len()
    );
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn ev(name: &str, tag: i64) -> LiEventIn {
        LiEventIn {
            interface: "X2".into(),
            event_name: name.into(),
            asn1_type: "AMFRegistration".into(),
            asn1_tag: tag,
            originating_nf: "AMF".into(),
            domain: "5GC".into(),
            spec_clause: "6.2.2.2".into(),
            field_count: 3,
        }
    }
    fn fl(ordinal: i64, optional: bool) -> LiFieldIn {
        LiFieldIn {
            interface: "X2".into(),
            event_name: "AMFRegistration".into(),
            field_name: "sUPI".into(),
            asn1_type: "SUPI".into(),
            asn1_tag: 1,
            is_optional: optional,
            ordinal,
        }
    }
    fn nf() -> LiNfClauseIn {
        LiNfClauseIn {
            originating_nf: "AMF".into(),
            interface: "X2".into(),
            spec_clause: "6.2.2".into(),
        }
    }
    fn ty(name: &str) -> Asn1TypeIn {
        Asn1TypeIn {
            type_name: name.into(),
            kind: "SEQUENCE".into(),
            members_json: "[]".into(),
        }
    }

    fn key_of(
        evs: &[LiEventIn],
        fs: &[LiFieldIn],
        ns: &[LiNfClauseIn],
        ts: &[Asn1TypeIn],
    ) -> String {
        content_key("Rel-19", "version7", evs, fs, ns, ts)
    }

    /// The property the skip depends on: identical rows, identical key. If this fails the
    /// binary re-ingests on every build and the layer is re-pushed every time.
    #[test]
    fn same_rows_same_key() {
        let a = key_of(&[ev("A", 1)], &[fl(0, false)], &[nf()], &[ty("T")]);
        let b = key_of(&[ev("A", 1)], &[fl(0, false)], &[nf()], &[ty("T")]);
        assert_eq!(a, b);
    }

    /// THE NEGATIVE CONTROL, and the one that matters. A skip is only safe if every field
    /// that reaches the corpus reaches the key: anything omitted here is a change the
    /// binary would silently refuse to write, and the corpus would keep stale rows for
    /// good. One case per field, so a field added to a struct without being added to
    /// content_key fails here rather than in production.
    #[test]
    fn any_changed_field_changes_the_key() {
        let base = key_of(&[ev("A", 1)], &[fl(0, false)], &[nf()], &[ty("T")]);

        let mut cases: Vec<(&str, String)> = Vec::new();
        cases.push((
            "event_name",
            key_of(&[ev("B", 1)], &[fl(0, false)], &[nf()], &[ty("T")]),
        ));
        cases.push((
            "asn1_tag",
            key_of(&[ev("A", 2)], &[fl(0, false)], &[nf()], &[ty("T")]),
        ));
        cases.push((
            "field ordinal",
            key_of(&[ev("A", 1)], &[fl(1, false)], &[nf()], &[ty("T")]),
        ));
        cases.push((
            "is_optional",
            key_of(&[ev("A", 1)], &[fl(0, true)], &[nf()], &[ty("T")]),
        ));
        cases.push((
            "type_name",
            key_of(&[ev("A", 1)], &[fl(0, false)], &[nf()], &[ty("U")]),
        ));
        cases.push((
            "row count",
            key_of(
                &[ev("A", 1), ev("B", 2)],
                &[fl(0, false)],
                &[nf()],
                &[ty("T")],
            ),
        ));
        cases.push((
            "no events",
            key_of(&[], &[fl(0, false)], &[nf()], &[ty("T")]),
        ));
        cases.push((
            "release",
            content_key(
                "Rel-18",
                "version7",
                &[ev("A", 1)],
                &[fl(0, false)],
                &[nf()],
                &[ty("T")],
            ),
        ));
        cases.push((
            "module_version",
            content_key(
                "Rel-19",
                "version8",
                &[ev("A", 1)],
                &[fl(0, false)],
                &[nf()],
                &[ty("T")],
            ),
        ));

        for (what, k) in cases {
            assert_ne!(
                base, k,
                "{what} did not change the key — that change would never be written"
            );
        }
    }

    /// A concatenation would hash ("ab","c") and ("a","bc") the same. Two registries that
    /// differ only in where one string ends must not share a key.
    #[test]
    fn field_boundaries_are_part_of_the_key() {
        let a = key_of(&[ev("AB", 1)], &[fl(0, false)], &[nf()], &[ty("C")]);
        let b = key_of(&[ev("A", 1)], &[fl(0, false)], &[nf()], &[ty("BC")]);
        assert_ne!(a, b);
    }

    /// The stored value is compared as a string, so its shape is part of the contract.
    #[test]
    fn the_key_is_16_hex_digits() {
        let k = key_of(&[ev("A", 1)], &[fl(0, false)], &[nf()], &[ty("T")]);
        assert_eq!(k.len(), 16, "{k}");
        assert!(
            k.chars()
                .all(|c| c.is_ascii_hexdigit() && !c.is_ascii_uppercase()),
            "{k}"
        );
    }
}
