// Package glossaryseed reads the Abbreviations clause of the specs that DECLARE
// the system vocabulary and writes what it finds into the corpus glossary.
//
// It lives here rather than in cmd/seed-glossary because cmd binaries are thin
// CLIs that wire internal packages together (cmd/CLAUDE.md); choosing a version,
// locating the clause, enforcing the floor and persisting the rows is BEHAVIOUR,
// and behaviour with its own tests belongs in a package another caller — a
// future gate, a report — can reuse. internal/abbrev holds the parsing rule and
// stays CGO-free; this package is where the corpus is touched.
//
// WHAT IT REPAIRS. Measured 2026-09-05 on the shipped corpus: of the 30 main 5GC
// network functions, resolve_term was right about two, wrong about nine, and
// silent about nineteen. The 3GPP half's glossary came only from TS 21.905,
// which does not name them; the ETSI half supplied "ATM Mapping Function" for
// AMF because in that corpus it is one.
//
// PRECEDENCE IS THE SPEC'S OWN RULE. TS 23.501 §3.2 opens: "An abbreviation
// defined in the present document takes precedence over the definition of the
// same abbreviation, if any, in TR 21.905 [1]." Store.ResolveTerm ranks these
// rows above the general vocabulary for that reason and no other.
package glossaryseed

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/kodflow/3gpp-mcp/internal/abbrev"
	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// DefaultSpecs are the specs that DEFINE the system vocabulary this corpus is
// asked about. Each is here for a named reason, not to pad the list:
//
//	23.501  5GC architecture   — the network functions; the measured failure
//	23.401  EPS architecture   — the EPC names the same questions reach for
//	24.501  5GS NAS            — the protocol-side terms
//	33.501  5G security        — the security vocabulary
//	23.548  edge computing     — EASDF and the edge terms, defined nowhere above
//	23.682  exposure / MTC     — SCEF, which nothing else in this list declares
//
// IT USED TO BE THE SCOPE, AND THAT WAS THE DEFECT. The comment here read: "a
// corpus-wide sweep would let an obscure study item outrank an architecture spec
// on a term they spell differently". That was true when it was written, and it
// stopped being true when declared_by arrived on the ETSI arm: ranking by HOW
// MANY specs declare an expansion settles exactly that disagreement.
//
// Measured 2026-09-08 on the shipped corpus, sweeping all 3 497 specs that carry
// an Abbreviations clause and ranking by declared_by: AMF, SMF, UPF, NWDAF, PCF,
// UDM, AUSF, NRF, NSSF, SMSF, NEF and UDR all resolve to the architecture answer
// — 12 of 12 — with the wrong spellings sitting far below on one or two
// declarations. The narrow scope was costing coverage (1 781 rows against the
// ETSI half's 28 154) to buy a ranking that declared_by already provides.
//
// So these six are no longer the scope. They are the PROVENANCE PREFERENCE: when
// several specs declare the same expansion, the row cites one of these if one of
// them is among the declarers, because that is what makes the precedence in
// Store.ResolveTerm auditable to a reader.
//
// TS 23.502 is deliberately ABSENT even though it is a core 5GC spec. Its
// Abbreviations clause is 282 characters of introduction and nothing else — it
// defers wholly to 23.501 — so listing it would report "parsed=0" on every run
// and read like a broken parser rather than a spec that declares no vocabulary
// of its own.
//
// KNOWN RESIDUAL: bare "EIR". No architecture spec declares it — 23.002, 23.401
// and 23.501 all name 5G-EIR instead — so it keeps TS 21.905's entry, and 21.905
// spells it "Equipment Identity Centre". That reads like an error in the source,
// and the corpus reproduces its sources faithfully rather than correcting them;
// "Equipment Identity Register" is present as a second row. Fixing it means
// declaring a term no spec declares, which is the failure mode this package was
// written to end.
const DefaultSpecs = "23.501,23.401,24.501,33.501,23.548,23.682"

// DefaultMin is the aggregate floor below which a run is treated as a broken
// read rather than a small vocabulary.
const DefaultMin = 150

// THE MASS-REMOVAL GUARD, and why the floor above is not enough.
//
// The write REPLACES the seeded rows, so a sweep that silently loses specs now
// DELETES their rows — where the additive writer before it could only have left
// them stale. The floor cannot see that: it is measured on the six preferred
// specs, and a read that loses any of the other ~1 980 owning specs passes it
// untouched. So a run is refused — nothing written, non-zero exit — on either of
// two signatures, unless --allow-mass-removal says the removal is deliberate:
//
//  1. A SPEC GOES SILENT. It owns rows today, the catalogue still lists it, and
//     the sweep came back with not one row from it (store.GlossaryDiff.Vanished).
//     That is what a broken read looks like and what an editorial change almost
//     never does; a spec that really dropped its vocabulary is rare enough to be
//     worth one deliberate flag.
//  2. TOO MANY ROWS AT ONCE — more than removalBound. This catches the loss that
//     rule 1 cannot: a regression spread across many specs, each of which still
//     yields something.
//
// THE BOUND IS DERIVED, from measurements taken 2026-09-11 on the shipped corpus:
//
//   - normal churn: the next run removes 1 row of the 13 722 seeded — 0.007 %;
//   - the unit of legitimate churn is ONE spec re-issuing its list, which can
//     remove at most what that spec owns: 1 984 specs own the 13 722 rows, median
//     3, p90 17, p99 53, max 174 (24.501, a preferred spec), then 134 (33.501),
//     127 (23.501), 124 (38.889).
//
// removalBoundPct = 1 %, 137 rows today: over a hundred times the measured churn,
// and above the ENTIRE vocabulary of every spec but 24.501 — so any one spec,
// re-issued with a rewritten list, still passes, while losses across several
// specs at once do not. removalBoundRows = 53, the p99 above, is the floor under
// that fraction: it only binds below 5 300 seeded rows (a partial rebuild, a test
// corpus), where 1 % would refuse a single ordinary spec's re-issue.
const (
	removalBoundPct  = 1
	removalBoundRows = 53
)

