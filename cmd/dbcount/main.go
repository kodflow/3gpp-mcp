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
	flag.Parse()
	if *db == "" {
		fmt.Fprintln(os.Stderr, "dbcount: --db is required")
		os.Exit(2)
	}
	if err := run(context.Background(), *db); err != nil {
		fmt.Fprintln(os.Stderr, "dbcount:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, path string) error {
	s, err := store.OpenReadOnly(path) // dbcount only reads row counts
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

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
	return nil
}
