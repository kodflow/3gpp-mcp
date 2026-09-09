// Command repair-reingest removes the occurrences left behind by a deliverable
// that was written by more than one ingest.
//
// WHAT IT REPAIRS. The ETSI resume check read files as strict UTF-8 while the
// ingest read them with a windows-1252 fallback, so the two files in 11 822 that
// are not UTF-8 were ingested again on every build — fifteen times each by
// 2026-09-09, 566 clauses per build. The 3GPP half carries the same shape from
// older runs, frozen: 8 deliverables, 770 excess occurrences.
//
// WHY IT IS SAFE, and it is worth being precise because chunk_id is a POSITION.
// The served corpus keeps text and vectors in `bodies`/`paragraphs`, deduplicated
// by (heading, text), and `clause_occ` holds one row per OCCURRENCE pointing at a
// body. Fifteen copies of a document therefore share one set of bodies: the
// damage is entirely in clause_occ (and clause_sparse, keyed by chunk_id). This
// deletes occurrences only. No body, no paragraph, no embedding is touched, and
// the HNSW index lives on bodies.
//
// HOW IT DECIDES WHAT TO KEEP. Not "one row per distinct clause" — a document may
// legitimately repeat a clause, and every copy repeats it, so deduplicating that
// way would destroy real content. Each ingest appended a contiguous block of
// chunk_ids (the writer offsets by max_chunk_id), so the FIRST block is the
// lowest N/k chunk_ids, where k is the multiplicity. It keeps exactly those and
// deletes the rest, which preserves the document's internal repeats untouched.
//
// It REFUSES rather than guesses: if a group's occurrence count is not an exact
// multiple of its multiplicity, the block model does not hold there and the group
// is reported and skipped.
//
//	repair-reingest --db data/etsi.duckdb           # dry run, reports
//	repair-reingest --db data/etsi.duckdb --apply   # writes
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"

	"github.com/kodflow/3gpp-mcp/internal/store"
)

func main() {
	db := flag.String("db", "", "corpus DuckDB to repair (required)")
	apply := flag.Bool("apply", false, "actually delete; without it this only reports")
	flag.Parse()
	if *db == "" {
		fmt.Fprintln(os.Stderr, "repair-reingest: --db is required")
		os.Exit(2)
	}
	if err := run(*db, *apply); err != nil {
		fmt.Fprintln(os.Stderr, "repair-reingest:", err)
		os.Exit(1)
	}
}

func run(dbPath string, apply bool) error {
	st, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", dbPath, err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	groups, err := st.ReingestedOccurrences(ctx)
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		fmt.Println("repair-reingest: nothing to repair — no deliverable is stored more than once")
		return nil
	}

	var totalDel, skipped int
	for _, g := range groups {
		keep := g.Held / g.Copies
		if g.Held%g.Copies != 0 {
			fmt.Printf("  SKIP %s %s v%s: %d rows is not a multiple of %d copies — the block model does not hold here\n",
				g.SpecID, g.Release, g.Version, g.Held, g.Copies)
			skipped++
			continue
		}
		del := g.Held - keep
		totalDel += del
		fmt.Printf("  %s %s v%s: %d copies, %d rows → keep %d, delete %d\n",
			g.SpecID, g.Release, g.Version, g.Copies, g.Held, keep, del)
		if !apply {
			continue
		}
		if err := repairOne(ctx, st.DB(), g, keep); err != nil {
			return fmt.Errorf("repair %s %s v%s: %w", g.SpecID, g.Release, g.Version, err)
		}
	}
	verb := "would delete"
	if apply {
		verb = "deleted"
	}
	fmt.Printf("repair-reingest: %d group(s), %s %d occurrence(s), %d skipped\n",
		len(groups), verb, totalDel, skipped)
	if !apply {
		fmt.Println("repair-reingest: DRY RUN — pass --apply to write")
	}
	return nil
}

// repairOne deletes everything past the first block, sparse postings first so no
// posting is ever left pointing at an occurrence that is gone.
func repairOne(ctx context.Context, db *sql.DB, g store.Reingested, keep int) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	const doomed = `
		SELECT chunk_id FROM (
			SELECT chunk_id, row_number() OVER (ORDER BY chunk_id) AS rn
			FROM clause_occ WHERE spec_id = ? AND release = ? AND version = ?
		) WHERE rn > ?`

	// clause_sparse may be absent on a corpus with no sparse layer; a missing
	// table is not a failure to repair the occurrences.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM clause_sparse WHERE chunk_id IN (`+doomed+`)`,
		g.SpecID, g.Release, g.Version, keep); err != nil {
		fmt.Printf("    (no sparse postings removed: %v)\n", err)
	}
	res, err := tx.ExecContext(ctx,
		`DELETE FROM clause_occ WHERE chunk_id IN (`+doomed+`)`,
		g.SpecID, g.Release, g.Version, keep)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if want := int64(g.Held - keep); n != want {
		return fmt.Errorf("deleted %d occurrence(s), expected %d — refusing to commit", n, want)
	}
	return tx.Commit()
}
