//! THE MINED VOCABULARY IS A SNAPSHOT, NOT AN ACCUMULATION.
//!
//! ingest-glossary reads the NEWEST version of every ETSI deliverable and used to
//! upsert what it found. That can only grow the table: an expansion a later
//! version corrected — or dropped — kept its row for ever beside the current one,
//! and resolve_term went on offering both. The corpus is supposed to say what the
//! archive says NOW.
//!
//! The scope of the replacement is the PROVENANCE, so this could not have been
//! written while every row carried the constant "etsi": that value named no
//! document, and a purge keyed on it could not tell a mined row from anything else
//! someone might later stamp the same way. Rows now name their deliverable —
//! "ETSI TS 103 221-1" — and the legacy constant is swept with them, once.

struct Tmp(std::path::PathBuf);
impl Tmp {
    fn new(name: &str) -> Self {
        let mut p = std::env::temp_dir();
        p.push(format!("glossary-replace-{}-{}", name, std::process::id()));
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

fn rows(store: &store_rs::Store) -> Vec<(String, String, String, i64)> {
    let conn = store.raw();
    let mut stmt = conn
        .prepare(
            "SELECT term, expansion, coalesce(source_series,''), coalesce(declared_by,0)
               FROM acronyms ORDER BY term, expansion",
        )
        .unwrap();
    let it = stmt
        .query_map([], |r| {
            Ok((
                r.get::<_, String>(0)?,
                r.get::<_, String>(1)?,
                r.get::<_, String>(2)?,
                r.get::<_, i64>(3)?,
            ))
        })
        .unwrap();
    it.map(|r| r.unwrap()).collect()
}

fn mined(term: &str, expansion: &str, source: &str, n: i64) -> store_rs::MinedAcronym {
    store_rs::MinedAcronym {
        term: term.into(),
        expansion: expansion.into(),
        version: "18.4.0".into(),
        source: source.into(),
        declared_by: n,
    }
}

#[test]
fn a_replaced_vocabulary_drops_what_the_archive_no_longer_says() {
    let t = Tmp::new("drop");
    let store = store_rs::Store::open_rw(&t.db()).unwrap();

    // What a previous pass left: one row under the legacy constant, one under a
    // deliverable — and a 3GPP row that MUST survive, because the purge is scoped
    // by provenance and a corpus can hold both.
    store
        .upsert_acronym(
            "MSC",
            "Main Service Channel",
            "",
            "1.0.0",
            "1.0.0",
            "etsi",
            0,
        )
        .unwrap();
    store
        .upsert_acronym(
            "MSC",
            "Mobile Switching Centre",
            "",
            "1.0.0",
            "1.0.0",
            "ETSI TS 101 200",
            4,
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

    let n = store
        .replace_mined_acronyms(&[mined(
            "MSC",
            "Mobile Switching Center",
            "ETSI EN 302 094-1",
            8,
        )])
        .unwrap();
    assert_eq!(n, 1);

    let got = rows(&store);
    assert_eq!(
        got,
        vec![
            (
                "AMF".to_string(),
                "Access and Mobility Management Function".to_string(),
                "23.501".to_string(),
                0
            ),
            (
                "MSC".to_string(),
                "Mobile Switching Center".to_string(),
                "ETSI EN 302 094-1".to_string(),
                8
            ),
        ],
        "the mined rows must be replaced and the 3GPP row left alone"
    );
}

/// Running the same pass twice must leave the same table. An additive writer
/// passes this trivially; a replacing one only passes it if the delete and the
/// insert agree on scope — which is the half that can go wrong.
#[test]
fn replacing_twice_is_replacing_once() {
    let t = Tmp::new("idem");
    let store = store_rs::Store::open_rw(&t.db()).unwrap();
    let batch = [
        mined(
            "UICC",
            "Universal Integrated Circuit Card",
            "ETSI TS 102 221",
            5,
        ),
        mined("ADF", "Application Dedicated File", "ETSI TS 102 221", 3),
    ];

    store.replace_mined_acronyms(&batch).unwrap();
    let first = rows(&store);
    store.replace_mined_acronyms(&batch).unwrap();
    assert_eq!(rows(&store), first);
    assert_eq!(first.len(), 2);
}

/// A pass that fails part-way must not commit a half-emptied glossary: the delete
/// and the writes are ONE transaction, and the only way to show it is to make the
/// write fail after the delete has already run. An empty term is the provocation
/// because it is a real refusal rather than a fake constraint — such a row answers
/// nothing and can never be found again to be removed.
#[test]
fn a_failed_replace_leaves_the_previous_vocabulary_intact() {
    let t = Tmp::new("atomic");
    let store = store_rs::Store::open_rw(&t.db()).unwrap();
    store
        .replace_mined_acronyms(&[mined(
            "UICC",
            "Universal Integrated Circuit Card",
            "ETSI TS 102 221",
            5,
        )])
        .unwrap();
    let before = rows(&store);
    assert_eq!(before.len(), 1);

    let err = store
        .replace_mined_acronyms(&[
            mined("ADF", "Application Dedicated File", "ETSI TS 102 221", 3),
            mined("", "Nothing declares this", "ETSI TS 102 221", 1),
        ])
        .unwrap_err();
    assert!(
        err.to_string().contains("empty term"),
        "unexpected error: {err}"
    );

    assert_eq!(
        rows(&store),
        before,
        "the delete committed without its inserts: the glossary would have been          emptied by a batch that failed"
    );
}