// removalBound is the most seeded rows one run may remove without
// --allow-mass-removal.
func removalBound(owned int) int {
	return max(removalBoundRows, owned*removalBoundPct/100)
}

// massRemoval says why a diff would be refused, or "" when it passes.
//
// RULE 2 COUNTS EVERY ROW THE SWEEP DROPPED — store.GlossaryDiff.Released — not
// only the rows the write deletes. Since 2026-09-11 a dropped row TS 21.905 still
// declares is handed back to it rather than deleted, and one the run could not
// clear is withheld; before that, every one of them was a removal. Counting all
// three keeps this verdict, on any given sweep, exactly what it was before the
// hand-back existed: the bound was derived from what one spec can DROP, and a
// sweep that lost half its specs is no less broken because TS 21.905 happens to
// declare many of their keys.
func massRemoval(d store.GlossaryDiff) string {
	var why []string
	if n, bound := d.Released(), removalBound(d.Owned); n > bound {
		// The words the guard has always used, while they are the whole truth.
		what := fmt.Sprintf("remove %d", n)
		if n != len(d.Removed) {
			what = fmt.Sprintf("release %d (%d removed, %d handed back to TS 21.905, %d withheld)",
				n, len(d.Removed), len(d.Restored), len(d.Withheld))
		}
		why = append(why, fmt.Sprintf("it would %s of the %d seeded rows, above the bound of %d",
			what, d.Owned, bound))
	}
	if len(d.Vanished) > 0 {
		// The first twenty by name; the JSON report carries every one. A broken
		// sweep can silence a thousand specs, and a message that long is not read.
		const named = 20
		var names []string
		for i, v := range d.Vanished {
			if i == named {
				names = append(names, fmt.Sprintf("and %d more", len(d.Vanished)-named))
				break
			}
			names = append(names, fmt.Sprintf("%s (%d rows)", v.Spec, v.Owned))
		}
		why = append(why, fmt.Sprintf("%d spec(s) still in the catalogue would lose ALL their rows: %s",
			len(d.Vanished), strings.Join(names, ", ")))
	}
	return strings.Join(why, "; and ")
}

// SpecReport is what one spec contributed.
type SpecReport struct {
	Spec    string `json:"spec"`
	Version string `json:"version"`
	Clause  string `json:"clause"`
	Parsed  int    `json:"parsed"`
	Written int    `json:"written"`
	Skipped string `json:"skipped,omitempty"`
}

// Report is the outcome of a run, and the shape of --report json.
type Report struct {
	Specs  []SpecReport `json:"specs"`
	Parsed int          `json:"parsed_total"`
	// Floor is what the aggregate floor is measured on: the contribution of the
	// PREFERRED specs alone. Parsed counts the whole sweep, which is hundreds of
	// times larger and would make any usable floor meaningless.
	Floor   int  `json:"floor_total"`
	Written int  `json:"written_total"`
	Min     int  `json:"min_required"`
	Applied bool `json:"applied"`
	// Changed says whether the corpus actually MOVED. Applied only says a write
	// was attempted; on a corpus already carrying this glossary nothing is
	// written, and that difference is what decides whether the published image
	// has to be pushed again.
	Changed bool `json:"changed"`
	// Rewritten counts the rows the write inserted or rewrote, Removed the seeded
	// rows it took out because no spec declares them any more, and Restored those
	// it handed back to TS 21.905 because TS 21.905 still does — and, on
	// --check-only, what the write WOULD do. The three together are exactly what
	// Changed summarises, so a check-only run answers the push question before
	// anything is written.
	Rewritten int `json:"rewritten_total"`
	Removed   int `json:"removed_total"`
	Restored  int `json:"restored_total"`
	// RemovedRows names every removed row. A deletion nobody can list is the
	// silent failure this package keeps refusing: the count alone would say that
	// the glossary shrank, never what a reader can no longer find.
	RemovedRows []RemovedRow `json:"removed,omitempty"`
	// RestoredRows names every row handed back, with the spec that held it until
	// now as Source. The key stays in the glossary; what moves is its precedence
	// — it now ranks as the general vocabulary, which is what it is.
	RestoredRows []RemovedRow `json:"restored,omitempty"`
	// Withheld counts the rows no spec declares any more that the run left in
	// place because TS 21.905 could not be read (General.Unread), and WithheldRows
	// names them. Nothing is written for them: they are what the table already
	// holds, so they do not make a run Changed.
	Withheld     int          `json:"withheld_total"`
	WithheldRows []RemovedRow `json:"withheld,omitempty"`
	// General is what the run read of TS 21.905 — the evidence every release was
	// checked against. See readGeneral.
	General GeneralReport `json:"ts21905"`
	// The mass-removal guard's inputs and verdict — see removalBound. Guard is
	// "pass", "refused" or "overridden" (refused, and let through by
	// --allow-mass-removal), and --check-only reaches the same verdict as the
	// write would, from the same diff, without writing.
	Owned        int            `json:"owned_total"`
	RemovalBound int            `json:"removal_bound"`
	Vanished     []VanishedSpec `json:"vanished_specs,omitempty"`
	Guard        string         `json:"guard,omitempty"`
	OK           bool           `json:"ok"`
	Error        string         `json:"error,omitempty"`
}

