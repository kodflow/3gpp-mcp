//! vectors.rs — the write path `embed-io` owns: the dense ledger import and the
//! sparse postings.
//!
//! WHY IT IS A SEPARATE FILE, and it is not tidiness. The `goal` pipeline tracks
//! provenance PER FILE: a step re-runs when a file it declares changes. `merge`
//! declares the store library because it genuinely links it — and while all of
//! this lived in lib.rs, editing the ledger import invalidated `merge` and cost a
//! 38-minute reconstruction of a corpus the edit could not affect. Measured on
//! build 23 (2026-09-06), which paid it.
//!
//! The split is not a guess about who uses what. Every Store method reached by
//! each binary was listed before moving anything:
//!
//! ```text
//!     merge        build_and_freeze_hnsw changed_buckets checkpoint count_clauses
//!                  count_null_embeddings delete_spec_release enable_fts
//!                  fold_shard_buckets get_meta max_chunk_id restore_stashed_vectors
//!                  set_meta shard_series spec_versions stash_bucket_vectors
//!                  strip_embeddings
//!     compact      clauses_is_view count_clauses enable_fts get_meta raw set_meta
//!     embed-io     the methods below, plus count_clauses/get_meta/set_meta/
//!                  count_null_embeddings/build_and_freeze_hnsw
//! ```
//!
//! Only what NOTHING but embed-io calls moved here. `clauses_is_view` stayed in
//! lib.rs for exactly that reason: compact.rs calls it too. `rust/ingest` calls
//! none of these, so the ingest steps' declarations stay correct as written.

use super::*;
use anyhow::{Context, Result};

impl Store {
    /// clauses_needing_embedding streams the work-list, oldest chunk first, capped at
    /// `limit` (0 = all).
    ///
    /// WHAT "NEEDING" MEANS depends on the identity. Normally it is "has no vector yet".
    /// But `EmbedIdentity` folds in the model, precision, windowing and max_tokens, and a
    /// corpus embedded under one identity must never be queried or indexed under another —
    /// that is the whole reason the identity exists. So when the DB is stamped with a
    /// DIFFERENT identity than the one this run embeds under, every embeddable clause needs
    /// re-embedding, vector or not.
    ///
    /// This used to ask `embedding IS NULL` alone. The #208 switch from truncate to
    /// mean_pool therefore archived the ledger, exported an EMPTY work-list, and reported
    /// "every clause already carries a vector — nothing to do" — a re-embed that silently
    /// did not happen, on a corpus every vector of which was now stale. Same defect class
    /// as validate/check-data/LoadVSS: the gate asked a different question than the thing
    /// it gated.
    ///
    /// `want_identity` empty = ask the old question (callers that have no identity to hand).
    pub fn clauses_needing_embedding(
        &self,
        limit: usize,
        floor_ord: i64,
        want_identity: &str,
    ) -> Result<Vec<WorkItem>> {
        let stamped = self.get_meta("embedding_model").unwrap_or_default();
        let identity_changed =
            !want_identity.is_empty() && !stamped.is_empty() && stamped != want_identity;
        if identity_changed {
            eprintln!(
                "embed work-list: corpus is stamped {stamped}, this run embeds {want_identity} — every embeddable clause is stale and re-enters the work-list"
            );
        }
        let vector_filter = if identity_changed {
            ""
        } else {
            "embedding IS NULL AND "
        };
        // Carry `release` so the floor (release ordinal ≥ floor_ord) is applied in Rust — the
        // Rel-99→3 special makes a pure-SQL ordinal awkward (== Go ClausesNeedingEmbedding
        // FloorOrd). floor_ord ≤ 0 = no floor.
        let sql = format!(
            "SELECT chunk_id, COALESCE(release,''), COALESCE(heading,''), COALESCE(text,'') FROM clauses
             WHERE {vector_filter}{EMBEDDABLE_TEXT_SQL} ORDER BY chunk_id"
        );
        let mut stmt = self.conn.prepare(&sql).context("prepare worklist")?;
        let rows = stmt
            .query_map([], |r| {
                Ok((
                    r.get::<_, u64>(0)?,
                    r.get::<_, String>(1)?,
                    r.get::<_, String>(2)?,
                    r.get::<_, String>(3)?,
                ))
            })
            .context("query worklist")?;
        let mut out = Vec::new();
        for r in rows {
            let (chunk_id, release, heading, text) = r.context("scan worklist row")?;
            if floor_ord > 0
                && crate::identity::release_ordinal(&release).is_none_or(|o| o < floor_ord)
            {
                continue; // below the embed floor → leave lexical-only
            }
            out.push(WorkItem {
                chunk_id,
                heading,
                text,
            });
            if limit > 0 && out.len() >= limit {
                break;
            }
        }
        Ok(out)
    }

