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

    // INVALIDATE FIRST, WRITE SECOND, STAMP LAST. clear_li, write_li_registry and set_meta
    // commit separately, so "stamp only after the rows are in" is not enough on its own:
    // if clear_li commits and write_li_registry then fails, the rows are gone and the OLD
    // key survives to describe them. A later run that parses back to that old content —
    // the .asn reverted, a parser change rolled back — matches it, returns early, and the
    // registry stays empty for good. Blanking the key before touching a row makes the
    // window fail safe instead: nothing can equal "" (a key is always 16 hex digits), so
    // any interruption leaves a corpus that rebuilds on the next run.
    //
    // The alternative is to fold all three into one transaction inside store-rs. It is the
    // tidier shape and it is deliberately NOT taken here: the `merge` binary links
    // rust/store, so an edit there re-runs merge and re-derives the corpus — 37 min of
    // rewrite plus 22 min of paragraphs behind it, measured 2026-09-05. This orders three
    // existing commits and buys the same guarantee for one extra meta write, on the path
    // that was going to write anyway.
    store.set_meta(&meta_key, "")?;
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

    // One row of each kind, all fields set to something distinguishable. Every test below
    // starts from these and changes exactly one thing.
    fn base_ev() -> LiEventIn {
        LiEventIn {
            interface: "X2".into(),
            event_name: "AMFRegistration".into(),
            asn1_type: "AMFRegistrationType".into(),
            asn1_tag: 1,
            originating_nf: "AMF".into(),
            domain: "5GC".into(),
            spec_clause: "6.2.2.2".into(),
            field_count: 3,
        }
    }
    fn base_fl() -> LiFieldIn {
        LiFieldIn {
            interface: "X2".into(),
            event_name: "AMFRegistration".into(),
            field_name: "sUPI".into(),
            asn1_type: "SUPI".into(),
            asn1_tag: 2,
            is_optional: false,
            ordinal: 0,
        }
    }
    fn base_nf() -> LiNfClauseIn {
        LiNfClauseIn {
            originating_nf: "AMF".into(),
            interface: "X2".into(),
            spec_clause: "6.2.2".into(),
        }
    }
    fn base_ty() -> Asn1TypeIn {
        Asn1TypeIn {
            type_name: "AMFRegistrationType".into(),
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
    fn base_key() -> String {
        key_of(&[base_ev()], &[base_fl()], &[base_nf()], &[base_ty()])
    }

    // Four one-row registries, each with a single field mutated by the caller. Keeping the
    // other three rows at their base values is what makes a failure name one field.
    fn ev_with(f: impl FnOnce(&mut LiEventIn)) -> String {
        let mut r = base_ev();
        f(&mut r);
        key_of(&[r], &[base_fl()], &[base_nf()], &[base_ty()])
    }
    fn fl_with(f: impl FnOnce(&mut LiFieldIn)) -> String {
        let mut r = base_fl();
        f(&mut r);
        key_of(&[base_ev()], &[r], &[base_nf()], &[base_ty()])
    }
    fn nf_with(f: impl FnOnce(&mut LiNfClauseIn)) -> String {
        let mut r = base_nf();
        f(&mut r);
        key_of(&[base_ev()], &[base_fl()], &[r], &[base_ty()])
    }
    fn ty_with(f: impl FnOnce(&mut Asn1TypeIn)) -> String {
        let mut r = base_ty();
        f(&mut r);
        key_of(&[base_ev()], &[base_fl()], &[base_nf()], &[r])
    }

    /// The property the skip depends on: identical rows, identical key. If this fails the
    /// binary re-ingests on every build and the layer is re-pushed every time.
    #[test]
    fn same_rows_same_key() {
        assert_eq!(base_key(), base_key());
    }

    /// THE NEGATIVE CONTROL, and the one that matters. A skip is only safe if EVERY field
    /// that reaches the corpus reaches the key: a field left out of content_key is a change
    /// the binary would silently refuse to write, and the corpus would keep stale rows for
    /// good.
    ///
    /// Every field of every struct write_li_registry persists gets its own case. An earlier
    /// version of this test covered five of the twenty-one and still passed, which is
    /// exactly the hole it was supposed to close — hence `every_persisted_field_is_covered`
    /// below, which turns a newly added field into a compile error rather than trusting
    /// this list to be kept complete by hand.
    #[test]
    fn any_changed_field_changes_the_key() {
        let base = base_key();
        let cases: Vec<(&str, String)> = vec![
            (
                "LiEventIn.interface",
                ev_with(|r| r.interface = "X3".into()),
            ),
            (
                "LiEventIn.event_name",
                ev_with(|r| r.event_name = "AMFDeregistration".into()),
            ),
            (
                "LiEventIn.asn1_type",
                ev_with(|r| r.asn1_type = "Other".into()),
            ),
            ("LiEventIn.asn1_tag", ev_with(|r| r.asn1_tag = 99)),
            (
                "LiEventIn.originating_nf",
                ev_with(|r| r.originating_nf = "SMF".into()),
            ),
            ("LiEventIn.domain", ev_with(|r| r.domain = "EPC".into())),
            (
                "LiEventIn.spec_clause",
                ev_with(|r| r.spec_clause = "6.2.3.1".into()),
            ),
            ("LiEventIn.field_count", ev_with(|r| r.field_count = 4)),
            (
                "LiFieldIn.interface",
                fl_with(|r| r.interface = "X3".into()),
            ),
            (
                "LiFieldIn.event_name",
                fl_with(|r| r.event_name = "Other".into()),
            ),
            (
                "LiFieldIn.field_name",
                fl_with(|r| r.field_name = "gPSI".into()),
            ),
            (
                "LiFieldIn.asn1_type",
                fl_with(|r| r.asn1_type = "GPSI".into()),
            ),
            ("LiFieldIn.asn1_tag", fl_with(|r| r.asn1_tag = 99)),
            ("LiFieldIn.is_optional", fl_with(|r| r.is_optional = true)),
            ("LiFieldIn.ordinal", fl_with(|r| r.ordinal = 7)),
            (
                "LiNfClauseIn.originating_nf",
                nf_with(|r| r.originating_nf = "SMF".into()),
            ),
            (
                "LiNfClauseIn.interface",
                nf_with(|r| r.interface = "X3".into()),
            ),
            (
                "LiNfClauseIn.spec_clause",
                nf_with(|r| r.spec_clause = "6.2.4".into()),
            ),
            (
                "Asn1TypeIn.type_name",
                ty_with(|r| r.type_name = "Other".into()),
            ),
            ("Asn1TypeIn.kind", ty_with(|r| r.kind = "CHOICE".into())),
            (
                "Asn1TypeIn.members_json",
                ty_with(|r| r.members_json = r#"[{"n":"a"}]"#.into()),
            ),
        ];
        assert_eq!(
            cases.len(),
            21,
            "a field was added or removed without a case"
        );
        for (what, k) in cases {
            assert_ne!(
                base, k,
                "{what} is not part of the key — a change to it would never be written"
            );
        }
    }

    /// The list above is only as good as someone remembering to extend it. These
    /// destructurings carry no `..`, so adding a field to any of the four structs stops
    /// this file from COMPILING, and the compiler names the field. That is the reminder.
    #[test]
    fn every_persisted_field_is_covered() {
        let LiEventIn {
            interface: _,
            event_name: _,
            asn1_type: _,
            asn1_tag: _,
            originating_nf: _,
            domain: _,
            spec_clause: _,
            field_count: _,
        } = base_ev();
        let LiFieldIn {
            interface: _,
            event_name: _,
            field_name: _,
            asn1_type: _,
            asn1_tag: _,
            is_optional: _,
            ordinal: _,
        } = base_fl();
        let LiNfClauseIn {
            originating_nf: _,
            interface: _,
            spec_clause: _,
        } = base_nf();
        let Asn1TypeIn {
            type_name: _,
            kind: _,
            members_json: _,
        } = base_ty();
    }

    /// The rows are not the only thing written: the release and the module version travel
    /// into the corpus too, and the row COUNTS decide whether a row was dropped.
    #[test]
    fn shape_and_provenance_are_part_of_the_key() {
        let base = base_key();
        let cases: Vec<(&str, String)> = vec![
            (
                "release",
                content_key(
                    "Rel-18",
                    "version7",
                    &[base_ev()],
                    &[base_fl()],
                    &[base_nf()],
                    &[base_ty()],
                ),
            ),
            (
                "module_version",
                content_key(
                    "Rel-19",
                    "version8",
                    &[base_ev()],
                    &[base_fl()],
                    &[base_nf()],
                    &[base_ty()],
                ),
            ),
            (
                "an extra event",
                key_of(
                    &[base_ev(), base_ev()],
                    &[base_fl()],
                    &[base_nf()],
                    &[base_ty()],
                ),
            ),
            (
                "no events at all",
                key_of(&[], &[base_fl()], &[base_nf()], &[base_ty()]),
            ),
            (
                "no types at all",
                key_of(&[base_ev()], &[base_fl()], &[base_nf()], &[]),
            ),
        ];
        for (what, k) in cases {
            assert_ne!(base, k, "{what} is not part of the key");
        }
    }

    /// A concatenation would hash ("ab","c") and ("a","bc") the same. Two registries that
    /// differ only in where one string ends must not share a key.
    #[test]
    fn field_boundaries_are_part_of_the_key() {
        let a = ev_with(|r| r.event_name = "AB".into());
        let b = ev_with(|r| {
            r.event_name = "A".into();
            r.asn1_type = "BAMFRegistrationType".into();
        });
        assert_ne!(a, b);
    }

    /// The stored value is compared as a string, and the empty string is what the binary
    /// writes to invalidate the key before it touches a row. A real key must never be able
    /// to collide with that, or an interrupted run would look complete.
    #[test]
    fn the_key_is_16_hex_digits_and_never_empty() {
        let k = base_key();
        assert_eq!(k.len(), 16, "{k}");
        assert!(!k.is_empty());
        assert!(
            k.chars()
                .all(|c| c.is_ascii_hexdigit() && !c.is_ascii_uppercase()),
            "{k}"
        );
    }
}