// RemovedRow is one seeded row a run took out of the glossary — or, listed as
// restored or withheld, one it handed back to TS 21.905 or left where it was.
type RemovedRow struct {
	Term      string `json:"term"`
	Expansion string `json:"expansion"`
	Source    string `json:"source"`
}

// GeneralReport is what a run learned of TS 21.905.
type GeneralReport struct {
	Spec    string `json:"spec"`
	Version string `json:"version,omitempty"`
	Release string `json:"release,omitempty"`
	// Pairs is how many (term, expansion) pairs the Abbreviations region yields;
	// Min is the floor under which that is read as a broken read.
	Pairs int `json:"pairs"`
	Min   int `json:"min_required"`
	// Unread says why TS 21.905 could not be read in full. When it is set, no
	// seeded row is released: each one the sweep dropped is withheld instead.
	Unread string `json:"unread,omitempty"`
}

// VanishedSpec is a spec still in the catalogue that the sweep no longer hears
// from: how many rows it owns, how many of them the write would delete, and how
// many it would hand back to TS 21.905.
type VanishedSpec struct {
	Spec     string `json:"spec"`
	Owned    int    `json:"owned"`
	Removed  int    `json:"removed"`
	Restored int    `json:"restored"`
}

// Options is what a run is asked to do.
//
// A struct, not two more positional booleans: CheckOnly and AllowMassRemoval side
// by side in a call are one transposition away from a check-only run that
// refuses nothing, or a real run that was meant to be a check.
type Options struct {
	// Specs are the PREFERRED specs: the floor is measured on them and their id
	// wins provenance. The sweep itself is always the whole corpus.
	Specs []string
	// Min is the floor on the preferred specs' contribution.
	Min int
	// CheckOnly parses, reports and reaches the guard's verdict, and writes
	// nothing — the database is opened read-only.
	CheckOnly bool
	// AllowMassRemoval lets through a removal the guard would refuse. Off by
	// default, and the pipeline's enrich step never sets it: it is for an
	// operator's deliberate cleanup, run by hand.
	AllowMassRemoval bool
}

