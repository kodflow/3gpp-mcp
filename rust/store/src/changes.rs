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
use sha2::{Digest, Sha256};

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

/// put length-prefixes one field into the digest.
///
/// A SEPARATOR WOULD HAVE TO BE A BYTE THE DATA CANNOT CONTAIN, and CR summaries
/// are free text out of a spreadsheet — there is no such byte. Length prefixes make
/// ("ab","c") and ("a","bc") different by construction instead of by hope.
fn put(h: &mut Sha256, b: &[u8]) {
    h.update((b.len() as u64).to_le_bytes());
    h.update(b);
}

/// changes_digest names the OUTPUT of a run: the exact rows that would be written,
/// in order, under the export they came from.
///
/// KEYED ON THE OUTPUT, NOT THE INPUT, which is the whole point of it existing.
/// Keying on the export name — "have I already loaded CRDB_20260715?" — is cheaper
/// and wrong: a fix to `rust/parse/src/crdb.rs` makes different rows out of the same
/// zip, and a run that skipped on the name would leave the corpus holding what the
/// OLD parser made of it while `changes_source` insisted it was current. That is the
/// mistake `ingest-li` (#286) and `ingest-etsi` (#316) each paid for separately.
///
/// The scheme tag is part of the hash so that changing WHAT is hashed cannot collide
/// with a digest written by the previous scheme.
fn changes_digest(rows: &[&ChangeRow], source: &str) -> String {
    let mut h = Sha256::new();
    h.update(b"changes-v1");
    put(&mut h, source.as_bytes());
    h.update((rows.len() as u64).to_le_bytes());
    for r in rows {
        put(&mut h, r.spec_id.as_bytes());
        put(&mut h, r.cr_number.as_bytes());
        // Some(0) and None are different facts about a CR and must not hash alike.
        match r.cr_revision {
            Some(v) => {
                h.update([1u8]);
                h.update(v.to_le_bytes());
            }
            None => h.update([0u8]),
        }
        put(&mut h, r.summary.as_bytes());
        put(&mut h, r.meeting.as_bytes());
        put(&mut h, r.category.as_bytes());
        put(&mut h, r.from_version.as_bytes());
        put(&mut h, r.to_version.as_bytes());
        put(&mut h, r.tdoc.as_bytes());
    }
    hex::encode(h.finalize())
}

impl Store {
    /// replace_changes rewrites the whole `changes` table from the CR database and
    /// answers `(written, skipped, changed)` — skipped being rows for specs this
    /// corpus does not hold.
    ///
    /// A TUPLE AND NOT A STRUCT, deliberately. A named type would have to be reached
    /// through `lib.rs`, and `mod changes` is private precisely so that `merge` —
    /// which declares `lib.rs` in full — is not dragged into every edit of this
    /// file. Naming the outcome would have cost 34m15 of merge plus a 42 GB re-push
    /// to make one return value prettier. `changed` is last and is the only
    /// non-`usize`, so the two counts cannot be silently swapped with it.
    ///
    /// WHAT `changed` BUYS, and why "the table is idempotent" was not enough. The
    /// table always was: run the overlay twice and it holds the same rows, which is
    /// what the paragraph below means. The FILE was not. A DELETE of 256 471 rows
    /// followed by an INSERT of the same 256 471 rows leaves DuckDB blocks that are
    /// not the blocks it started with; the corpus is ONE image layer per half and
    /// layers are addressed by content, so an overlay that changed nothing still
    /// bought a full re-push of the 3GPP corpus. Measured 2026-09-10: `enrich`
    /// replayed because `discover` refreshed `status-report.htm` — a live page whose
    /// bytes move on their own — `ingest-catalog` reported `0 spec(s) overlaid`, and
    /// the corpus still moved. `changed` is how `ingest-crs` can say "corpus
    /// untouched" and mean the file, not just the rows.
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
    pub fn replace_changes(
        &self,
        rows: &[ChangeRow],
        source: &str,
    ) -> Result<(usize, usize, bool)> {
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

        // The filter that used to live inside the insert loop, hoisted so the digest
        // can be taken over exactly the rows that will be written and nothing else.
        let keep: Vec<&ChangeRow> = rows.iter().filter(|r| held.contains(&r.spec_id)).collect();
        let skipped = rows.len() - keep.len();
        let written = keep.len();
        let digest = changes_digest(&keep, source);

        // THE GUARD ASKS THE CORPUS, NOT ONLY THE LEDGER.
        //
        // A recorded digest on its own is a CLAIM: it says what some earlier run
        // meant to leave behind, not what is there now. `changes` could have been
        // emptied by a restore, a hand-run repair, or a build that died between the
        // two. Counting the rows is the cheap half of the answer that cannot be
        // asserted, so both halves must agree before a rewrite is skipped — the same
        // reason `TestNoArmExceptionOutlivesItsStep` reads the exception map instead
        // of trusting it.
        let held_digest = self.get_meta("changes_digest")?;
        if held_digest == digest {
            let n: i64 = self
                .conn
                .query_row("SELECT count(*) FROM changes", [], |r| r.get(0))
                .context("count the changelog the digest claims to describe")?;
            if n as usize == written {
                return Ok((written, skipped, false));
            }
        }

        self.conn
            .execute_batch("BEGIN; DELETE FROM changes;")
            .context("replace_changes: clear")?;

        let res = (|| -> Result<()> {
            // `clauses` is left out of the column list so it defaults to NULL — see
            // ChangeRow for why the CR database cannot fill it.
            let mut st = self.conn.prepare(
                "INSERT INTO changes
                   (cr_number, cr_revision, spec_id, from_version, to_version,
                    meeting, category, summary, tdoc_url)
                 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
            )?;
            for r in &keep {
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
            // THE DIGEST RIDES WITH THE DATA FOR THE SAME REASON THE SOURCE DOES,
            // and it is load-bearing in a way the source name is not: a digest
            // committed while the rows were not would make the NEXT run skip a
            // rewrite the corpus still needs. Inside this transaction the two cannot
            // disagree, so the guard above is reading a fact and not a promise.
            self.conn
                .execute(
                    "INSERT INTO schema_meta(key, value) VALUES ('changes_digest', ?)
                     ON CONFLICT (key) DO UPDATE SET value = excluded.value",
                    duckdb::params![digest],
                )
                .context("stamp changes_digest")?;
            Ok(())
        })();

        match res {
            Ok(()) => {
                self.conn
                    .execute_batch("COMMIT;")
                    .context("replace_changes: commit")?;
                Ok((written, skipped, true))
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
