//! compact — rewrite a corpus without its dead space.
//!
//! DuckDB never gives free blocks back to the filesystem. A CHECKPOINT reclaims
//! them for REUSE INSIDE the file, which is a different thing, and the
//! difference is not small: measured on the 2026-08-30 corpus,
//!
//!     total_blocks 229166   used_blocks 46947   free_blocks 182219
//!
//! — 12.3 GB of data in a 55.9 GB file, and `DROP TABLE` + `CHECKPOINT` moved
//! it by exactly zero bytes. Publishing that file ships 43.6 GB of nothing.
//!
//! `COPY FROM DATABASE` rebuilds the storage instead of cloning it, and
//! `Store::copy_database_compact` already wraps it with the two lessons that
//! cost the most to learn: VSS loaded first, or the source's HNSW catalogue
//! cannot even be bound; and a temp directory on the DESTINATION's own volume
//! before any memory cap, or an in-memory connection with nowhere to spill dies
//! after seventy minutes having written nothing. This bin is a front for that
//! function, not a second implementation of it — the absence of the front is
//! what left `merge --base` (which needs a shard, and replaces its buckets) as
//! the only way to reach a pure compaction.
//!
//! CUSTOM INDEXES ARE NOT COPIED. That is not a limitation to work around, it
//! is part of why the copy is compact. It does mean the vector index has to be
//! rebuilt afterwards, so this bin clears `hnsw_state` rather than letting the
//! copied row keep saying "frozen" about an index that was left behind. That
//! exact mismatch — a flag that travels while the thing it describes does not —
//! is how a corpus once shipped serving exact scan with every gate green.
//!
//! The original is kept as `<db>.pre-compact`: a corpus is not something to
//! delete on the strength of a copy that was verified seconds ago.
use anyhow::{bail, Context, Result};
use clap::Parser;
use std::fs;
use std::path::PathBuf;
use store_rs::Store;

#[derive(Parser)]
#[command(
    name = "compact",
    about = "Rewrite a DuckDB corpus without dead space (COPY FROM DATABASE)"
)]
struct Args {
    /// Corpus to compact, in place. The original is kept as <db>.pre-compact.
    #[arg(long)]
    db: String,

    /// Compact to this path and leave the source untouched.
    #[arg(long)]
    out: Option<String>,
}

fn gib(p: &str) -> f64 {
    fs::metadata(p)
        .map(|m| m.len() as f64 / (1024.0 * 1024.0 * 1024.0))
        .unwrap_or(0.0)
}

/// fingerprint is what the copy has to reproduce to be believed: the number of
/// clauses the corpus can serve, and the identity its vectors were made under.
/// Cheap enough to run twice, specific enough that a truncated copy cannot pass.
fn fingerprint(path: &str) -> Result<(i64, String)> {
    let st = Store::open_rw(path).with_context(|| format!("open {path}"))?;
    let clauses = st.count_clauses().context("count clauses")?;
    let model = st.get_meta("embedding_model").unwrap_or_default();
    Ok((clauses, model))
}

/// views reads back the CREATE statement of every non-internal view.
///
/// COPY FROM DATABASE does not carry a view across as a view: it lands as an
/// EMPTY BASE TABLE of the same name. On a content-addressed corpus `clauses` is
/// the compatibility view over clause_occ ⋈ bodies ⋈ body_seq ⋈ paragraphs, so
/// the copy arrives holding all 2 752 688 occurrences and answering 0 for every
/// query that goes through the name the whole read side uses. Measured
/// 2026-08-30. The definitions come from the source itself rather than from a
/// constant here, so this cannot drift away from what the corpus actually had.
fn views(path: &str) -> Result<Vec<(String, String)>> {
    let st = Store::open_rw(path).with_context(|| format!("open {path}"))?;
    let mut stmt = st
        .raw()
        .prepare("SELECT view_name, sql FROM duckdb_views() WHERE internal = false")
        .context("prepare duckdb_views")?;
    let rows = stmt
        .query_map([], |r| Ok((r.get::<_, String>(0)?, r.get::<_, String>(1)?)))
        .context("query duckdb_views")?;
    let mut out = Vec::new();
    for r in rows {
        out.push(r.context("read view row")?);
    }
    Ok(out)
}