// Run seeds the glossary from the named specs' Abbreviations clauses.
func Run(ctx context.Context, path string, opt Options) (Report, error) {
	specIDs, min, checkOnly := opt.Specs, opt.Min, opt.CheckOnly
	// Applied stays FALSE until the write actually lands. Setting it from
	// checkOnly up front makes a failed run report applied=true, which is the
	// one field a caller reads to decide whether the corpus changed.
	rep := Report{Min: min}

	// --check-only OPENS READ-ONLY, so "writes nothing" is a property of the
	// handle rather than of every statement Open happens to run. Open migrates
	// the schema in place — CREATE … IF NOT EXISTS, ADD COLUMN IF NOT EXISTS —
	// and a check-only run is exactly what an operator points at the shipped
	// 23 GB corpus to ask what the next write would remove, where one changed
	// byte is a new image layer.
	open := store.Open
	if checkOnly {
		open = store.OpenReadOnly
	}
	s, err := open(path)
	if err != nil {
		return rep, err
	}
	defer func() { _ = s.Close() }()

	// READ EVERYTHING FIRST, WRITE AFTERWARDS. The floor below is what catches a
	// broken read, and checking it after the writes would let a broken read
	// leave rows behind: these rows carry the HIGHEST precedence in
	// Store.ResolveTerm, so a handful of them written before the run aborts
	// would outrank the corpus's real vocabulary and stay there, with the
	// command having exited non-zero as if nothing had happened.
	//
	// And since the write REPLACES the seeded rows, the same ordering is what
	// keeps a broken read from DELETING them: nothing below the floor is ever
	// handed to the store, so a run that fails there removes nothing at all.
	type pending struct {
		sr      SpecReport
		entries []abbrev.Entry
	}
	// THE SCOPE IS THE WHOLE CORPUS. specIDs names the specs whose contribution
	// the FLOOR below is measured on, and whose id is preferred as provenance —
	// not the specs that are read. See DefaultSpecs.
	sweep, err := s.SpecsWithAbbreviations(ctx)
	if err != nil {
		return rep, err
	}
	preferred := map[string]bool{}
	for _, id := range specIDs {
		if id = strings.TrimSpace(id); id != "" {
			preferred[id] = true
		}
	}
	// Every spec is read, the preferred ones LAST — because ReplaceSeededAcronyms
	// keeps the LAST row it sees for a key. Reading them first would have made
	// them the ones overwritten, which is the opposite of the intent and would not
	// have shown up as an error anywhere: the count would be identical and only
	// the cited document would differ.
	order := readOrder(sweep, preferred)

	var todo []pending
	for _, id := range order {
		sr, entries, err := readSpec(ctx, s, id)
		if err != nil {
			return rep, err
		}
		todo = append(todo, pending{sr, entries})
		rep.Parsed += sr.Parsed
		// THE FLOOR IS MEASURED ON THE PREFERRED SPECS ONLY. A sweep parses
		// hundreds of thousands of lines, so any floor low enough to be safe over
		// the whole corpus is far too low to catch a broken read of 23.501 — the
		// failure this floor exists for. Measuring the six keeps the check exactly
		// as sharp as it was when they were the whole scope.
		if preferred[id] {
			rep.Floor += sr.Parsed
		}
	}

	// THE FLOOR. Every failure this package exists to prevent is silent: the
	// clause moves, the heading is spelled differently, the parse yields three
	// rows instead of hundreds — and a run that seeded almost nothing reports
	// success just as loudly as one that worked. A corpus that holds these specs
	// has hundreds of abbreviations; anything far below that is a broken read,
	// not a small vocabulary.
	//
	// It is an AGGREGATE floor on purpose. Per-spec floors were considered and
	// rejected: a legitimate contribution here ranges from 15 rows (23.548) to
	// 220 (23.501), and 23.502 legitimately declares NONE — its clause defers
	// wholly to 23.501. Any per-spec threshold high enough to catch a broken
	// read of 23.501 fails on the small specs that are working correctly.
	if rep.Floor < min {
		return rep, fmt.Errorf("parsed only %d abbreviations across %s, expected at least %d — "+
			"the Abbreviations clause was probably not found or not recognised "+
			"(the corpus-wide sweep read %d specs for %d entries; the floor is measured "+
			"on the named specs so a broken read of one of them cannot hide behind the rest)",
			rep.Floor, strings.Join(specIDs, ","), min, len(todo), rep.Parsed)
	}

	// TS 21.905, READ BEFORE ANYTHING IS PLANNED, on --check-only too — the plan
	// decides from it which dropped rows are removed and which are handed back, so
	// a check that skipped it would predict a different write.
	//
	// A READ THAT FAILS IS NOT AN ERROR HERE, and not a success either: it comes
	// back unread, and the store then releases nothing (store.GeneralVocabulary).
	// Failing the run would stop the whole enrich over rows that were never going
	// to be deleted; succeeding with an empty set would delete every one of them.
	// Only a query that errors — a corpus the store cannot read at all — fails.
	general, gr, err := readGeneral(ctx, s)
	rep.General = gr
	if err != nil {
		return rep, err
	}

	// TALLY FIRST: declared_by is HOW MANY specs declare this exact expansion,
	// and it is what lets a corpus-wide sweep rank correctly. Counting specs,
	// not rows: a spec present at a dozen releases declares its vocabulary
	// once, and counting rows would let a long-lived spec outvote a dozen.
	//
	// The rows are built on --check-only too, because what a check-only run
	// reports is the diff the write would apply — computed by the store from
	// these very rows, not re-derived by a second query of its own.
	type pair struct{ term, expansion string }
	declarers := map[pair]map[string]bool{}
	for i := range todo {
		for _, e := range todo[i].entries {
			k := pair{e.Term, e.Expansion}
			if declarers[k] == nil {
				declarers[k] = map[string]bool{}
			}
			declarers[k][todo[i].sr.Spec] = true
		}
	}

	var rows []model.Acronym
	for i := range todo {
		for _, e := range todo[i].entries {
			rows = append(rows, model.Acronym{
				Term:      e.Term,
				Expansion: e.Expansion,
				// HOW MANY specs declare exactly this expansion.
				// ReplaceSeededAcronyms keeps the LAST row for a key and the
				// preferred specs are read LAST, so the row that survives cites a
				// preferred spec whenever one declares the pair — while the count
				// covers all of them.
				DeclaredBy: len(declarers[pair{e.Term, e.Expansion}]),
				// Domain stays empty on purpose. The clause declares an
				// abbreviation, not which architecture owns it, and stamping
				// "5GC" on all 221 rows of 23.501 §3.2 would assert something
				// the source never said — 5G LAN and QoS live there too.
				Domain:       "",
				FirstRelease: todo[i].sr.Version,
				LastRelease:  todo[i].sr.Version,
				// The owning SPEC, not its two-digit series: it is what makes
				// the precedence above auditable, and it is what marks the row as
				// this package's to replace (store.seededSource). The series form
				// belongs to TS 21.905's rows, which this package never removes —
				// and writes only to hand a row back to TS 21.905 (readGeneral).
				SourceSeries: todo[i].sr.Spec,
			})
		}
	}

	// THE GUARD'S VERDICT, reached ONCE, from the diff that is written — the store
	// calls approve between its plan and its transaction — and reached the same
	// way on --check-only from the same plan, so a check that passes is a write
	// that passes.
	approve := func(d store.GlossaryDiff) error {
		rep.Owned, rep.RemovalBound = d.Owned, removalBound(d.Owned)
		for _, v := range d.Vanished {
			rep.Vanished = append(rep.Vanished, VanishedSpec{Spec: v.Spec, Owned: v.Owned,
				Removed: v.Removed, Restored: v.Restored})
		}
		why := massRemoval(d)
		switch {
		case why == "":
			rep.Guard = "pass"
		case opt.AllowMassRemoval:
			rep.Guard = "overridden"
		default:
			rep.Guard = "refused"
			return fmt.Errorf("refusing to replace the seeded glossary: %s. Nothing was written. "+
				"A sweep that silently lost specs looks exactly like this; if the removal is "+
				"deliberate, re-run with --allow-mass-removal", why)
		}
		return nil
	}

	var diff store.GlossaryDiff
	if checkOnly {
		if diff, err = s.PlanSeededAcronyms(rows, general); err == nil {
			err = approve(diff)
		}
	} else {
		// ONE TRANSACTION for the whole batch, the removal included. Collecting
		// before writing removes the partial write a failed FLOOR would leave; it
		// does nothing about a failure on row 400 of 679, which would leave 399
		// high-precedence rows behind just the same. All of them land or none do.
		//
		// Changed says whether the corpus actually moved, and the report repeats
		// it rather than assuming. Re-seeding a glossary that is already correct
		// writes nothing — which is the point, since one changed byte in this
		// 23 GB file is an 11 GB push — and a run that announced "written=679"
		// either way would hide exactly the thing worth knowing.
		if diff, err = s.ReplaceSeededAcronyms(rows, general, approve); err == nil {
			rep.Applied = true
			rep.Changed = diff.Changed()
			for i := range todo {
				todo[i].sr.Written = len(todo[i].entries)
			}
		}
	}
	// FILLED ON A REFUSAL TOO. The diff is exactly what is being refused, and an
	// operator deciding whether to pass --allow-mass-removal needs to see the rows
	// before deciding, not after.
	rep.Rewritten = diff.Written
	rep.Removed = len(diff.Removed)
	for _, a := range diff.Removed {
		rep.RemovedRows = append(rep.RemovedRows, RemovedRow{a.Term, a.Expansion, a.SourceSeries})
	}
	rep.Restored = len(diff.Restored)
	for _, a := range diff.Restored {
		rep.RestoredRows = append(rep.RestoredRows, RemovedRow{a.Term, a.Expansion, a.SourceSeries})
	}
	rep.Withheld = len(diff.Withheld)
	for _, a := range diff.Withheld {
		rep.WithheldRows = append(rep.WithheldRows, RemovedRow{a.Term, a.Expansion, a.SourceSeries})
	}
	for i := range todo {
		rep.Specs = append(rep.Specs, todo[i].sr)
		rep.Written += todo[i].sr.Written
	}
	if err != nil {
		return rep, err
	}
	rep.OK = true
	return rep, nil
}

