// Command seed-glossary writes the abbreviations a spec declares about itself
// into the corpus glossary, so resolve_term can answer from the document that
// defines the term instead of from whatever else spelled it the same way.
//
// Usage:
//
//	seed-glossary --db data/3gpp.duckdb [--specs 23.501,23.401,...]
//	              [--min 150] [--check-only] [--report json]
//	              [--allow-mass-removal]
//
// The behaviour lives in internal/glossaryseed; this is the thin CLI around it
// (cmd/CLAUDE.md). It REPLACES the rows it owns — those citing a spec id — and
// nothing else: what the sweep declares is written, a seeded row no spec declares
// any more is removed, and the TS 21.905 and ETSI entries stay exactly where they
// are. It is idempotent: re-running over an unchanged corpus writes nothing. What
// changes is which meaning a reader is shown first.
//
// A SEEDED ROW TS 21.905 STILL DECLARES IS NEVER REMOVED. When a spec took the
// key of a TS 21.905 entry and later stops declaring it, the row is handed back —
// re-stamped as TS 21.905's own — so the entry stays where TS 21.905 put it.
// Every run reads TS 21.905 to know; a run that cannot read it releases nothing
// and says so ("withheld").
//
// A run that would remove more rows than the mass-removal guard allows, or leave
// a spec the catalogue still lists with none of its rows, is REFUSED: nothing is
// written and it exits 1. --allow-mass-removal lets a deliberate cleanup through;
// the pipeline never passes it.
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
	allowMass := flag.Bool("allow-mass-removal", false,
		"let through a removal the mass-removal guard refuses (too many rows, or a spec "+
			"still in the catalogue losing all of its rows) — a deliberate cleanup only; "+
			"the pipeline never passes it")
	format := flag.String("report", "text", "text | json")
	flag.Parse()
	if *db == "" {
		// Through emit, not Fprintln: --report json advertises that every run
		// prints one JSON object, and an error path that prints prose instead
		// makes the mode unusable for the caller that chose it.
		emit(glossaryseed.Report{Min: *min, Error: "--db is required"}, *format)
		os.Exit(2)
	}

	rep, err := glossaryseed.Run(context.Background(), *db, glossaryseed.Options{
		Specs:            strings.Split(*specs, ","),
		Min:              *min,
		CheckOnly:        *checkOnly,
		AllowMassRemoval: *allowMass,
	})
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
	// TS 21.905 is what every release was checked against, so the log says what
	// was read — or, loudly, that it could not be, which is why nothing was
	// released. A run that stopped before reading it prints neither.
	switch g := rep.General; {
	case g.Unread != "":
		fmt.Printf("seed-glossary: TS %s UNREAD — %s; no seeded row is released this run\n",
			g.Spec, g.Unread)
	case g.Version != "":
		fmt.Printf("seed-glossary: TS %s v%s (%s): %d pairs — a seeded row it declares is "+
			"handed back to it, never removed\n", g.Spec, g.Version, g.Release, g.Pairs)
	}
	switch {
	case !rep.Applied:
		fmt.Printf("seed-glossary: parsed=%d (check-only, floor %d)\n", rep.Parsed, rep.Min)
		if rep.OK {
			fmt.Printf("seed-glossary: a write would rewrite %d row(s), remove %d and hand %d back "+
				"to TS 21.905\n", rep.Rewritten, rep.Removed, rep.Restored)
		}
	case rep.Changed:
		fmt.Printf("seed-glossary: parsed=%d written=%d rewritten=%d removed=%d restored=%d (floor %d)\n",
			rep.Parsed, rep.Written, rep.Rewritten, rep.Removed, rep.Restored, rep.Min)
	default:
		// Said out loud, because "written=679" used to be printed either way. A
		// reader of the enrich log needs to tell a corpus left untouched from a
		// step that did not run — the first means the image need not be pushed.
		fmt.Printf("seed-glossary: parsed=%d — already correct, corpus untouched (floor %d)\n",
			rep.Parsed, rep.Min)
	}
	// THE REMOVED ROWS ARE NAMED IN THE LOG, not only counted. The enrich log is
	// read in text mode, and "removed=37" says that the glossary shrank without
	// saying what a reader can no longer find. The first few are enough to judge
	// a run by; --report json carries every one. The rows handed back and the rows
	// withheld are named the same way: each is a row whose citation moved, or a
	// release a failed read stopped.
	removed, restored := "removed", "handed back"
	if !rep.Applied {
		removed, restored = "would remove", "would hand back"
	}
	name(rep.RemovedRows, removed, "")
	name(rep.RestoredRows, restored, " -> TS 21.905")
	name(rep.WithheldRows, "withheld", ", TS 21.905 unread")
	// The guard's verdict is printed on every run that reached it, pass included:
	// "pass" with its numbers is what tells a reader of the enrich log how far this
	// run was from being refused, which a silent pass never would. It counts every
	// row the sweep dropped, however the write disposes of it — see massRemoval.
	released := rep.Removed + rep.Restored + rep.Withheld
	switch rep.Guard {
	case "pass":
		fmt.Printf("seed-glossary: mass-removal guard: pass — %d of %d seeded row(s) released "+
			"(%d removed, %d handed back, %d withheld; bound %d), no catalogued spec silenced\n",
			released, rep.Owned, rep.Removed, rep.Restored, rep.Withheld, rep.RemovalBound)
	case "overridden":
		fmt.Printf("seed-glossary: mass-removal guard: OVERRIDDEN by --allow-mass-removal — "+
			"%d of %d seeded row(s) (bound %d), %d catalogued spec(s) silenced\n",
			released, rep.Owned, rep.RemovalBound, len(rep.Vanished))
	case "refused":
		fmt.Printf("seed-glossary: mass-removal guard: REFUSED — nothing written "+
			"(%d of %d seeded row(s), bound %d, %d catalogued spec(s) silenced)\n",
			released, rep.Owned, rep.RemovalBound, len(rep.Vanished))
	}
	if rep.Error != "" {
		fmt.Fprintf(os.Stderr, "seed-glossary: %s\n", rep.Error)
	}
}

// name prints the first rows of a list, and how many more --report json holds.
func name(rows []glossaryseed.RemovedRow, verb, note string) {
	const shown = 10
	for i, r := range rows {
		if i == shown {
			fmt.Printf("seed-glossary:   … and %d more (--report json lists every one)\n", len(rows)-shown)
			break
		}
		fmt.Printf("seed-glossary:   %s %s = %q (%s%s)\n", verb, r.Term, r.Expansion, r.Source, note)
	}
}