fn main() -> Result<()> {
    let args = Args::parse();
    let src = args.db.clone();
    let in_place = args.out.is_none();
    let dst = args.out.clone().unwrap_or_else(|| format!("{src}.compact"));

    let before_bytes = gib(&src);
    let (clauses, model) = fingerprint(&src)?;
    if clauses == 0 {
        bail!("{src} reports 0 clauses — refusing to compact a corpus that has nothing in it");
    }
    let defs = views(&src)?;
    eprintln!(
        "compact: {src} is {before_bytes:.1} GiB, {clauses} clause(s), model {model:?}, {} view(s)",
        defs.len()
    );

    if fs::metadata(&dst).is_ok() {
        bail!("{dst} already exists — refusing to overwrite it");
    }
    Store::copy_database_compact(&src, &dst).context("compact copy")?;

    // RESTORE WHAT THE COPY FLATTENS, BEFORE ASKING IT ANYTHING.
    //
    // Each view arrived as an empty base table of the same name, and the FTS
    // index did not arrive at all (it lives in its own schema, which the copy
    // does not carry). Both are part of being the same corpus, so both are
    // rebuilt here rather than left as a surprise for whoever serves the file.
    {
        let st = Store::open_rw(&dst).context("reopen copy")?;
        for (name, sql) in &defs {
            st.raw()
                .execute_batch(&format!("DROP TABLE IF EXISTS {name};"))
                .with_context(|| format!("drop flattened view {name}"))?;
            st.raw()
                .execute_batch(sql)
                .with_context(|| format!("recreate view {name}"))?;
            eprintln!("compact: view {name} restored");
        }
        // THE FTS INDEX FOLLOWS THE SHAPE, LIKE THE VECTOR INDEX DOES.
        //
        // enable_fts() indexes `clauses`, which on a content-addressed corpus is a
        // view — DuckDB answers "clauses is not an table" and stops. The path that
        // actually scores such a corpus (searchClausesCA) uses fts_main_paragraphs,
        // over the real `paragraphs` table, so that is what gets rebuilt here.
        //
        // Nothing in the repository built fts_main_paragraphs before this: the
        // corpus carried one, the read side used it, and a from-scratch rebuild
        // would have produced a corpus that silently scored with the LIKE fallback.
        if st.clauses_is_view().context("read corpus shape")? {
            st.raw()
                .execute_batch(
                    "INSTALL fts; LOAD fts;
                     PRAGMA create_fts_index('paragraphs', 'para_id', 'part', overwrite=1);",
                )
                .context("rebuild fts_main_paragraphs")?;
            eprintln!("compact: FTS rebuilt over paragraphs (content-addressed corpus)");
        } else {
            st.enable_fts().context("rebuild fts_main_clauses")?;
            eprintln!("compact: FTS rebuilt over clauses");
        }
    }

    // Believe the copy only after it answers the same questions the source did.
    let (got_clauses, got_model) = fingerprint(&dst)?;
    if got_clauses != clauses {
        bail!("the copy has {got_clauses} clause(s), the source had {clauses} — leaving both files in place");
    }
    if got_model != model {
        bail!("the copy is stamped {got_model:?}, the source was {model:?} — leaving both files in place");
    }
    {
        // The index did not come across, so nothing downstream may read "frozen"
        // off this file. "building" is the state serve already treats as unusable.
        let st = Store::open_rw(&dst).context("reopen copy")?;
        st.set_meta("hnsw_state", "building")
            .context("clear hnsw_state")?;
    }
    eprintln!(
        "compact: {dst} is {:.1} GiB — {clauses} clause(s) verified, hnsw_state cleared to \"building\"",
        gib(&dst)
    );

    if !in_place {
        eprintln!("compact: rebuild the vector index on {dst} before serving it");
        return Ok(());
    }

    let kept = format!("{src}.pre-compact");
    if fs::metadata(&kept).is_ok() {
        bail!("{kept} already exists — move it aside before compacting in place");
    }
    fs::rename(&src, &kept).with_context(|| format!("move {src} aside"))?;
    if let Err(e) = fs::rename(&dst, &src) {
        // Put the original back rather than leaving the caller with no corpus at all.
        let _ = fs::rename(&kept, &src);
        return Err(e).with_context(|| format!("move {dst} into place (original restored)"));
    }
    eprintln!(
        "compact: {src} is now {:.1} GiB (was {before_bytes:.1} GiB); original kept at {kept}",
        gib(&src)
    );
    eprintln!("compact: REBUILD THE VECTOR INDEX (freeze-hnsw) — the copy has none");
    eprintln!(
        "compact: once served and verified, remove {}",
        PathBuf::from(&kept).display()
    );
    Ok(())
}