// readOrder puts the preferred specs LAST.
//
// It is a function, and tested, because getting it backwards is invisible.
// store.ReplaceSeededAcronyms keeps the LAST row it sees for a (term, expansion,
// domain) key, so reading the preferred specs FIRST — which is what reads
// naturally, and what this code did when it was written — makes them the ones
// overwritten. The row count would be identical, every gate would pass, and the
// only difference would be which document the glossary cites: an obscure spec
// instead of TS 23.501, for the terms where citing 23.501 is the whole point.
func readOrder(sweep []string, preferred map[string]bool) []string {
	order := make([]string, 0, len(sweep))
	for _, id := range sweep {
		if !preferred[id] {
			order = append(order, id)
		}
	}
	for _, id := range sweep {
		if preferred[id] {
			order = append(order, id)
		}
	}
	return order
}

// readSpec finds a spec's newest version, locates its Abbreviations clause and
// parses it.
func readSpec(ctx context.Context, s *store.Store, specID string) (SpecReport, []abbrev.Entry, error) {
	sr := SpecReport{Spec: specID}

	// Clause 3 holds "Definitions, symbols and abbreviations"; the abbreviations
	// are a subclause of it. Reading the whole spec to find one clause would
	// pull tens of thousands of rows.
	clauses, err := s.GetClauses(ctx, specID, "", "3")
	if err != nil {
		return sr, nil, fmt.Errorf("%s: %w", specID, err)
	}
	if len(clauses) == 0 {
		sr.Skipped = "not in this corpus"
		return sr, nil, nil
	}

	best := ""
	for _, c := range clauses {
		if newerVersion(c.Version, best) {
			best = c.Version
		}
	}
	sr.Version = best

	// By HEADING, not by a hardcoded "3.2". The clause number differs between
	// specs and moves between releases; the heading is what the editor writes.
	//
	// THE FIRST CANDIDATE IS NOT A CHOICE, IT IS WHATEVER THE SCAN HANDED BACK.
	//
	// This loop used to `break` on the first match, which reads like a decision and
	// is not one. GetClauses orders by `len(clause_path), clause_path` and nothing
	// else, so two rows sharing a clause_path — the same clause catalogued under
	// more than one release, or a document that repeats it — come back TIED, and
	// the winner is then whatever physical order the table happens to be in.
	// `compact` rewrites that order.
	//
	// Measured on the corpus published 2026-09-10: of 3 433 (spec, clause) pairs
	// headed "Abbreviations" at their newest version, 41 carry a tie and 8 of those
	// have tied rows with DIFFERENT TEXT — enough to move the mined vocabulary. It
	// did: `seed-glossary` reported parsed=55778 before a compact and parsed=55753
	// after, on a corpus no ingest had touched between the two. Nothing failed. The
	// only visible effect was that a 23 GB corpus moved, so its image layer got a
	// new digest and the whole thing was pushed again.
	//
	// So the tie is broken HERE, by what each candidate YIELDS — see the paragraph
	// below for the rule and the two measured cases that chose it.
	//
	// It is fixed HERE rather than in GetClauses' ORDER BY because `enrich` declares
	// internal/glossaryseed and does NOT declare internal/store/store.go, where
	// GetClauses lives (only the glossary WRITE path, internal/store/acronyms_write.go,
	// is declared — see that file for why it stops there): a fix in GetClauses
	// would change what enrich produces without enrich replaying, which is the same
	// provenance hole #322 closed when it found enrich declaring four Rust binaries
	// and none of their manifests.
	//
	// WHAT IS COMPARED IS WHAT EACH CANDIDATE YIELDS, not where it sits.
	//
	// Ranking tied clauses by chunk id alone is deterministic and demonstrably
	// picks worse text. Measured on the corpus published 2026-09-10: for
	// 38.475 v0.3.0 §3.2 the lowest chunk is the clause's introduction, while its
	// tied sibling additionally defines gNB-CU, gNB-DU and gNB; for 33.802 v0.2.0
	// §3.3 the lowest chunk is an "<ACRONYM> <Explanation>" template and the
	// sibling holds real entries. So "there is no principled better text among
	// them" — which an earlier version of this comment asserted — was wrong. There
	// is: this function exists to mine abbreviations, and a clause that declares
	// more of them is more of what was asked for.
	//
	// Position still decides when the yields tie, and only then. That keeps the
	// order GetClauses established (`len(clause_path), clause_path`) and appends
	// the chunk id, so two runs over the same corpus cannot disagree.
	var (
		found     bool
		body      string
		path      string
		bestChunk uint64
		entries   []abbrev.Entry
	)
	for _, c := range clauses {
		if c.Version != best {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(c.Heading), "abbreviations") {
			continue
		}
		e := abbrev.Parse(c.Text)
		// `found`, NOT `body == ""`. Conflating "nothing chosen yet" with "chose an
		// empty clause" left the loop order-dependent exactly where the fix was
		// aimed: with an empty candidate at chunk 7 and a populated one at chunk 9,
		// the arrival order decided whether the spec was mined or skipped. 19 specs
		// carry an empty Abbreviations body at their newest version.
		if !found || betterCandidate(len(e), c.ClausePath, c.ChunkID, len(entries), path, bestChunk) {
			found, body, path, bestChunk, entries = true, c.Text, c.ClausePath, c.ChunkID, e
		}
	}
	if !found {
		sr.Skipped = "no clause headed \"Abbreviations\" in " + best
		return sr, nil, nil
	}
	if body == "" {
		sr.Skipped = "empty clause headed \"Abbreviations\" in " + best
		return sr, nil, nil
	}
	sr.Clause = path
	sr.Parsed = len(entries)
	return sr, entries, nil
}

