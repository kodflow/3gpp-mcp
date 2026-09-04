// Command dbcount prints corpus-coverage counters of a DuckDB snapshot as
// machine-readable KEY=VALUE lines, for the CI pre-publish guard (PR-1).
//
//	dbcount --db base.duckdb
//	  spec_versions=3128
//	  api_operations=914
//
// The guard reads these for the about-to-publish DB and the current `latest`
// base, and refuses to clobber `latest` when either counter DECREASES on a
// delta run (deterministic, fail-closed — no "significantly smaller" heuristic).
// A full rebuild or an explicit ALLOW_*_SHRINK override may legitimately shrink.
//
// Read-only: it opens the DB, counts two tables, and exits. A missing table
// (legacy base predating api_operations) counts as 0, which the guard treats as
// "no API rows to lose" — never a false shrink alarm.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/kodflow/3gpp-mcp/internal/store"
)

var Version = "dev"

func main() {
	db := flag.String("db", "", "DuckDB path to count (required)")
	blocks := flag.Bool("blocks", false,
		"print only the physical block accounting, skipping the coverage counts")
	flag.Parse()
	if *db == "" {
		fmt.Fprintln(os.Stderr, "dbcount: --db is required")
		os.Exit(2)
	}
	if err := run(context.Background(), *db, *blocks); err != nil {
		fmt.Fprintln(os.Stderr, "dbcount:", err)
		os.Exit(1)
	}
}

// reportBlocks prints DuckDB's own accounting of the file: how many blocks it
// holds, how many carry data, and how many are free.
//
// THE FREE ONES ARE THE ONLY REASON TO COMPACT. DuckDB never returns them to the
// filesystem — a CHECKPOINT re-offers them for reuse INSIDE the file, which is a
// different thing — so `COPY FROM DATABASE` is the only operation that shrinks
// the corpus, and it is worth exactly the free blocks it drops. Nothing in the
// file size says how many those are: a corpus that has never freed a block and
// one that is four fifths dead space are the same number of bytes on disk right
// up until the rewrite.
//
// That is why this lives here rather than in the compactor, and the reason is
// provenance, not taste. `ingest` names the whole of `rust/store/src` in its
// Impl, so adding a flag to rust/store/src/bin/compact.rs schedules a heavy
// re-ingest of the corpus — measured on 2026-09-04, `make plan` went from
// "0 certain to run" to a re-ingest and the merge, embed, paragraphs and sparse
// chain behind it. A read-only query has no business costing that, and dbcount
// is already the bin `compact` runs to validate itself.
//
// The row is picked by largest total_blocks: pragma_database_size() reports one
// row per attached database, and only the corpus itself has blocks worth counting.
func reportBlocks(ctx context.Context, s *store.Store) error {
	var blockSize, total, used, free int64
	if err := s.QueryRowContext(ctx,
		`SELECT block_size, total_blocks, used_blocks, free_blocks
		   FROM pragma_database_size()
		  WHERE total_blocks IS NOT NULL
		  ORDER BY total_blocks DESC
		  LIMIT 1`).Scan(&blockSize, &total, &used, &free); err != nil {
		return fmt.Errorf("read pragma_database_size: %w", err)
	}
	fmt.Printf("block_size=%d\n", blockSize)
	fmt.Printf("total_blocks=%d\n", total)
	fmt.Printf("used_blocks=%d\n", used)
	fmt.Printf("free_blocks=%d\n", free)
	fmt.Printf("reclaimable_bytes=%d\n", free*blockSize)
	return nil
}
func run(ctx context.Context, path string, blocksOnly bool) error {
	s, err := store.OpenReadOnly(path) // dbcount only reads row counts
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	// The block accounting comes first and always: it is one pragma, it costs
	// nothing, and the counts below are the expensive part. --blocks stops here,
	// which is what lets `compact` ask "is there anything to reclaim?" of a corpus
	// whose coverage it does not need — and of the ETSI half, which has no
	// spec_versions table to count at all.
	if err := reportBlocks(ctx, s); err != nil {
		return err
	}
	if blocksOnly {
		return nil
	}

	sv, err := s.CountSpecVersions(ctx)
	if err != nil {
		return fmt.Errorf("count spec_versions: %w", err)
	}
	ao, err := s.CountAPIOperations(ctx)
	if err != nil {
		return fmt.Errorf("count api_operations: %w", err)
	}
	fmt.Printf("spec_versions=%d\n", sv)
	fmt.Printf("api_operations=%d\n", ao)
	// The bake needs two more facts, and both must come from the DB rather than
	// from a log. corpus-data-image.yml used to read its vectorised count out of
	// the overlay's stdout, and the comment there records what that cost: the
	// Rust rewrite changed the wording, the greps were never repointed, and the
	// count silently read 0 for months. A number taken from the thing being
	// measured cannot drift that way.
	//
	// Counting through `clauses` is deliberate. On a content-addressed corpus
	// (ADR 0004) that name is a view and the embedding comes off `bodies`, so
	// this counts OCCURRENCES that resolve to a vector — the same unit the old
	// lexical+overlay corpus reported, which keeps the bake's >= 2,000,000
	// threshold meaning what it always meant. Measured at 5.2 s on the real
	// corpus: DuckDB never rebuilds the text, because nothing selects it.
	var vec int64
	if err := s.QueryRowContext(ctx,
		`SELECT count(*) FROM clauses WHERE embedding IS NOT NULL`).Scan(&vec); err != nil {
		return fmt.Errorf("count clauses with vectors: %w", err)
	}
	fmt.Printf("clauses_with_vectors=%d\n", vec)
	fmt.Printf("embedding_model=%s\n", s.GetMeta(ctx, "embedding_model"))

	// The sparse layer, for the same reason: the bake has to decide which model
	// to bake, and that decision depends on whether this corpus carries sparse
	// postings at all. Asking the DB is the only way to know — an image baked
	// with a dense-only active model over a corpus that HAS a sparse layer serves
	// it with one retrieval arm silently missing, because SparseCapable() reads
	// the active registry entry and search.Engine then just drops the arm.
	//
	// A corpus predating the sparse pass has no clause_sparse table at all, which
	// is 0, not an error: the counter must work on both shapes or the guard that
	// consumes it becomes conditional on corpus age.
	var sparse int64
	if err := s.QueryRowContext(ctx,
		`SELECT count(*) FROM clause_sparse`).Scan(&sparse); err != nil {
		sparse = 0
	}
	fmt.Printf("clauses_with_sparse=%d\n", sparse)
	fmt.Printf("sparse_model=%s\n", s.GetMeta(ctx, "sparse_model"))
	return nil
}
