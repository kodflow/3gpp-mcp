//! ingest-crs — the additive 3GPP change-request overlay: read the published CR
//! database and give the `changes` table a writer for the first time since the
//! write side moved to Rust.
//!
//! The table has been a fossil since Phase 11b (c635038) deleted the Go
//! HTML-ingest write side without the Rust ingest reimplementing the change-history
//! parser. Measured on the corpus published 2026-09-09: 61 321 rows over 3 452
//! specs, of which only 311 named anything citable and 3 026 were the literal
//! string "Date" — the change-history table's column header, read positionally as a
//! body row. PR #311 stopped `get_changelog` serving that header. This gives the
//! table a source.
//!
//! Why the CR database rather than the change-history table printed in each spec:
//! see the module docs of parse3gpp::crdb. Short version — the printed table is a
//! RENDERING of this database, the database is one 57 MB download covering every
//! spec, and parsing the documents instead would cover only the third of the
//! archive that happens to be on disk after a delta fetch.
//!
//! Usage: ingest-crs --db <db> --crdb <CRDB_*.zip|.xlsx> [--dry-run]

use anyhow::{Context, Result};
use clap::Parser;
use parse3gpp::crdb::for_each_cr;
use store_rs::{ChangeRow, Store};

#[derive(Parser)]
#[command(
    name = "ingest-crs",
    about = "Rewrite the changes table from the 3GPP Change Request database"
)]
struct Args {
    #[arg(long)]
    db: String,
    /// The published CRDB_<date>.zip, or the .xlsx inside it.
    #[arg(long)]
    crdb: String,
    /// Parse and report without writing. Used to measure a new export before it is
    /// allowed near the corpus.
    #[arg(long, default_value_t = false)]
    dry_run: bool,
}

fn main() -> Result<()> {
    let args = Args::parse();

    let f = std::fs::File::open(&args.crdb).with_context(|| format!("open {}", args.crdb))?;

    // Collected rather than streamed into the database, and the reason is the
    // transaction. replace_changes DELETEs before it INSERTs, so it must not begin
    // until the whole file has been read: a parse error a third of the way in would
    // otherwise roll back to a corpus whose changelog had already been emptied, and
    // "the export was truncated" would present as "the corpus lost its changelog".
    // ~600 000 rows of small strings is tens of megabytes — the sheet they come
    // from is 480 MB, and THAT is what stays streamed.
    let mut rows: Vec<ChangeRow> = Vec::new();
    let mut not_landed = 0usize;
    let read = for_each_cr(f, |c| {
        // ONLY CHANGES THAT ACTUALLY HAPPENED, and the predicate is measured
        // rather than assumed. Tallied over the 596 696 records of CRDB_20260715:
        //
        //   TSG-level status        total    names a real Version-new
        //   approved              269 238                     265 207
        //   (empty, never reached TSG)
        //                         313 039                         106
        //   reissued                3 102                       1 197
        //   revised                 6 519                          41
        //   withdrawn               2 162                          90
        //   rejected / postponed / not pursued / noted / merged / endorsed
        //                           2 636                          26
        //
        // A CR database is a record of PROPOSALS and their fate. More than half of
        // it — 313 039 rows — is CRs that never reached TSG level, and their
        // Version-new is the literal placeholder "..". Writing those would have put
        // 322 407 proposals into `changes` dressed as changes: 55.6% of the table,
        // every one of them answering "what changed in this spec" with something
        // that never changed it. That is the same failure as the header rows this
        // work removes, and the same shape as the ETSI re-ingest leak — a corpus
        // can be wrong by containing TOO MUCH, and no count-based gate notices.
        //
        // So both halves are required, and they are not redundant: `approved`
        // without a version is a CR whose target was not yet allocated when the
        // export was taken (4 031 rows), and a version without `approved` is a
        // status the database itself contradicts (1 454 rows, mostly `reissued`).
        let landed = c.to_version.starts_with(|d: char| d.is_ascii_digit());
        if c.tsg_status != "approved" || !landed {
            not_landed += 1;
            return;
        }
        rows.push(ChangeRow {
            spec_id: c.spec_id,
            cr_number: c.cr_number,
            cr_revision: c.cr_revision,
            summary: c.summary,
            meeting: c.meeting,
            category: c.category,
            from_version: c.from_version,
            to_version: c.to_version,
            tdoc: c.tdoc,
        });
    })
    .with_context(|| format!("read {}", args.crdb))?;

    if args.dry_run {
        let specs: std::collections::HashSet<&str> =
            rows.iter().map(|r| r.spec_id.as_str()).collect();
        eprintln!(
            "ingest-crs (dry run): {read} record(s) read, {} landed over {} spec(s), {not_landed} dropped as not landed",
            rows.len(),
            specs.len()
        );
        return Ok(());
    }

    // WHICH EXPORT THIS IS, recorded where the read side can reach it and written
    // in the SAME transaction as the rows.
    //
    // The CR database is re-exported every few months, so "the changelog stops at
    // 19.4.0" has two causes that look identical from the outside: no CR has been
    // raised since, or the export predates the ones that were. The stamp is what
    // lets get_changelog tell a reader which. Stamping after the commit would make
    // it a second failure point — the rows would land under the previous export's
    // name, and the note would then be precisely and confidently wrong.
    let stamp = std::path::Path::new(&args.crdb)
        .file_stem()
        .map(|s| s.to_string_lossy().to_string())
        .unwrap_or_default();

    let store = Store::open_rw(&args.db)?;
    let (written, skipped, changed) = store.replace_changes(&rows, &stamp)?;
    if !changed {
        // NO CHECKPOINT ON THIS PATH, and that is the point rather than a saving. A
        // checkpoint is a WRITE; reaching here means the corpus must not move, so
        // the one thing this branch must not do is touch the file to say it did
        // nothing. It is the line `ingest-openapi` and `ingest-li` already print,
        // and now it means the same thing on disk that it means in the table.
        eprintln!(
            "ingest-crs: {read} record(s) read — the changelog already carries these {written} row(s) from {stamp}, corpus untouched"
        );
        return Ok(());
    }
    store.checkpoint()?;
    eprintln!(
        "ingest-crs: {read} record(s) read, {written} written, {skipped} skipped (spec not in this corpus), {not_landed} dropped as not landed"
    );
    Ok(())
}