// ts21905 is the vocabulary spec, whose rows the Rust ingest writes into the
// glossary stamped "21" (rust/parse/src/glossary.rs, GLOSSARY_SPEC_ID).
//
// THIS PACKAGE NEVER MINES IT — readSpec looks under clause 3 and TS 21.905's
// abbreviations are clause 4, so the sweep lists it and skips it, "no clause
// headed Abbreviations" — and that is right: its rows are its writer's, not
// seeded ones. What this package asks of it is narrower and comes from the
// replacement: before a seeded row is released, does TS 21.905 still declare its
// key? See readGeneral.
const ts21905 = "21.905"

// ts21905Min is the floor under which a read of TS 21.905 is treated as broken,
// and every release is withheld rather than decided on it.
//
// MEASURED 2026-09-11 on every version the corpus holds, with the rule
// readGeneral applies: the vocabulary has NEVER SHRUNK across its 16 stored
// versions — 963 pairs at v4.5.0 (Rel-4), 1 086 at v9.4.0, 1 242 from v10.3.0
// (Rel-10) onwards, 1 285 at v19.2.0. 1 200 is 93 % of today's figure and below
// every version from v10.3.0 (Rel-10) on, so an editorial change does not trip it;
// losing any one of the four largest letter clauses (S 128, C 126, P 102, M 97)
// does, and so does a read that finds the region and parses nothing.
//
// The floor errs HIGH on purpose, because its two failures do not cost the same.
// Tripping it on a sound read withholds releases: stale rows stay one more run,
// which is what the additive writer did for ever. Passing a broken read lets a
// partial set "clear" keys TS 21.905 does declare, and those rows are deleted.
const ts21905Min = 1200

