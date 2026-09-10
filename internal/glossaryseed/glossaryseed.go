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
	// rows it took out because no spec declares them any more — and, on
	// --check-only, what the write WOULD do. The two together are exactly what
	// Changed summarises, so a check-only run answers the push question before
	// anything is written.
	Rewritten int `json:"rewritten_total"`
	Removed   int `json:"removed_total"`
	// RemovedRows names every removed row. A deletion nobody can list is the
	// silent failure this package keeps refusing: the count alone would say that
	// the glossary shrank, never what a reader can no longer find.
	RemovedRows []RemovedRow `json:"removed,omitempty"`
	OK          bool         `json:"ok"`
	Error       string       `json:"error,omitempty"`
}

// RemovedRow is one seeded row a run took out of the glossary.
type RemovedRow struct {
	Term      string `json:"term"`
	Expansion string `json:"expansion"`
	Source    string `json:"source"`
}

// Run seeds the glossary from the named specs' Abbreviations clauses.
func Run(ctx context.Context, path string, specIDs []string, min int, checkOnly bool) (Report, error) {
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
				// belongs to TS 21.905's rows, which this package never removes.
				SourceSeries: todo[i].sr.Spec,
			})
		}
	}

	var diff store.GlossaryDiff
	if checkOnly {
		if diff, err = s.PlanSeededAcronyms(rows); err != nil {
			return rep, err
		}
	} else {
		for i := range todo {
			todo[i].sr.Written = len(todo[i].entries)
		}
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
		if diff, err = s.ReplaceSeededAcronyms(rows); err != nil {
			return rep, err
		}
		rep.Applied = true
		rep.Changed = diff.Changed()
	}
	rep.Rewritten = diff.Written
	rep.Removed = len(diff.Removed)
	for _, a := range diff.Removed {
		rep.RemovedRows = append(rep.RemovedRows, RemovedRow{a.Term, a.Expansion, a.SourceSeries})
	}
	for i := range todo {
		rep.Specs = append(rep.Specs, todo[i].sr)
		rep.Written += todo[i].sr.Written
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
