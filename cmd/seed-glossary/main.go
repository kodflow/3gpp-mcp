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
// any more is removed, and the TS 21.905 and ETSI entries stay where they are —
// except a TS 21.905 row whose key the newest TS 21.905 no longer stores, which is
// retired (a version corrected or dropped it). It is idempotent: re-running over an unchanged corpus writes nothing. What
// changes is which meaning a reader is shown first.
//
// A SEEDED ROW WHOSE KEY TS 21.905'S WRITER STORES IS NEVER REMOVED. When a spec
// took the key of a TS 21.905 entry and later stops declaring it, the row is
// handed back — re-stamped as TS 21.905's own — so the entry stays where
// TS 21.905 put it. Every run reads TS 21.905 to know; a run that cannot read it
// releases nothing and says so, loudly ("withheld").
//
// A run that would remove more rows than the mass-removal guard allows, or delete
// rows of a spec the catalogue still lists and the sweep no longer hears from, is
// REFUSED: nothing is written and it exits 1. The guard counts deletions only — a
// row handed back or withheld is not one. --allow-mass-removal lets a deliberate
// cleanup through; the pipeline never passes it.
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
		fmt.Printf("seed-glossary: TS %s v%s (%s): %d keys its writer stores — a seeded row "+
			"carrying one is handed back to it, never removed\n", g.Spec, g.Version, g.Release, g.Pairs)
	}
	switch {
	case !rep.Applied:
		fmt.Printf("seed-glossary: parsed=%d (check-only, floor %d)\n", rep.Parsed, rep.Min)
		if rep.OK {
			fmt.Printf("seed-glossary: a write would rewrite %d row(s), remove %d, hand %d back "+
				"to TS 21.905 and retire %d of its own\n", rep.Rewritten, rep.Removed, rep.Restored, rep.Retired)
		}
	case rep.Changed:
		fmt.Printf("seed-glossary: parsed=%d written=%d rewritten=%d removed=%d restored=%d retired=%d (floor %d)\n",
			rep.Parsed, rep.Written, rep.Rewritten, rep.Removed, rep.Restored, rep.Retired, rep.Min)
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
	removed, restored, retired := "removed", "handed back", "retired"
	if !rep.Applied {
		removed, restored, retired = "would remove", "would hand back", "would retire"
	}
	name(rep.RemovedRows, removed, "")
	name(rep.RestoredRows, restored, " -> TS 21.905")
	name(rep.RetiredRows, retired, ", no longer in the newest TS 21.905")
	name(rep.WithheldRows, "withheld", ", TS 21.905 unread")
	// The guard's verdict is printed on every run that reached it, pass included:
	// "pass" with its numbers is what tells a reader of the enrich log how far this
	// run was from being refused, which a silent pass never would. It counts
	// deletions and nothing else — see massRemoval — so its line says removed, and
	// names the TS 21.905 rows retired, which are deletions too.
	retiredToo := ""
	if rep.Retired > 0 {
		retiredToo = fmt.Sprintf(" and %d TS 21.905 row(s) retired", rep.Retired)
	}
	switch rep.Guard {
	case "pass":
		fmt.Printf("seed-glossary: mass-removal guard: pass — %d of %d seeded row(s) removed%s "+
			"(bound %d), no catalogued spec silenced\n", rep.Removed, rep.Owned, retiredToo, rep.RemovalBound)
	case "overridden":
		fmt.Printf("seed-glossary: mass-removal guard: OVERRIDDEN by --allow-mass-removal — "+
			"%d of %d seeded row(s)%s (bound %d), %d catalogued spec(s) silenced\n",
			rep.Removed, rep.Owned, retiredToo, rep.RemovalBound, len(rep.Vanished))
	case "refused":
		fmt.Printf("seed-glossary: mass-removal guard: REFUSED — nothing written "+
			"(%d of %d seeded row(s)%s, bound %d, %d catalogued spec(s) silenced)\n",
			rep.Removed, rep.Owned, retiredToo, rep.RemovalBound, len(rep.Vanished))
	}
	// WITHHELD ROWS ARE SAID APART, AND LOUDLY. The guard no longer counts them —
	// they are not deletions — so this line is what keeps them from passing
	// unnoticed: a count here means TS 21.905 could not be read, that the glossary
	// keeps rows its specs dropped, and that the next unread run keeps these and
	// adds its own. stderr, so it reaches an operator who reads only the errors.
	if rep.Withheld > 0 {
		fmt.Fprintf(os.Stderr, "seed-glossary: WARNING — %d seeded row(s) no spec declares any more are "+
			"WITHHELD, left in place, because TS %s could not be read (%s). They stay until a run "+
			"can read it, which then removes them or hands them back.\n",
			rep.Withheld, rep.General.Spec, rep.General.Unread)
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