// readGeneral reads what TS 21.905 declares NOW — the evidence a release is
// checked against — and says why when it cannot.
//
// WHAT IS READ: the newest version's region under the clause headed exactly
// "Abbreviations" (the miner's own heading test), which in TS 21.905 is not one
// clause but 28: "4 Abbreviations" with an empty body, then "0-9" and "A" to "Z"
// as UNNUMBERED clauses — clause_path "" — up to "5 Equations". Measured on all
// 16 stored versions: that shape, every time.
//
// DOCUMENT ORDER IS THE CHUNK ID, sorted here. GetClauses orders by
// `len(clause_path), clause_path`, which puts the 27 letter clauses — and the
// Contents and Foreword before clause 1 — in one tie that the table's physical
// order decides; readSpec paid for that tie once already. The chunk id is the
// clause's position in its document and survives compaction.
//
// WHAT IS PARSED: every clause of the region with internal/abbrev's rules —
// Parse AND ParseLines — so a key here has exactly the shape of the keys the
// seeded rows carry. Both, because the wrap rule reads TS 21.905 wrongly where a
// cell's text sits on its own untabbed line: measured on v19.2.0, Parse alone
// yields 1 264 pairs and misses 4 of the 896 seeded rows whose key TS 21.905's
// writer stored (SN, OSP, REQ, X2-U — see abbrev.ParseLines); with ParseLines
// the set is 1 285 pairs and misses none.
//
// THE UNION ERRS TOWARD SPARING A ROW, and that is its cost as well as its
// point. A pair only Parse yields can be one of its misreadings — "OSP = Octet
// Stream Protocol Service" — and a seeded row carrying exactly that key would be
// handed back rather than removed. None does: of the 905 seeded rows whose key
// is in the union, 896 carry a pair TS 21.905's writer stored and the other 9 a
// pair it prints but its writer could not read — a term with a space ("JAR
// file", "WLAN UE"), an ampersand ("O&M"), or a whole wrapped expansion where the
// writer kept the first line ("ADM = Access condition to an EF which is under
// the control of the authority which creates this file").
//
// A missing spec, a missing region or a set under ts21905Min comes back UNREAD —
// never as an empty set, which the store would read as "TS 21.905 declares none
// of these" and release every row on. Only a query error is returned as one.
func readGeneral(ctx context.Context, s *store.Store) (store.GeneralVocabulary, GeneralReport, error) {
	gr := GeneralReport{Spec: ts21905, Min: ts21905Min}
	clauses, err := s.GetClauses(ctx, ts21905, "", "")
	if err != nil {
		return store.GeneralVocabulary{}, gr, fmt.Errorf("%s: %w", ts21905, err)
	}
	if len(clauses) == 0 {
		gr.Unread = "not in this corpus"
		return store.GeneralVocabulary{}, gr, nil
	}
	region := generalRegion(clauses)
	gr.Version = region.version
	if len(region.clauses) == 0 {
		gr.Unread = "no clause headed \"Abbreviations\" in v" + region.version
		return store.GeneralVocabulary{}, gr, nil
	}
	gr.Release = region.clauses[0].Release
	pairs := map[store.GeneralPair]bool{}
	for _, c := range region.clauses {
		for _, e := range append(abbrev.Parse(c.Text), abbrev.ParseLines(c.Text)...) {
			pairs[store.GeneralPair{Term: e.Term, Expansion: e.Expansion}] = true
		}
	}
	gr.Pairs = len(pairs)
	if gr.Pairs < ts21905Min {
		gr.Unread = fmt.Sprintf("v%s yields %d pairs, below the floor of %d — a broken read, "+
			"not a smaller vocabulary", region.version, gr.Pairs, ts21905Min)
		return store.GeneralVocabulary{}, gr, nil
	}
	return store.GeneralVocabulary{Read: true, Pairs: pairs, Release: gr.Release}, gr, nil
}

// abbreviationsRegion is the part of one version of a spec its Abbreviations
// heading governs.
type abbreviationsRegion struct {
	version string
	clauses []model.Clause
}

