// Command seed-glossary writes the abbreviations a spec declares about itself
// into the corpus glossary, so resolve_term can answer from the document that
// defines the term instead of from whatever else spelled it the same way.
//
// Usage:
//
//	seed-glossary --db data/3gpp.duckdb [--specs 23.501,23.401,...]
//	              [--min 150] [--check-only] [--report json]
//
// The behaviour lives in internal/glossaryseed; this is the thin CLI around it
// (cmd/CLAUDE.md). It is additive and idempotent: rows are upserted on
// (term, expansion, domain), so re-running changes nothing and the existing
// TS 21.905 and ETSI entries stay where they are. What changes is which meaning
// a reader is shown first.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/glossaryseed"
)

func main() {
	db := flag.String("db", "", "path to the corpus DuckDB")
	specs := flag.String("specs", glossaryseed.DefaultSpecs,
		"comma-separated spec ids whose Abbreviations clause to read")
	min := flag.Int("min", glossaryseed.DefaultMin,
		"fail if fewer than this many abbreviations are parsed in total")
	checkOnly := flag.Bool("check-only", false, "parse and report, write nothing")
	format := flag.String("report", "text", "text | json")
	flag.Parse()
	if *db == "" {
		// Through emit, not Fprintln: --report json advertises that every run
		// prints one JSON object, and an error path that prints prose instead
		// makes the mode unusable for the caller that chose it.
		emit(glossaryseed.Report{Min: *min, Error: "--db is required"}, *format)
		os.Exit(2)
	}

	rep, err := glossaryseed.Run(context.Background(), *db, strings.Split(*specs, ","), *min, *checkOnly)
	if err != nil {
		rep.Error = err.Error()
	}
	emit(rep, *format)
	if err != nil {
		os.Exit(1)
	}
}

func emit(rep glossaryseed.Report, format string) {
	if format == "json" {
		b, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Println(string(b))
		return
	}
	sort.Slice(rep.Specs, func(i, j int) bool { return rep.Specs[i].Spec < rep.Specs[j].Spec })
	for _, sr := range rep.Specs {
		if sr.Skipped != "" {
			fmt.Printf("seed-glossary: %-8s skipped — %s\n", sr.Spec, sr.Skipped)
			continue
		}
		fmt.Printf("seed-glossary: %-8s v%-8s §%-5s parsed=%d written=%d\n",
			sr.Spec, sr.Version, sr.Clause, sr.Parsed, sr.Written)
	}
	switch {
	case !rep.Applied:
		fmt.Printf("seed-glossary: parsed=%d (check-only, floor %d)\n", rep.Parsed, rep.Min)
	case rep.Changed:
		fmt.Printf("seed-glossary: parsed=%d written=%d (floor %d)\n", rep.Parsed, rep.Written, rep.Min)
	default:
		// Said out loud, because "written=679" used to be printed either way. A
		// reader of the enrich log needs to tell a corpus left untouched from a
		// step that did not run — the first means the image need not be pushed.
		fmt.Printf("seed-glossary: parsed=%d — already correct, corpus untouched (floor %d)\n",
			rep.Parsed, rep.Min)
	}
	if rep.Error != "" {
		fmt.Fprintf(os.Stderr, "seed-glossary: %s\n", rep.Error)
	}
}
