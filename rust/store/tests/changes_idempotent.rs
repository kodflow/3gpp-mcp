//! AN IDEMPOTENT TABLE IS NOT AN IDEMPOTENT FILE.
//!
//! `replace_changes` was already idempotent in the sense its own doc comment
//! claimed: run the CR overlay twice and `changes` holds the same 256 471 rows. But
//! it reached that state by DELETEing every row and INSERTing every row again, and
//! the blocks DuckDB writes the second time are not the blocks it wrote the first.
//! The corpus is ONE image layer per half and layers are addressed by content, so
//! an overlay that changed nothing still bought a full re-push of the 3GPP corpus.
//!
//! Measured on 2026-09-10: `enrich` replayed because `discover` had refreshed
//! `.local/state/status-report.htm` — a live page whose bytes move on their own —
//! `ingest-catalog` reported `0 spec(s) overlaid, 0 release(s)`, `ingest-openapi`,
//! `ingest-li` and `seed-evolutions` all reported "corpus untouched", and
//! `data/3gpp.duckdb` moved anyway.
//!
//! WHAT THESE TESTS PIN is not "it is fast now". It is that the skip is keyed on
//! what would be WRITTEN and verified against what the corpus HOLDS — the two
//! failure modes that make a cheap guard worse than none.

struct Tmp(std::path::PathBuf);
impl Tmp {
    fn new(name: &str) -> Self {
        let mut p = std::env::temp_dir();
        p.push(format!(
            "changes-idempotent-{}-{}",
            name,
            std::process::id()
        ));
        let _ = std::fs::remove_dir_all(&p);
        std::fs::create_dir_all(&p).unwrap();
        Tmp(p)
    }
    fn db(&self) -> String {
        self.0.join("c.duckdb").to_str().unwrap().to_string()
    }
}
impl Drop for Tmp {
    fn drop(&mut self) {
        let _ = std::fs::remove_dir_all(&self.0);
    }
}

fn row(spec: &str, cr: &str, summary: &str) -> store_rs::ChangeRow {
    store_rs::ChangeRow {
        spec_id: spec.into(),
        cr_number: cr.into(),
        cr_revision: Some(1),
        summary: summary.into(),
        meeting: "SA#105".into(),
        category: "F".into(),
        from_version: "18.3.0".into(),
        to_version: "18.4.0".into(),
        tdoc: "SP-241234".into(),
    }
}

fn held(store: &store_rs::Store) -> Vec<(String, String, String)> {
    let conn = store.raw();
    let mut st = conn
        .prepare("SELECT spec_id, cr_number, summary FROM changes ORDER BY spec_id, cr_number")
        .unwrap();
    let it = st
        .query_map([], |r| {
            Ok((
                r.get::<_, String>(0)?,
                r.get::<_, String>(1)?,
                r.get::<_, String>(2)?,
            ))
        })
        .unwrap();
    it.map(|r| r.unwrap()).collect()
}

fn corpus(t: &Tmp) -> store_rs::Store {
    let store = store_rs::Store::open_rw(&t.db()).unwrap();
    store
        .raw()
        .execute_batch("INSERT INTO specs(spec_id) VALUES ('23.501'),('23.502');")
        .unwrap();
    store
}

#[test]
fn a_second_identical_overlay_writes_nothing() {
    let t = Tmp::new("same");
    let store = corpus(&t);
    let rows = vec![
        row("23.501", "0001", "AMF registration"),
        row("23.502", "0002", "PDU session establishment"),
    ];

    let first = store.replace_changes(&rows, "CRDB_20260715").unwrap();
    assert_eq!(first, (2, 0, true), "the first overlay must write");

    let second = store.replace_changes(&rows, "CRDB_20260715").unwrap();
    assert_eq!(
        second,
        (2, 0, false),
        "an identical overlay must report the same rows and NOT write them"
    );
    assert_eq!(held(&store).len(), 2, "and the table must be intact");
}