// generalRegion returns the newest version's Abbreviations region, in document
// order: each clause headed "Abbreviations", and every clause after it that is
// unnumbered or numbered under it, up to the first numbered clause that is not.
//
// UNNUMBERED CLAUSES BELONG TO THE REGION, which is what TS 21.905's letter
// clauses need, and what the Rust rule no longer grants them: rust/parse's
// extract_acronyms stops at the first clause that is not a DESCENDANT, and ""
// is not a descendant of "4" (is_descendant), so under today's rule it reads
// "4 Abbreviations" — an empty body — and nothing after it. The rows stamped
// "21" in the corpus predate that rule; this reading does not depend on it.
//
// A version holding two documents yields both regions. Over-reading can only
// spare a row; under-reading is what deletes one.
func generalRegion(clauses []model.Clause) abbreviationsRegion {
	var r abbreviationsRegion
	for _, c := range clauses {
		if newerVersion(c.Version, r.version) {
			r.version = c.Version
		}
	}
	var doc []model.Clause
	for _, c := range clauses {
		if c.Version == r.version {
			doc = append(doc, c)
		}
	}
	sort.SliceStable(doc, func(i, j int) bool { return doc[i].ChunkID < doc[j].ChunkID })
	for i := 0; i < len(doc); i++ {
		if !strings.EqualFold(strings.TrimSpace(doc[i].Heading), "abbreviations") {
			continue
		}
		root := doc[i].ClausePath
		r.clauses = append(r.clauses, doc[i])
		for i+1 < len(doc) {
			p := doc[i+1].ClausePath
			if p != "" && (root == "" || !strings.HasPrefix(p, root+".")) {
				break
			}
			i++
			r.clauses = append(r.clauses, doc[i])
		}
	}
	return r
}

// earlier reports whether the clause at (pathA, chunkA) sorts before (pathB,
// chunkB) under the order readSpec needs.
//
// The first two keys are GetClauses' own — `len(clause_path), clause_path` — and
// they are repeated here rather than relied upon, because relying on them is what
// broke: an ORDER BY that leaves rows tied hands the decision to the physical
// order of the table, and `compact` rewrites that. The third key, the chunk id, is
// a stored value that compaction preserves, so it decides the tie the same way
// every time.
//
// Length FIRST is not decoration. Compared as plain strings "10.2" sorts before
// "3.2", so a spec with an Abbreviations clause under both would change which one
// seeds the glossary — a silent content change, in the middle of a fix for silent
// content changes.
func earlier(pathA string, chunkA uint64, pathB string, chunkB uint64) bool {
	// RUNES, NOT BYTES. GetClauses orders by SQL `length()`, which counts
	// CHARACTERS; Go's len() counts bytes. They agree on ASCII and part company on
	// anything else — "3.١" is three characters and four bytes, so SQL puts it
	// before "3.10" and a byte comparison puts it after. Measured 2026-09-10: zero
	// non-ASCII clause paths across 2 751 918 3GPP and 3 168 482 ETSI occurrences,
	// so this is not today's drift — but the salvage parser in rust/parse captures
	// `\d` , which Unicode digits satisfy, so it is reachable. Counting runes costs
	// nothing and removes the assumption instead of recording it.
	if a, b := utf8.RuneCountInString(pathA), utf8.RuneCountInString(pathB); a != b {
		return a < b
	}
	if pathA != pathB {
		return pathA < pathB
	}
	return chunkA < chunkB
}

// betterCandidate reports whether candidate A should displace candidate B as the
// clause a spec's glossary is mined from.
//
// YIELD FIRST, POSITION SECOND. See readSpec for the two measured cases where the
// positionally-first clause is the poorer one. Position remains the tiebreak, and
// remains total, so the choice is still the same on every run over the same corpus.
func betterCandidate(entriesA int, pathA string, chunkA uint64, entriesB int, pathB string, chunkB uint64) bool {
	if entriesA != entriesB {
		return entriesA > entriesB
	}
	return earlier(pathA, chunkA, pathB, chunkB)
}

// newerVersion reports whether a is a higher dotted-numeric version than b.
//
// NUMERIC, not lexical: lexical max is wrong the moment a spec reaches double
// digits, because "9.5.0" sorts above "20.2.0" as a string and the glossary
// would be seeded from a decade-old release.
//
// Version alone is enough, and that is a measured claim rather than an
// assumption: in 3GPP the version's MAJOR NUMBER IS THE RELEASE. Checked
// 2026-09-05 across all six default specs and every release they hold — 23.401
// Rel-8 at 8.18.0 through Rel-20 at 20.0.0, 23.501 Rel-15 at 15.13.0 through
// Rel-20 at 20.2.0 — without an exception. Ordering by release first would need
// a release-to-ordinal map to answer what these digits already answer.
func newerVersion(a, b string) bool {
	if b == "" {
		return a != ""
	}
	av, bv := parts(a), parts(b)
	for i := 0; i < len(av) || i < len(bv); i++ {
		x, y := 0, 0
		if i < len(av) {
			x = av[i]
		}
		if i < len(bv) {
			y = bv[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func parts(v string) []int {
	var out []int
	for _, f := range strings.Split(v, ".") {
		n, err := strconv.Atoi(strings.TrimFunc(f, func(r rune) bool { return r < '0' || r > '9' }))
		if err != nil {
			n = 0
		}
		out = append(out, n)
	}
	return out
}
