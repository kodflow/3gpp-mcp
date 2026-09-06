//! THE TRADE THE INCREMENTAL SPARSE IMPORT MAKES, PINNED IN BOTH DIRECTIONS.
//!
//! `embed-io --import-sparse` rewrites every posting in the ledger. That is
//! SELF-REPAIRING: postings damaged under a chunk_id that is still present are
//! restored on the next build, and no work list can see that case, because
//! `clauses_needing_sparse` only asks whether a clause has ANY posting.
//!
//! `--import-sparse-changed-only` skips the clauses that already have postings.
//! Measured on build 23 (2026-09-06, the ETSI half): 368 clauses needed postings
//! and took 9 seconds on the GPU, then the import rewrote all 2 000 182 and took
//! 49 MINUTES.
//!
//! Both behaviours are therefore load-bearing, and a test that only proved the
//! fast one would let someone "simplify" the slow one away. So each direction is
//! asserted, and each would fail if the other were made the default.

use std::path::Path;
use std::process::Command;

fn embed_io() -> &'static str {
    env!("CARGO_BIN_EXE_embed-io")
}

/// A corpus of `n` embeddable clauses.
fn seed(db: &str, n: u64) {
    let store = store_rs::Store::open_rw(db).unwrap();
    let mut sql = String::from("BEGIN;");
    for i in 1..=n {
        sql.push_str(&format!(
            "INSERT INTO clauses(chunk_id,spec_id,release,version,clause_path,heading,text,is_normative) \
             VALUES ({i},'23.501','Rel-19','19.5.0','5.{i}','H','body text number {i} with enough words',true);"
        ));
    }
    sql.push_str("COMMIT;");
    store.raw().execute_batch(&sql).unwrap();
}

/// A ledger giving every clause in 1..=n a single posting of `weight`.
fn ledger(path: &Path, n: u64, weight: f32) {
    use std::io::Write;
    let f = std::fs::File::create(path).unwrap();
    let mut w = std::io::BufWriter::new(f);
    for i in 1..=n {
        writeln!(w, "{{\"chunk_id\":{i},\"terms\":[[7,{weight:.3}]]}}").unwrap();
    }
    w.flush().unwrap();
}

fn run(db: &str, led: &Path, changed_only: bool) -> String {
    let mut c = Command::new(embed_io());
    c.args(["--db", db, "--import-sparse", led.to_str().unwrap()]);
    if changed_only {
        c.arg("--import-sparse-changed-only");
    }
    let out = c.output().unwrap();
    assert!(out.status.success(), "embed-io failed: {out:?}");
    String::from_utf8_lossy(&out.stderr).into_owned()
}

fn weight_of(db: &str, chunk_id: u64) -> f32 {
    let store = store_rs::Store::open_rw(db).unwrap();
    store
        .raw()
        .query_row(
            "SELECT weight FROM clause_sparse WHERE chunk_id = ?",
            [chunk_id],
            |r| r.get::<_, f32>(0),
        )
        .unwrap()
}

fn posted_clauses(db: &str) -> i64 {
    let store = store_rs::Store::open_rw(db).unwrap();
    store
        .raw()
        .query_row("SELECT count(DISTINCT chunk_id) FROM clause_sparse", [], |r| {
            r.get::<_, i64>(0)
        })
        .unwrap()
}

struct Tmp(std::path::PathBuf);
impl Tmp {
    fn new(name: &str) -> Self {
        let mut p = std::env::temp_dir();
        p.push(format!("sparse-inc-{}-{}", name, std::process::id()));
        let _ = std::fs::remove_dir_all(&p);
        std::fs::create_dir_all(&p).unwrap();
        Tmp(p)
    }
    fn join(&self, n: &str) -> std::path::PathBuf {
        self.0.join(n)
    }
    fn db(&self) -> String {
        self.join("c.duckdb").to_str().unwrap().to_string()
    }
}
impl Drop for Tmp {
    fn drop(&mut self) {
        let _ = std::fs::remove_dir_all(&self.0);
    }
}

/// THE FAST PATH SKIPS WHAT IS ALREADY POSTED — that is the whole point, and the
/// only way to see it is to make the stored value DIFFER from the ledger's and
/// check that the stored one survives.
#[test]
fn changed_only_leaves_an_already_posted_clause_alone() {
    let t = Tmp::new("skip");
    let db = t.db();
    seed(&db, 5);
    let led = t.join("l.jsonl");

    ledger(&led, 5, 0.5);
    run(&db, &led, false);
    assert_eq!(posted_clauses(&db), 5);
    assert!((weight_of(&db, 1) - 0.5).abs() < 1e-6);

    // The ledger now says something else about clause 1.
    ledger(&led, 5, 0.9);
    run(&db, &led, true);

    assert!(
        (weight_of(&db, 1) - 0.5).abs() < 1e-6,
        "the incremental import rewrote a clause that already had postings — \
         it is doing the 49 minutes of work it exists to avoid"
    );
}

/// THE SLOW PATH REPAIRS, and it is the reason the full import is kept. A corpus
/// whose postings were damaged under a chunk_id that is still present is invisible
/// to `clauses_needing_sparse`; only the rewrite fixes it.
#[test]
fn the_full_import_still_repairs_a_damaged_posting() {
    let t = Tmp::new("repair");
    let db = t.db();
    seed(&db, 5);
    let led = t.join("l.jsonl");

    ledger(&led, 5, 0.5);
    run(&db, &led, false);

    {
        let store = store_rs::Store::open_rw(&db).unwrap();
        store
            .raw()
            .execute_batch("UPDATE clause_sparse SET weight = 0.01 WHERE chunk_id = 1;")
            .unwrap();
    }
    assert!((weight_of(&db, 1) - 0.01).abs() < 1e-6, "fixture did not damage the posting");

    run(&db, &led, false);

    assert!(
        (weight_of(&db, 1) - 0.5).abs() < 1e-6,
        "the full import no longer repairs a damaged posting — the property the \
         incremental path deliberately gives up has been lost from BOTH paths"
    );
}

/// AND IT MUST STILL DO THE WORK. Skipping is only correct if the clauses that
/// genuinely have no postings are still written.
#[test]
fn changed_only_still_posts_the_new_clauses() {
    let t = Tmp::new("new");
    let db = t.db();
    seed(&db, 5);
    let led = t.join("l.jsonl");

    ledger(&led, 3, 0.5);
    run(&db, &led, false);
    assert_eq!(posted_clauses(&db), 3);

    ledger(&led, 5, 0.5);
    let log = run(&db, &led, true);

    assert_eq!(
        posted_clauses(&db),
        5,
        "the incremental import skipped clauses that had NO postings: {log}"
    );
}

/// A FIRST BAKE IS A BULK LOAD EVEN WITH THE FLAG. The index decision is on the
/// row count, not on the mode: an empty table means every insert is a bulk load,
/// and maintaining the ART index row by row there is the original seven-hour bug.
#[test]
fn a_first_bake_with_the_flag_is_still_treated_as_a_bulk_load() {
    let t = Tmp::new("first");
    let db = t.db();
    seed(&db, 5);
    let led = t.join("l.jsonl");
    ledger(&led, 5, 0.5);

    let log = run(&db, &led, true);

    assert_eq!(posted_clauses(&db), 5, "a first bake wrote nothing: {log}");
    assert!(
        !log.contains("term_id index kept"),
        "an empty clause_sparse was loaded with the term_id index in place: {log}"
    );
}
