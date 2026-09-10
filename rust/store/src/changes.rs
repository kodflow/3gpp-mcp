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

/// row_hash reduces one change record to a number, so that a whole changelog can be
/// compared to another without holding both.
///
/// It hashes what the row IS, and nothing about where it came from or where it sits.
/// That is what lets the same function be used on the rows read back out of the
/// corpus and on the rows a run is about to write: if the two multisets differ by so
/// much as one character of one summary, the sums differ.
fn row_hash(r: &ChangeRow) -> u128 {
    // A row this crate is about to write has a value in every string column and
    // leaves `clauses` NULL — see ChangeRow for why the CR database cannot fill it.
    hash_parts(
        &[
            Some(r.spec_id.as_str()),
            Some(r.cr_number.as_str()),
            Some(r.summary.as_str()),
            Some(r.meeting.as_str()),
            Some(r.category.as_str()),
            Some(r.from_version.as_str()),
            Some(r.to_version.as_str()),
            Some(r.tdoc.as_str()),
            None,
        ],
        r.cr_revision,
    )
}

/// hash_parts is the one definition of what a change record hashes to, so the rows
/// read back out of the corpus and the rows about to be written cannot drift apart
/// by being hashed in two places.
///
/// NULL IS NOT THE EMPTY STRING, and here that is a correctness property rather
/// than tidiness. `store.GetChangelog` scans these columns into plain Go strings, so
/// a NULL where the reader expects text makes the whole call fail — the defect #322
/// fixed for `cr_revision` and `clauses`, which was invisible for as long as the
/// table had no writer. Coercing both to "" would let this guard declare a
/// changelog the server cannot read identical to one it can, and skip the repair.
/// Measured on the corpus published 2026-09-10: 268 rows carry at least one empty
/// string, and none carry a NULL one — so the two are distinguishable today and the
/// guard must keep them so.
///
/// THE LAST SLOT IS `clauses`, which this writer always leaves NULL and which is
/// not on ChangeRow. Leaving it out of the hash was a hole with a working
/// counterexample: set `clauses` on one row out of band and the count, the sum and
/// the source stamp all still match, so the overlay reports "corpus untouched" and
/// preserves an invented clause association — one that `find_cross_references` and
/// `get_changelog` then filter on.
fn hash_parts(text: &[Option<&str>; 9], revision: Option<i32>) -> u128 {
    let mut h = Sha256::new();
    h.update(b"change-row-v2");
    for f in text {
        match f {
            Some(s) => {
                h.update([1u8]);
                put(&mut h, s.as_bytes());
            }
            None => h.update([0u8]),
        }
    }
    // Some(0) and None are different facts about a CR and must not hash alike.
    match revision {
        Some(v) => {
            h.update([1u8]);
            h.update(v.to_le_bytes());
        }
        None => h.update([0u8]),
    }
    let d = h.finalize();
    let mut b = [0u8; 16];
    b.copy_from_slice(&d[..16]);
    u128::from_le_bytes(b)
}

impl Store {
    /// changes_sum reads the whole `changes` table and answers (sum of row hashes,
    /// row count).
    ///
    /// A SCAN, NOT A RECORDED NUMBER. The point of the comparison this feeds is to
    /// look at the corpus, so reading the corpus is the work, not an overhead to be
    /// optimised away with a stamp. On the published corpus it is 256 471 rows; the
    /// rewrite it avoids is 1m54 plus a 22 GiB image layer.
    ///
    /// A DISCARDED SCAN ERROR HERE WOULD BE THE WORST OF BOTH: a failed read that
    /// returned an empty sum would look like an empty table, the overlay would
    /// rewrite the corpus every run, and nothing would say why. The row error is
    /// propagated, as it is in the spec-list read below.
    fn changes_sum(&self) -> Result<(u128, usize)> {
        // `clauses` is a VARCHAR[] and is read through CAST(... AS VARCHAR) rather
        // than as a list: the only thing this comparison needs to know is whether
        // the column is still NULL and, if it is not, what it says. Rendering it as
        // text answers both without teaching this function a list type it would
        // otherwise never touch.
        //
        // THE COLUMN ORDER HERE IS THE ORDER hash_parts EXPECTS, and the two are
        // adjacent for that reason. They are the same nine slots the writer fills.
        let mut st = self.conn.prepare(
            "SELECT spec_id, cr_number, summary, meeting, category,
                    from_version, to_version, tdoc_url, CAST(clauses AS VARCHAR),
                    cr_revision
               FROM changes",
        )?;
        let it = st.query_map([], |r| {
            Ok((
                [
                    r.get::<_, Option<String>>(0)?,
                    r.get::<_, Option<String>>(1)?,
                    r.get::<_, Option<String>>(2)?,
                    r.get::<_, Option<String>>(3)?,
                    r.get::<_, Option<String>>(4)?,
                    r.get::<_, Option<String>>(5)?,
                    r.get::<_, Option<String>>(6)?,
                    r.get::<_, Option<String>>(7)?,
                    r.get::<_, Option<String>>(8)?,
                ],
                r.get::<_, Option<i32>>(9)?,
            ))
        })?;
        let mut sum = 0u128;
        let mut n = 0usize;
        for r in it {
            let (text, revision) = r.context("read the changelog the corpus already holds")?;
            let parts: [Option<&str>; 9] = std::array::from_fn(|i| text[i].as_deref());
            sum = sum.wrapping_add(hash_parts(&parts, revision));
            n += 1;
        }
        Ok((sum, n))
    }

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

        // The filter that used to live inside the insert loop, hoisted so the
        // comparison below is taken over exactly the rows that will be written and
        // nothing else.
        let keep: Vec<&ChangeRow> = rows.iter().filter(|r| held.contains(&r.spec_id)).collect();
        let skipped = rows.len() - keep.len();
        let written = keep.len();

        // THE GUARD READS THE TABLE. IT DOES NOT ASK A LEDGER WHAT THE TABLE SHOULD
        // CONTAIN.
        //
        // The first version of this stamped a digest into schema_meta and compared
        // against that, with a row count beside it as a sanity check. Both halves
        // were claims about the corpus rather than readings of it: a changelog
        // replaced out of band by a DIFFERENT changelog of the SAME SIZE — a
        // restore, a hand-run repair, a half-finished build — matched the recorded
        // digest and the count, and the overlay would have left the wrong records in
        // the served corpus while reporting "corpus untouched". This repository has
        // been bitten by exactly that shape twice: `migrate-paragraphs` cut
        // clause_occ to 5 % with every gate green, and the ETSI half re-ingested 566
        // clauses per build for fifteen builds under a `require-worklist` gate that
        // only ever asked whether something was MISSING.
        //
        // So there is no ledger key any more. The rows the corpus holds are compared
        // against the rows that would be written, and the only thing schema_meta is
        // trusted for is `changes_source`, which is a fact about provenance that the
        // rows themselves do not carry.
        //
        // THE SUM IS ORDER-INDEPENDENT ON PURPOSE. Nothing guarantees the order a
        // scan returns, `compact` rewrites the physical order, and sorting both
        // sides would mean matching DuckDB's string collation to Rust's byte
        // ordering — a second thing to get wrong. Wrapping ADDITION of per-row
        // hashes, not XOR: XOR cancels a duplicated row against itself, and a
        // changelog that holds one record twice is exactly the kind of damage worth
        // noticing.
        let want: u128 = keep
            .iter()
            .fold(0u128, |acc, r| acc.wrapping_add(row_hash(r)));
        let (have, have_n) = self.changes_sum()?;
        if have_n == written && have == want && self.get_meta("changes_source")? == source {
            return Ok((written, skipped, false));
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