    /// clauses_needing_sparse streams the SPARSE work-list: embeddable clauses with no
    /// clause_sparse posting yet, oldest chunk first, capped at `limit` (0 = all). Mirrors
    /// clauses_needing_embedding but keys on the sparse table instead of `embedding`, so a
    /// `--sparse-only` pass is resumable and additive (== Go embed --sparse-only worklist).
    pub fn clauses_needing_sparse(&self, limit: usize, floor_ord: i64) -> Result<Vec<WorkItem>> {
        let sql = format!(
            "SELECT chunk_id, COALESCE(release,''), COALESCE(heading,''), COALESCE(text,'') FROM clauses
             WHERE chunk_id NOT IN (SELECT chunk_id FROM clause_sparse) AND {EMBEDDABLE_TEXT_SQL} ORDER BY chunk_id"
        );
        let mut stmt = self.conn.prepare(&sql).context("prepare sparse worklist")?;
        let rows = stmt
            .query_map([], |r| {
                Ok((
                    r.get::<_, u64>(0)?,
                    r.get::<_, String>(1)?,
                    r.get::<_, String>(2)?,
                    r.get::<_, String>(3)?,
                ))
            })
            .context("query sparse worklist")?;
        let mut out = Vec::new();
        for r in rows {
            let (chunk_id, release, heading, text) = r.context("scan sparse worklist row")?;
            if floor_ord > 0
                && crate::identity::release_ordinal(&release).is_none_or(|o| o < floor_ord)
            {
                continue;
            }
            out.push(WorkItem {
                chunk_id,
                heading,
                text,
            });
            if limit > 0 && out.len() >= limit {
                break;
            }
        }
        Ok(out)
    }

