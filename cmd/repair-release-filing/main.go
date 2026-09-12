// Command repair-release-filing moves a stored document to the release its own
// version number names, when the corpus files it under one that number contradicts.
//
// The decisions and the write live in internal/refile, which documents why each
// filing moves or does not; this is the CLI around it — flags, the two report
// forms, and the read-only-until-there-is-work open.
//
// Measured on the corpus published 2026-09-12: 74 filings have a release their
// version's major contradicts, 12 of them carry text (4 112 clause occurrences of
// Rel-18/Rel-19 documents filed under Rel-20), and in every one of the twelve that
// filing is the ONLY copy of the version in the corpus. It moves those twelve and
// leaves the other 62 alone, each with its reason printed. Nothing is deleted.
//
// ALL TWELVE OR NONE: one transaction covers every move (refile.Apply). It is run
// by hand, once, on a corpus that is then published, so a failure on the seventh
// filing must not leave six committed. Recovery is "fix the cause and run it
// again" — the repair is idempotent.
//
//	repair-release-filing --db data/3gpp.duckdb                  # dry run, reports
//	repair-release-filing --db data/3gpp.duckdb --apply          # writes
//	repair-release-filing --db data/3gpp.duckdb --report json    # for a machine
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/kodflow/3gpp-mcp/internal/refile"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

func main() {
	db := flag.String("db", "", "corpus DuckDB to repair (required)")
	apply := flag.Bool("apply", false, "actually move the filings; without it this only reports")
	report := flag.String("report", "text", `"text" for an operator, "json" for a machine (cmd/CLAUDE.md)`)
	flag.Parse()
	if *db == "" {
		fmt.Fprintln(os.Stderr, "repair-release-filing: --db is required")
		os.Exit(2)
	}
	if *report != "text" && *report != "json" {
		fmt.Fprintf(os.Stderr, "repair-release-filing: --report %q is neither text nor json\n", *report)
		os.Exit(2)
	}
	res, err := run(*db, *apply, *report == "json", os.Stdout)
	if err != nil {
		// A FAILURE IS A RESULT TOO. `--report json` promises a self-contained JSON
		// response on every path, so the error goes into it rather than beside it on
		// stderr, where a caller parsing stdout would see nothing and no reason.
		if *report == "json" {
			if res == nil {
				res = &result{DB: *db, Applied: *apply}
			}
			res.Error = err.Error()
			_ = writeJSONTo(os.Stdout, res)
		}
		fmt.Fprintln(os.Stderr, "repair-release-filing:", err)
		os.Exit(1)
	}
	if *report == "json" {
		if err := writeJSONTo(os.Stdout, res); err != nil {
			fmt.Fprintln(os.Stderr, "repair-release-filing:", err)
			os.Exit(1)
		}
	}
}

// result is the machine-readable answer: every filing considered, what was decided
// about it and why, and whether the decision was carried out. `--report json`
// prints exactly this, on the success, dry-run, no-op and failure paths alike.
type result struct {
	DB          string      `json:"db"`
	Applied     bool        `json:"applied"`
	Candidates  int         `json:"candidates"`
	Moved       int         `json:"moved"`
	Occurrences int         `json:"occurrences"`
	LeftAlone   int         `json:"left_alone"`
	Filings     []filingOut `json:"filings"`
	Error       string      `json:"error,omitempty"`
}

// filingOut is one filing's decision. `action` is "move" or "keep"; a "keep"
// always carries its reason, because a repair that skips silently cannot be told
// apart from one that misses silently.
type filingOut struct {
	SpecID      string `json:"spec_id"`
	Release     string `json:"release"`
	Version     string `json:"version"`
	Target      string `json:"target"`
	Occurrences int    `json:"occurrences"`
	Action      string `json:"action"`
	Reason      string `json:"reason,omitempty"`
	Destination string `json:"destination,omitempty"`
}

func writeJSONTo(out io.Writer, res *result) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(res)
}

// run plans, reports, and with apply=true writes. It opens READ-ONLY until it
// knows there is work, so a second pass over a repaired corpus cannot touch the
// file at all — it is run by hand on a 23 GB corpus whose every byte is an image
// layer.
//
// Measured, because the obvious justification for that turned out to be false:
// opening this corpus read-write and closing it without executing a statement
// leaves it identical to the byte on this DuckDB build. So the read-only open is a
// guarantee, not a repair of something observed — and TestASecondPassIsANoOp
// cannot falsify it, which is why the test asserts the file bytes rather than how
// the file was opened.
func run(dbPath string, apply, asJSON bool, out io.Writer) (*result, error) {
	res := &result{DB: dbPath, Applied: apply}
	say := func(format string, args ...any) {
		if !asJSON {
			fmt.Fprintf(out, format, args...)
		}
	}
	ro, err := store.OpenReadOnly(dbPath)
	if err != nil {
		return res, fmt.Errorf("open %s: %w", dbPath, err)
	}
	ctx := context.Background()
	plan, err := refile.Plan(ctx, ro.DB())
	_ = ro.Close()
	if err != nil {
		return res, err
	}

	moving := 0
	for _, d := range plan {
		row := filingOut{
			SpecID: d.SpecID, Release: d.Release, Version: d.Version,
			Target: d.Target, Occurrences: d.Occ,
		}
		if d.Move {
			moving++
			res.Occurrences += d.Occ
			row.Action, row.Destination = "move", d.Dest
			say("  MOVE %-11s %-7s %-9s -> %-7s %5d occurrence(s), %s\n",
				d.SpecID, d.Release, d.Version, d.Target, d.Occ, d.Detail())
		} else {
			row.Action, row.Reason = "keep", d.Why
			say("  KEEP %-11s %-7s %-9s    %s\n", d.SpecID, d.Release, d.Version, d.Why)
		}
		res.Filings = append(res.Filings, row)
	}
	res.Candidates, res.LeftAlone = len(plan), len(plan)-moving
	say("repair-release-filing: %d filing(s) whose release the version's major contradicts; %d to move, %d occurrence(s); %d left alone\n",
		len(plan), moving, res.Occurrences, res.LeftAlone)

	if moving == 0 {
		say("repair-release-filing: nothing to move — corpus untouched\n")
		return res, nil
	}
	if !apply {
		say("repair-release-filing: DRY RUN — pass --apply to write\n")
		return res, nil
	}

	st, err := store.Open(dbPath)
	if err != nil {
		return res, fmt.Errorf("open %s read-write: %w", dbPath, err)
	}
	defer func() { _ = st.Close() }()
	moved, occ, err := refile.Apply(ctx, st.DB(), plan)
	if err != nil {
		return res, err
	}
	// Counted only once the commit succeeded: `moved` is what the corpus now holds,
	// not what was planned. On the failure path Apply rolls everything back and
	// returns 0, so `moved` stays 0 beside the error — which is the true report.
	res.Moved = moved
	say("repair-release-filing: moved %d filing(s), %d occurrence(s)\n", moved, occ)
	return res, nil
}
