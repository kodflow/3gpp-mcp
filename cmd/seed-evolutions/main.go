// Command seed-evolutions writes the curated EPC(4G) -> 5GC(5G) network-element ->
// network-function edge seed (internal/evolseed) into a corpus, and CHECKS every
// edge's citation (internal/evolcheck) before it lands.
//
//	seed-evolutions --db data/3gpp.duckdb [--strict] [--check-only] [--report json]
//
// Why this binary exists. internal/evolseed carries the seed and
// internal/store.ReplaceEvolutions writes it, but between them there was NOTHING:
// the seed's only production consumer used to be the Go ingest (internal/ingest),
// and when the write side moved to rust/ingest that caller disappeared with it.
// evolseed.SeedHash() kept being folded into the published identity, so an edit to
// the seed still shifted global_enrichment_identity and still forced an enricher
// refresh — while the enricher that was supposed to APPLY it no longer existed.
// The corpus therefore kept whatever edges the Rust merge folded up from shard #0,
// which is exactly the "stale seed wins against fresh code" failure evolseed's own
// doc comment says hosting the seed CGO-free was meant to end.
//
// The two failure modes are graded differently, on purpose:
//
//   - MISSING clause: fatal. Nothing in the corpus backs the claim.
//   - clause exists but does not NAME the target: warned, and fatal under --strict.
//     A clause can legitimately describe a function without spelling its acronym.
//
// Both are counted and reported, so a run that degrades the seed is visible in the
// pipeline log rather than silently green.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/kodflow/3gpp-mcp/internal/evolcheck"
	"github.com/kodflow/3gpp-mcp/internal/evolseed"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

var Version = "dev"

// report is the --report json shape: the counts a caller would otherwise have to
// scrape out of prose, plus the offending edges themselves.
type report struct {
	Seed    int              `json:"seed"`
	Hash    string           `json:"seed_hash"`
	Missing []string         `json:"missing_clause"`
	Unnamed []string         `json:"clause_does_not_name_target"`
	Written bool             `json:"written"`
	OK      bool             `json:"ok"`
	Detail  evolcheck.Result `json:"detail"`
	Error   string           `json:"error,omitempty"`
}

func main() {
	db := flag.String("db", "", "corpus DuckDB to seed (required)")
	strict := flag.Bool("strict", false, "treat an edge whose clause never names its target as fatal")
	checkOnly := flag.Bool("check-only", false, "verify the seed against the corpus and report, without writing")
	format := flag.String("report", "text", "text | json")
	flag.Parse()
	if *db == "" {
		fmt.Fprintln(os.Stderr, "seed-evolutions: --db is required")
		os.Exit(2)
	}

	rep, err := run(context.Background(), *db, *strict, *checkOnly)
	if err != nil {
		rep.Error = err.Error()
	}
	emit(rep, *format)
	if err != nil {
		os.Exit(1)
	}
}

func run(ctx context.Context, path string, strict, checkOnly bool) (report, error) {
	rep := report{Hash: evolseed.SeedHash()}

	s, err := store.Open(path)
	if err != nil {
		return rep, err
	}
	defer func() { _ = s.Close() }()

	seed := evolseed.Seed()
	rep.Seed = len(seed)
	if len(seed) == 0 {
		return rep, fmt.Errorf("the seed is empty — refusing to wipe the corpus's edges for nothing")
	}

	res, err := evolcheck.Verify(ctx, s, seed)
	if err != nil {
		return rep, err
	}
	rep.Detail = res
	for _, e := range res.Missing {
		rep.Missing = append(rep.Missing, fmt.Sprintf("%s cites %s §%s",
			evolcheck.Describe(e), e.JustificationSpec, e.JustificationClause))
	}
	for _, e := range res.Unnamed {
		rep.Unnamed = append(rep.Unnamed, fmt.Sprintf("%s cites %s §%s, which never names %q",
			evolcheck.Describe(e), e.JustificationSpec, e.JustificationClause, e.ToTerm))
	}

	if !res.OK() {
		return rep, fmt.Errorf("%d edge(s) cite a clause the corpus does not have", len(res.Missing))
	}
	if strict && len(res.Unnamed) > 0 {
		return rep, fmt.Errorf("--strict: %d edge(s) cite a clause that never names the target", len(res.Unnamed))
	}
	if checkOnly {
		rep.OK = true
		return rep, nil
	}

	if err := s.ReplaceEvolutions(ctx, seed); err != nil {
		return rep, err
	}
	rep.Written, rep.OK = true, true
	return rep, nil
}

func emit(rep report, format string) {
	if format == "json" {
		b, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Println(string(b))
		return
	}
	fmt.Printf("seed-evolutions: seed=%d hash=%s missing_clause=%d clause_does_not_name_target=%d\n",
		rep.Seed, rep.Hash, len(rep.Missing), len(rep.Unnamed))
	for _, m := range rep.Missing {
		fmt.Fprintf(os.Stderr, "seed-evolutions: FATAL %s, which is NOT in the corpus\n", m)
	}
	for _, u := range rep.Unnamed {
		fmt.Fprintf(os.Stderr, "seed-evolutions: WARN %s\n", u)
	}
	if rep.Written {
		fmt.Printf("seed-evolutions: wrote %d edge(s)\n", rep.Seed)
	}
	if rep.Error != "" {
		fmt.Fprintln(os.Stderr, "seed-evolutions:", rep.Error)
	}
}
