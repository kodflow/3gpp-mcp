//! A RE-ENCODE REPLACES THE WHOLE SPARSE LAYER — the two `embed-io` flags the
//! pipeline reaches for when the postings in a corpus were written by another
//! producer (internal/goal/sparse_producer.go).
//!
//! A clause_sparse row does not say which build computed it. So the ordinary work
//! list, "clauses with no posting", is EMPTY exactly when every posting is stale —
//! a new sparse model, a new ort or tokenizers, a fix to the pooling — and the
//! ordinary import only replaces the chunk_ids its ledger names. Each flag closes
//! one of those two, and each test below fails if its flag stops doing it.

use std::path::Path;
use std::process::Command;

fn embed_io() -> &'static str {
    env!("CARGO_BIN_EXE_embed-io")
}

/// A corpus of `n` embeddable clauses, all in Rel-19.
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

/// A ledger giving every clause in `ids` a single posting of `weight`.
fn ledger(path: &Path, ids: std::ops::RangeInclusive<u64>, weight: f32) {
    use std::io::Write;
    let f = std::fs::File::create(path).unwrap();
    let mut w = std::io::BufWriter::new(f);
    for i in ids {
        writeln!(w, "{{\"chunk_id\":{i},\"terms\":[[7,{weight:.3}]]}}").unwrap();
    }
    w.flush().unwrap();
}

fn embed_io_ok(args: &[&str]) -> String {
    let out = Command::new(embed_io()).args(args).output().unwrap();
    assert!(out.status.success(), "embed-io {args:?} failed: {out:?}");
    String::from_utf8_lossy(&out.stderr).into_owned()
}

fn export(db: &str, wl: &Path, all: bool) -> Vec<u64> {
    let mut args = vec!["--db", db, "--export-sparse-worklist", wl.to_str().unwrap()];
    if all {
        args.push("--export-sparse-all");
    }
    embed_io_ok(&args);
    std::fs::read_to_string(wl)
        .unwrap()
        .lines()
        .filter(|l| !l.trim().is_empty())
        .map(|l| {
            serde_json::from_str::<serde_json::Value>(l).unwrap()["chunk_id"]
                .as_u64()
                .unwrap()
        })
        .collect()
}

fn posted(db: &str) -> Vec<u64> {
    let store = store_rs::Store::open_rw(db).unwrap();
    let mut stmt = store
        .raw()
        .prepare("SELECT DISTINCT chunk_id FROM clause_sparse ORDER BY chunk_id")
        .unwrap();
    stmt.query_map([], |r| r.get::<_, u64>(0))
        .unwrap()
        .map(|r| r.unwrap())
        .collect()
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

fn index_present(db: &str) -> bool {
    let store = store_rs::Store::open_rw(db).unwrap();
    store
        .raw()
        .query_row(
            "SELECT count(*) FROM duckdb_indexes() WHERE index_name = 'clause_sparse_term'",
            [],
            |r| r.get::<_, i64>(0),
        )
        .unwrap()
        > 0
}

struct Tmp(std::path::PathBuf);
impl Tmp {
    fn new(name: &str) -> Self {
        let mut p = std::env::temp_dir();
        p.push(format!("sparse-reenc-{}-{}", name, std::process::id()));
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

/// THE ORDINARY WORK LIST IS EMPTY ON A FULLY POSTED CORPUS, and that is the
/// defect: after a producer change it is empty exactly when everything is stale.
/// --export-sparse-all hands back every embeddable clause, posted or not.
#[test]
fn export_all_includes_clauses_that_already_carry_postings() {
    let t = Tmp::new("export");
    let db = t.db();
    seed(&db, 5);
    let led = t.join("l.jsonl");
    ledger(&led, 1..=3, 0.5);
    embed_io_ok(&["--db", &db, "--import-sparse", led.to_str().unwrap()]);

    let wl = t.join("w.jsonl");
    assert_eq!(
        export(&db, &wl, false),
        vec![4, 5],
        "the ordinary work list must still subtract the posted clauses"
    );
    assert_eq!(
        export(&db, &wl, true),
        vec![1, 2, 3, 4, 5],
        "--export-sparse-all left out clauses that already carry postings — a re-encode \
         would recompute only what was never posted and keep every stale posting"
    );
}

/// A REPLACE LEAVES NOTHING OF THE OLD LAYER. A clause the new ledger does not
/// name — clause 5 here — must lose its old posting, and a named one must carry
/// the NEW posting, not keep the old.
#[test]
fn replace_clears_every_posting_the_new_ledger_does_not_name() {
    let t = Tmp::new("replace");
    let db = t.db();
    seed(&db, 5);
    let led = t.join("l.jsonl");

    ledger(&led, 1..=5, 0.5);
    embed_io_ok(&["--db", &db, "--import-sparse", led.to_str().unwrap()]);
    assert_eq!(posted(&db), vec![1, 2, 3, 4, 5]);

    ledger(&led, 1..=3, 0.9);
    let log = embed_io_ok(&[
        "--db",
        &db,
        "--import-sparse",
        led.to_str().unwrap(),
        "--import-sparse-replace",
        "--sparse-model",
        "newproducer",
    ]);

    assert_eq!(
        posted(&db),
        vec![1, 2, 3],
        "the replace kept postings its ledger does not name — the old producer's, \
         now served under the new stamp: {log}"
    );
    assert!(
        (weight_of(&db, 1) - 0.9).abs() < 1e-6,
        "the replace did not write the new posting"
    );
    assert!(
        log.contains("cleared the postings of 5 clause(s)"),
        "the replace does not say what it cleared: {log}"
    );
    assert!(
        index_present(&db),
        "the term_id index was not rebuilt after the replace — every sparse query would scan the table"
    );
    let store = store_rs::Store::open_rw(&db).unwrap();
    assert_eq!(store.get_meta("sparse_model").unwrap(), "newproducer");
}

/// THE TWO IMPORT MODES CONTRADICT EACH OTHER, so passing both is an error, not a
/// silent choice of one.
#[test]
fn replace_and_changed_only_are_refused_together() {
    let t = Tmp::new("both");
    let db = t.db();
    seed(&db, 2);
    let led = t.join("l.jsonl");
    ledger(&led, 1..=2, 0.5);
    embed_io_ok(&["--db", &db, "--import-sparse", led.to_str().unwrap()]);

    let out = Command::new(embed_io())
        .args([
            "--db",
            &db,
            "--import-sparse",
            led.to_str().unwrap(),
            "--import-sparse-replace",
            "--import-sparse-changed-only",
        ])
        .output()
        .unwrap();
    assert!(
        !out.status.success(),
        "embed-io accepted --import-sparse-replace with --import-sparse-changed-only"
    );
    assert_eq!(
        posted(&db),
        vec![1, 2],
        "a refused import still touched the layer"
    );
}
