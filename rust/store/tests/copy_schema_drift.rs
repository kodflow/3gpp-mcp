//! A COMPACT COPY MUST SURVIVE AN ADDITIVE MIGRATION, and until 2026-09-07 it
//! could not.
//!
//! copy_database_compact bootstraps its DESTINATION from today's schema.sql and
//! reads its SOURCE from a corpus built whenever that corpus was built. Every table
//! was carried with `INSERT INTO dst SELECT * FROM src`, under a comment that said
//! the premise out loud: "Column order is identical — both sides were bootstrapped
//! from the same schema.sql — so SELECT * is the right shape, not a shortcut."
//!
//! The premise holds only while the schema never grows. Adding one nullable column
//! to `acronyms` killed the merge of a 22 GB corpus after ten minutes of work, and
//! the failure landed in the step AFTER the six-and-a-half-minute restore that
//! precedes it:
//!
//! ```text
//! copy table acronyms rows [0,20000000)
//! Binder Error: table acronyms has 7 columns but 6 values were supplied
//! ```
//!
//! Nothing smaller than a real corpus was going to show it: a fresh test database
//! is created from the same schema.sql as the destination, so both sides match and
//! the copy passes. The drift has to be MANUFACTURED, which is what this does.

struct Tmp(std::path::PathBuf);
impl Tmp {
    fn new(name: &str) -> Self {
        let mut p = std::env::temp_dir();
        p.push(format!("copy-drift-{}-{}", name, std::process::id()));
        let _ = std::fs::remove_dir_all(&p);
        std::fs::create_dir_all(&p).unwrap();
        Tmp(p)
    }
    fn path(&self, n: &str) -> String {
        self.0.join(n).to_str().unwrap().to_string()
    }
}
impl Drop for Tmp {
    fn drop(&mut self) {
        let _ = std::fs::remove_dir_all(&self.0);
    }
}

/// An "old" corpus: one whose acronyms table predates the declared_by column.
fn old_corpus(path: &str) {
    let store = store_rs::Store::open_rw(path).unwrap();
    store
        .upsert_acronym(
            "UICC",
            "Universal Integrated Circuit Card",
            "",
            "18.4.0",
            "18.4.0",
            "ETSI TS 102 221",
            5,
        )
        .unwrap();
    store
        .upsert_acronym(
            "AMF",
            "Access and Mobility Management Function",
            "",
            "Rel-19",
            "Rel-19",
            "23.501",
            0,
        )
        .unwrap();
    store
        .raw()
        .execute_batch("ALTER TABLE acronyms DROP COLUMN declared_by;")
        .unwrap();
}

#[test]
fn a_source_missing_a_column_the_destination_has_still_copies() {
    let t = Tmp::new("narrow");
    let src = t.path("src.duckdb");
    let dst = t.path("dst.duckdb");
    old_corpus(&src);

    store_rs::Store::copy_database_compact(&src, &dst)
        .expect("a corpus predating an additive column must still compact");

    let out = store_rs::Store::open_rw(&dst).unwrap();
    let conn = out.raw();
    let n: i64 = conn
        .query_row("SELECT count(*) FROM acronyms", [], |r| r.get(0))
        .unwrap();
    assert_eq!(n, 2, "the rows must be carried, not dropped");

    // The column the source did not have takes its default. NULL is the honest
    // answer — "not counted" — and Store.ResolveTerm reads it as one declaration.
    let nulls: i64 = conn
        .query_row(
            "SELECT count(*) FROM acronyms WHERE declared_by IS NULL",
            [],
            |r| r.get(0),
        )
        .unwrap();
    assert_eq!(
        nulls, 2,
        "the added column must exist and be NULL, not absent"
    );

    // And the values that WERE there are unchanged.
    let src_series: String = conn
        .query_row(
            "SELECT source_series FROM acronyms WHERE term = 'UICC'",
            [],
            |r| r.get(0),
        )
        .unwrap();
    assert_eq!(src_series, "ETSI TS 102 221");
}

/// The same shape one chunk at a time: the column list is computed once per table
/// and reused across row ranges, so a step small enough to force several ranges
/// must produce the same result rather than a half-copied table.
#[test]
fn the_named_column_copy_holds_across_row_ranges() {
    let t = Tmp::new("chunked");
    let src = t.path("src.duckdb");
    let dst = t.path("dst.duckdb");
    old_corpus(&src);

    store_rs::Store::copy_database_compact_step(&src, &dst, 1).unwrap();

    let out = store_rs::Store::open_rw(&dst).unwrap();
    let n: i64 = out
        .raw()
        .query_row("SELECT count(*) FROM acronyms", [], |r| r.get(0))
        .unwrap();
    assert_eq!(n, 2);
}
