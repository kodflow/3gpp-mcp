//! changes.rs — the write path `ingest-crs` owns: the 3GPP change-request
//! overlay.
//!
//! WHY IT IS A SEPARATE FILE, for the same reason vectors.rs is and with the same
//! measurement behind it. `goal` tracks provenance PER FILE, and `merge` declares
//! `rust/store/src/lib.rs` because it genuinely links the store library. While the
//! ledger import lived in lib.rs, editing it invalidated `merge` and bought a
//! 38-minute reconstruction of a corpus the edit could not affect (build 23,
//! 2026-09-06). A changelog writer in lib.rs would cost the same every time it was
//! touched: merge 34m15, then paragraphs 20m32, compact 19m22 and index 8m06
//! behind it, plus a 42 GB image re-push — measured on build D.
//!
//! So the writer lives here and `enrich` declares THIS FILE, which is the step that
//! actually runs `ingest-crs`. That is the narrow direction, and the narrow
//! direction is the dangerous one when it is wrong — a step that does not replay
//! ships a stale corpus and nothing says so. It is correct here for a reason that
//! can be checked rather than asserted: `replace_changes` is called by
//! `ingest-crs` and by nothing else, and `ingest-crs` is run by `enrich` and by
//! nothing else. Adding a second caller means adding this file to that caller's
//! step, and `TestEnrichDeclaresTheChangelogWriter` fails until it is.
//!
//! Declaring `pub mod` in lib.rs is a one-time cost paid deliberately: it moves
//! merge's fingerprint once, and every later edit to the changelog costs one
//! minute of `enrich` instead of two hours of pipeline.

use super::*;
use anyhow::{Context, Result};

/// A change-request row for the CR-database overlay (== Go model.Change, minus
/// `clauses`).
///
/// `clauses` is absent on purpose rather than by omission: the CR database records
/// WHICH change was made to which spec at which version transition, not which
/// clause paths it touched. The column stays NULL, and `trace_clause` remains the
/// tool that answers "what happened to THIS clause".
pub struct ChangeRow {
    pub spec_id: String,
    pub cr_number: String,
    pub cr_revision: Option<i32>,
    pub summary: String,
    pub meeting: String,
    pub category: String,
    pub from_version: String,
    pub to_version: String,
    pub tdoc: String,
}

impl Store {
    /// replace_changes rewrites the whole `changes` table from the CR database and
    /// answers (written, skipped) — skipped being rows for specs this corpus does
    /// not hold.
    ///
    /// REPLACE, NOT APPEND. The CR database is the complete authority for 3GPP
    /// change requests, so whatever is already in the table is a previous
    /// generation of the same fact. Appending would leave the 3 026 "Date" header
    /// rows and the rest of the fossil sitting beside the real records, and the
    /// corpus would hold two answers to one question.
    ///
    /// It is also what makes the step idempotent on its OUTPUT rather than its
    /// input — the lesson `ingest-li` (#286) and `ingest-etsi` (#316) each paid for
    /// separately, the second of them fifteen builds in a row. Run this twice and
    /// the table is identical, because what ends up written depends only on the
    /// database read and the specs held, never on how many times it ran.
    ///
    /// CITE-OR-SILENT, like every other overlay here. The CR database covers specs
    /// this corpus holds no text for, and a changelog entry for a spec we cannot
    /// quote is a claim with nothing behind it. The filter runs in Rust against the
    /// spec set rather than as a SQL `IN (SELECT …)` because the set is ~3 500 rows
    /// against ~600 000 on the CR side: pulling the small side into memory once
    /// beats making the database re-derive it per row.
    ///
    /// ONE TRANSACTION, on purpose, and the opposite call from `copy_database_compact`.
    /// That one documents why a single statement per table does not scale — but
    /// its wall was 265 MILLION rows. This is 596 696, and here atomicity is what
    /// matters: a DELETE that committed without its INSERT would leave the corpus
    /// with no changelog at all, which is strictly worse than the fossil it
    /// replaces.
    pub fn replace_changes(&self, rows: &[ChangeRow], source: &str) -> Result<(usize, usize)> {
        // A DISCARDED SCAN ERROR IS A SHORTER SPEC LIST, AND A SHORTER SPEC LIST IS
        // SILENTLY FEWER CHANGES.
        //
        // `filter_map(Result::ok)` here would turn a driver error into "this corpus
        // does not hold that spec", and every row for it would be counted as
        // skipped rather than lost — the run would report success and the changelog
        // would simply be short. It is the same defect `checkNoReingest` was fixed
        // for in ad70abd, where a discarded Scan error made an unread corpus look
        // clean. Both the row error and rows.Err() equivalent are propagated.
        let held: std::collections::HashSet<String> = {
            let mut st = self.conn.prepare("SELECT spec_id FROM specs")?;
            let it = st.query_map([], |r| r.get::<_, String>(0))?;
            let mut set = std::collections::HashSet::new();
            for r in it {
                set.insert(r.context("read the spec list the changelog is filtered against")?);
            }
            set
        };

        self.conn
            .execute_batch("BEGIN; DELETE FROM changes;")
            .context("replace_changes: clear")?;

        let mut written = 0usize;
        let mut skipped = 0usize;
        let res = (|| -> Result<()> {
            // `clauses` is left out of the column list so it defaults to NULL — see
            // ChangeRow for why the CR database cannot fill it.
            let mut st = self.conn.prepare(
                "INSERT INTO changes
                   (cr_number, cr_revision, spec_id, from_version, to_version,
                    meeting, category, summary, tdoc_url)
                 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
            )?;
            for r in rows {
                if !held.contains(&r.spec_id) {
                    skipped += 1;
                    continue;
                }
                st.execute(duckdb::params![
                    r.cr_number,
                    r.cr_revision,
                    r.spec_id,
                    r.from_version,
                    r.to_version,
                    r.meeting,
                    r.category,
                    r.summary,
                    r.tdoc,
                ])
                .with_context(|| format!("insert change {} {}", r.spec_id, r.cr_number))?;
                written += 1;
            }
            // THE STAMP RIDES WITH THE DATA IT DESCRIBES.
            //
            // Written after the COMMIT it would be a separate failure point: the
            // rows would land under the PREVIOUS export's name, and get_changelog
            // would then tell readers, precisely and wrongly, which export its
            // silence came from. Inside the transaction the two cannot disagree.
            self.conn
                .execute(
                    "INSERT INTO schema_meta(key, value) VALUES ('changes_source', ?)
                     ON CONFLICT (key) DO UPDATE SET value = excluded.value",
                    duckdb::params![source],
                )
                .context("stamp changes_source")?;
            Ok(())
        })();

        match res {
            Ok(()) => {
                self.conn
                    .execute_batch("COMMIT;")
                    .context("replace_changes: commit")?;
                Ok((written, skipped))
            }
            Err(e) => {
                // Leaving the transaction open would make every later statement on
                // this connection fail with a message about the transaction rather
                // than about what actually went wrong here.
                let _ = self.conn.execute_batch("ROLLBACK;");
                Err(e)
            }
        }
    }
}