#[test]
fn a_reparse_of_the_same_export_still_writes() {
    // THE TEST THAT DECIDES WHETHER THE KEY IS ON THE OUTPUT OR THE INPUT.
    //
    // Same `CRDB_20260715`, different rows out of it — which is exactly what a fix
    // to rust/parse/src/crdb.rs produces. A guard keyed on the export NAME would
    // skip here and leave the corpus holding what the old parser made of the zip,
    // with `changes_source` insisting it was current. That is the shape `ingest-li`
    // and `ingest-etsi` each paid for.
    let t = Tmp::new("reparse");
    let store = corpus(&t);

    let before = vec![row("23.501", "0001", "AMF registration")];
    assert_eq!(
        store.replace_changes(&before, "CRDB_20260715").unwrap(),
        (1, 0, true)
    );

    let after = vec![row("23.501", "0001", "AMF registration procedure")];
    let out = store.replace_changes(&after, "CRDB_20260715").unwrap();
    assert_eq!(
        out,
        (1, 0, true),
        "the same export parsed differently must be written"
    );
    assert_eq!(held(&store)[0].2, "AMF registration procedure");
}

#[test]
fn a_skipped_row_is_not_in_the_digest() {
    // A spec the corpus cannot quote is dropped, so two CR databases that differ
    // ONLY in rows for such specs produce the same table — and must not be
    // rewritten. This is what makes the digest describe the output rather than the
    // input it came from.
    let t = Tmp::new("skip");
    let store = corpus(&t);

    let lean = vec![row("23.501", "0001", "AMF registration")];
    assert_eq!(
        store.replace_changes(&lean, "CRDB_20260715").unwrap(),
        (1, 0, true)
    );

    let mut fat = lean.clone_rows();
    fat.push(row(
        "38.331",
        "0009",
        "a spec this corpus holds no text for",
    ));
    let out = store.replace_changes(&fat, "CRDB_20260715").unwrap();
    assert_eq!(
        out,
        (1, 1, false),
        "one row skipped, and the table it would produce is the one already there"
    );
}

#[test]
fn an_emptied_table_is_rewritten_though_the_digest_still_matches() {
    // THE GUARD MUST ASK THE CORPUS, NOT ONLY THE LEDGER.
    //
    // `changes_digest` records what an earlier run MEANT to leave behind. If that
    // were the whole check, a corpus whose changelog had been emptied — by a
    // restore, a hand-run repair, a build that died between two steps — would be
    // left empty for ever, and `get_changelog` would answer "no change recorded"
    // for every spec while the stamp said the export was loaded.
    let t = Tmp::new("emptied");
    let store = corpus(&t);
    let rows = vec![row("23.501", "0001", "AMF registration")];

    assert_eq!(
        store.replace_changes(&rows, "CRDB_20260715").unwrap(),
        (1, 0, true)
    );
    store
        .raw()
        .execute_batch("DELETE FROM changes;")
        .expect("empty the table behind the writer's back");

    let out = store.replace_changes(&rows, "CRDB_20260715").unwrap();
    assert_eq!(
        out,
        (1, 0, true),
        "the digest matches but the rows are gone — it must rewrite"
    );
    assert_eq!(held(&store).len(), 1);
}

#[test]
fn a_new_export_writes_even_when_the_rows_are_identical() {
    // The stamp is part of the digest because `changes_source` is a fact
    // get_changelog serves: "the changelog stops at 19.4.0" means something
    // different under a July export than under a September one. Identical rows
    // under a newer export must still update the stamp.
    let t = Tmp::new("newexport");
    let store = corpus(&t);
    let rows = vec![row("23.501", "0001", "AMF registration")];

    assert_eq!(
        store.replace_changes(&rows, "CRDB_20260715").unwrap(),
        (1, 0, true)
    );
    let out = store.replace_changes(&rows, "CRDB_20260930").unwrap();
    assert_eq!(
        out,
        (1, 0, true),
        "a newer export must land even when it says the same thing"
    );
    assert_eq!(store.get_meta("changes_source").unwrap(), "CRDB_20260930");
}

/// ChangeRow is not Clone (it does not need to be in production), so the one test
/// that needs a second copy builds it here rather than widening the type.
trait CloneRows {
    fn clone_rows(&self) -> Vec<store_rs::ChangeRow>;
}
impl CloneRows for Vec<store_rs::ChangeRow> {
    fn clone_rows(&self) -> Vec<store_rs::ChangeRow> {
        self.iter()
            .map(|r| store_rs::ChangeRow {
                spec_id: r.spec_id.clone(),
                cr_number: r.cr_number.clone(),
                cr_revision: r.cr_revision,
                summary: r.summary.clone(),
                meeting: r.meeting.clone(),
                category: r.category.clone(),
                from_version: r.from_version.clone(),
                to_version: r.to_version.clone(),
                tdoc: r.tdoc.clone(),
            })
            .collect()
    }
}