    /// check_body_ledger_agreement guards the collapse the body-level write performs.
    ///
    /// Many occurrences share one body, and they share it precisely because their text is
    /// identical — so they carry the same vector and the same hash, and folding them onto
    /// one row is sound. That is an invariant, not a hope. A body whose occurrences
    /// disagree means the ledger and the corpus describe different text, and the write
    /// would pick one of them at random; refuse instead.
    fn check_body_ledger_agreement(&self) -> Result<()> {
        let conflicts: i64 = self
            .conn
            .query_row(
                "SELECT count(*) FROM (
                   SELECT o.body_id FROM _ledger l JOIN clause_occ o USING (chunk_id)
                    GROUP BY o.body_id HAVING count(DISTINCT l.embedding_hash) > 1)",
                [],
                |r| r.get(0),
            )
            .context("check that occurrences of a body agree on their vector")?;
        if conflicts > 0 {
            anyhow::bail!(
                "{conflicts} body/bodies whose occurrences carry different embedding hashes — \
                 the ledger and the corpus disagree about their text; refusing to write one at random"
            );
        }
        Ok(())
    }

    /// check_ledger_ids_unambiguous refuses a ledger that holds MORE THAN ONE content
    /// hash for the same chunk_id.
    ///
    /// The ledger is append-only and the apply below joins it to the corpus on chunk_id
    /// alone, so two rows for one id make the UPDATE pick one of them arbitrarily —
    /// and a chunk_id is a POSITION in one build, not an identity: it is assigned
    /// sequentially at ingest (offset = max_chunk_id), so a corpus rebuilt from scratch
    /// reuses the same numbers for different clauses.
    ///
    /// Measured 2026-09-02 between two ETSI builds of the SAME 11 821 documents:
    /// chunk_id 138 was "ETSI TS 101 671 v3.15.1 §10" in one and
    /// "ETSI EN 300 113-1 v1.3.1 §4" in the other. Importing across that would have
    /// given 1 262 127 clauses a vector computed from an unrelated clause, with every
    /// gate green — null_at_floor 0, clauses_with_text equal to vectors, the identities
    /// agreeing, the HNSW index building. Only the answers would have been wrong.
    ///
    /// Refusing is the right answer rather than "keep the last one": the ledger has no
    /// dependable row order once read back, and guessing which of two vectors belongs to
    /// a clause is exactly the decision that must not be made silently. The remedy is to
    /// archive the ledger and re-derive — its (hash, vec) pairs are still reusable
    /// through --resume-from, which costs no GPU.
    fn check_ledger_ids_unambiguous(&self) -> Result<()> {
        let (ids, worst): (i64, i64) = self
            .conn
            .query_row(
                "SELECT coalesce(count(*), 0), coalesce(max(n), 0) FROM (
                   SELECT chunk_id, count(DISTINCT embedding_hash) AS n
                     FROM _ledger GROUP BY chunk_id HAVING n > 1)",
                [],
                |r| Ok((r.get(0)?, r.get(1)?)),
            )
            .context("check that the ledger names each chunk_id once")?;
        if ids > 0 {
            anyhow::bail!(
                "the ledger holds {ids} chunk_id(s) with more than one content hash (worst: {worst}). chunk_ids are positional, so this ledger describes more than one build of the corpus and the import would attach vectors at random. Archive it and re-run the embed step: pass the archive as --resume-from so its vectors are reused without GPU."
            );
        }
        Ok(())
    }

    /// import_ledger applies EVERY row of the ledger. Self-repairing, and priced
    /// accordingly — see `import_ledger_changed_only` for when that price is not
    /// worth paying.
    ///
    /// Returns (rows staged from the ledger, rows the UPDATE actually touched).
    pub fn import_ledger(&self, path: &str) -> Result<(i64, i64)> {
        self.import_ledger_inner(path, false)
    }

    /// import_ledger_changed_only applies just the rows whose vector the corpus
    /// does not already carry.
    ///
    /// WHAT THIS GIVES UP, because it is not free. The full import is
    /// SELF-REPAIRING: it rewrites every vector, so a body whose embedding was
    /// corrupted while its `embedding_hash` stayed correct is silently fixed on
    /// the next build. That property is not theoretical — it is what recovered
    /// this corpus after a killed import on 2026-09-06. Filtering on the hash
    /// cannot see that case, by construction.
    ///
    /// What it buys, measured the same day: the ETSI half had 368 new vectors and
    /// re-applied 1 999 814, re-reading a 25.6 GB ledger for over 32 minutes to
    /// end with `ledger grew from 1999814 to 1999814`. The cost tracked the size
    /// of the ledger, never the size of the work.
    ///
    /// So both stay, and the choice is explicit at the call site rather than
    /// implied by a default. The guards are shared: ambiguous chunk_ids and a
    /// body/ledger disagreement are refused on BOTH paths, because the fast path
    /// is the one that will actually run.
    pub fn import_ledger_changed_only(&self, path: &str) -> Result<(i64, i64)> {
        self.import_ledger_inner(path, true)
    }

    fn import_ledger_inner(&self, path: &str, only_changed: bool) -> Result<(i64, i64)> {
        let p = path.replace('\\', "/").replace('\'', "''");
        self.conn
            .execute_batch(&format!(
                "BEGIN;
                 CREATE OR REPLACE TEMP TABLE _ledger AS
                   SELECT chunk_id, hash AS embedding_hash, vec
                   FROM read_json('{p}',
                                  format='newline_delimited',
                                  ignore_errors=true,
                                  columns={{chunk_id:'UBIGINT', hash:'VARCHAR', vec:'FLOAT[]'}})
                   WHERE vec IS NOT NULL AND len(vec) = {DENSE_DIM};"
            ))
            .with_context(|| format!("read ledger {path}"))?;

        let staged: i64 = self
            .conn
            .query_row("SELECT count(*) FROM _ledger", [], |r| r.get(0))
            .context("count staged ledger rows")?;

        // WHERE THE VECTORS ACTUALLY LIVE. On a content-addressed corpus (ADR 0004)
        // `clauses` is a view and DuckDB refuses to UPDATE it, which is why the embed step
        // used to run `migrate-paragraphs --restore` first — rematerialising every clause's
        // text and taking the corpus from 11.5 GB to 38.8 GB before a single vector was
        // computed, then needing a full re-compaction to give the space back. Writing to
        // `bodies` costs none of that and touches 821 146 rows instead of 2 752 688.
        //
        // The cast to the FIXED width happens only here: staging is FLOAT[] (variable),
        // the column is FLOAT[1024], and the filter above has already guaranteed every
        // surviving row is exactly that wide.
        self.check_ledger_ids_unambiguous()?;
        let to_bodies = self.clauses_is_view()?;
        if to_bodies {
            self.check_body_ledger_agreement()?;
        }
        // THE FILTER, and why it reads like this. `IS DISTINCT FROM` and not `<>`:
        // a row that has never been embedded holds NULL in both columns, and `<>`
        // would skip exactly the rows that need the vector most. The NULL check on
        // `embedding` is not redundant with the hash check either — a corpus can
        // carry a hash from a previous model with no vector behind it.
        let bodies_filter = if only_changed {
            "AND (bodies.embedding IS NULL
                  OR bodies.embedding_hash IS DISTINCT FROM d.embedding_hash)"
        } else {
            ""
        };
        let clauses_filter = if only_changed {
            "AND (clauses.embedding IS NULL
                  OR clauses.embedding_hash IS DISTINCT FROM l.embedding_hash)"
        } else {
            ""
        };
        let apply = if to_bodies {
            format!(
                "UPDATE bodies SET embedding = d.vec::FLOAT[{DENSE_DIM}],
                                   embedding_hash = d.embedding_hash
                   FROM (SELECT o.body_id,
                                any_value(l.vec) AS vec,
                                any_value(l.embedding_hash) AS embedding_hash
                           FROM _ledger l JOIN clause_occ o USING (chunk_id)
                          GROUP BY o.body_id) AS d
                  WHERE bodies.body_id = d.body_id {bodies_filter};"
            )
        } else {
            format!(
                "UPDATE clauses SET embedding = l.vec::FLOAT[{DENSE_DIM}],
                                    embedding_hash = l.embedding_hash
                   FROM _ledger AS l WHERE clauses.chunk_id = l.chunk_id {clauses_filter};"
            )
        };
        // execute, not execute_batch, so the UPDATE reports how many rows it ACTUALLY
        // touched. The caller used to be handed `staged` twice over and print it as
        // "wrote N vector(s)" — which was true of the full import and a lie about the
        // incremental one, where the whole point is that most staged rows are skipped.
        // A log that overstates its work by three orders of magnitude is worse than
        // no log: it is the number someone will quote back when the corpus is wrong.
        let applied = self.conn.execute(&apply, []).with_context(|| {
            if to_bodies {
                "apply ledger to bodies"
            } else {
                "apply ledger to clauses"
            }
        })? as i64;
        self.conn
            .execute_batch("DROP TABLE _ledger; COMMIT;")
            .context("close the ledger import")?;
        Ok((staged, applied))
    }

    /// set_sparse_many writes MANY clauses' postings in ONE transaction.
    ///
    /// set_sparse above is correct and, at corpus scale, unusable: it opens its own
    /// BEGIN/COMMIT per clause and formats one INSERT statement per term. Importing
    /// the 3GPP sparse layer that way is 2.2 million transactions and ~110 million
    /// individually-parsed statements, and it was measured at over seven hours —
    /// with no progress output, so it looks like a hang for most of them.
    ///
    /// This batches both dimensions: one transaction per call, one DELETE naming
    /// every chunk_id in the batch, and ONE multi-row INSERT for all their terms.
    /// The per-statement parse cost collapses from 110 million to a few thousand.
    ///
    /// Semantics are unchanged, including the part that is easy to lose: a clause
    /// with NO terms still gets its DELETE, so re-importing genuinely clears a
    /// previous posting set rather than leaving a stale one behind. That is what
    /// makes the import idempotent and the work list converge.
    /// drop_sparse_term_index / create_sparse_term_index bracket a bulk import.
    ///
    /// `clause_sparse` carries a secondary index on term_id (the one the sparse arm
    /// scores through), and DuckDB maintains it row by row. Importing the 3GPP layer
    /// inserts ~265 million rows — 2.2 million clauses at ~120 postings each — and
    /// every one of them updates an ART index that is itself growing, so the import
    /// gets slower the further it goes. That is the shape observed on the real run:
    /// brisk for the first hours, then hours more with the file barely growing while
    /// the process stayed CPU-bound.
    ///
    /// Dropping the index for the load and rebuilding it once afterwards is the
    /// standard remedy, and it is safe here because the import owns the table for
    /// the duration: nothing reads clause_sparse until the corpus is served.
    /// Rebuilding is NOT optional — `SearchSparse` scores by term_id, and leaving it
    /// off would turn every sparse query into a full scan of a 265-million-row table
    /// without any error to say so.
    /// sparse_chunk_ids returns every chunk_id that ALREADY carries postings.
    ///
    /// This is the set `clauses_needing_sparse` subtracts, spelled the same way
    /// (`chunk_id NOT IN (SELECT chunk_id FROM clause_sparse)`), so the importer
    /// can skip exactly the clauses the work list already decided were done. No
    /// new notion of "unchanged" is introduced: the work list and the import now
    /// answer to one definition instead of two.
    ///
    /// It is materialised in memory rather than joined in SQL because the ledger
    /// is a JSONL stream decoded line by line — 2 M u64 is ~48 MB, against the
    /// 49 MINUTES the alternative costs (measured below).
    pub fn sparse_chunk_ids(&self) -> Result<std::collections::HashSet<u64>> {
        let mut stmt = self
            .conn
            .prepare("SELECT DISTINCT chunk_id FROM clause_sparse")
            .context("prepare sparse chunk_id set")?;
        let rows = stmt
            .query_map([], |r| r.get::<_, u64>(0))
            .context("query sparse chunk_id set")?;
        let mut out = std::collections::HashSet::new();
        for r in rows {
            out.insert(r.context("scan sparse chunk_id")?);
        }
        Ok(out)
    }

    pub fn drop_sparse_term_index(&self) -> Result<()> {
        self.conn
            .execute_batch("DROP INDEX IF EXISTS clause_sparse_term;")
            .context("drop clause_sparse_term")?;
        Ok(())
    }

    /// create_sparse_term_index rebuilds what drop_sparse_term_index removed.
    pub fn create_sparse_term_index(&self) -> Result<()> {
        self.conn
            .execute_batch(
                "CREATE INDEX IF NOT EXISTS clause_sparse_term ON clause_sparse (term_id);",
            )
            .context("create clause_sparse_term")?;
        Ok(())
    }

    pub fn set_sparse_many(&self, batch: &[(u64, Vec<(u32, f32)>)]) -> Result<()> {
        if batch.is_empty() {
            return Ok(());
        }
        let mut sql = String::with_capacity(64 * 1024);
        sql.push_str("BEGIN; DELETE FROM clause_sparse WHERE chunk_id IN (");
        for (i, (chunk_id, _)) in batch.iter().enumerate() {
            if i > 0 {
                sql.push(',');
            }
            sql.push_str(&chunk_id.to_string());
        }
        sql.push_str(");");

        let mut rows = 0usize;
        for (chunk_id, terms) in batch {
            for (term_id, weight) in terms {
                if rows == 0 {
                    sql.push_str("INSERT INTO clause_sparse(chunk_id, term_id, weight) VALUES ");
                } else {
                    sql.push(',');
                }
                sql.push_str(&format!("({chunk_id},{term_id},{weight})"));
                rows += 1;
            }
        }
        if rows > 0 {
            sql.push(';');
        }
        sql.push_str("COMMIT;");
        self.conn.execute_batch(&sql).context("set_sparse_many")?;
        Ok(())
    }
}
