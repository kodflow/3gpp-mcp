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
// It is not "every spec". A corpus-wide sweep would read 3 568 Abbreviations
// clauses and let an obscure study item outrank an architecture spec on a term
// they spell differently — the same class of defect being repaired here, just
// with a different loser. These six are where the vocabulary is DECLARED.
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
	Specs   []SpecReport `json:"specs"`
	Parsed  int          `json:"parsed_total"`
	Written int          `json:"written_total"`
	Min     int          `json:"min_required"`
	Applied bool         `json:"applied"`
	OK      bool         `json:"ok"`
	Error   string       `json:"error,omitempty"`
}

// Run seeds the glossary from the named specs' Abbreviations clauses.
func Run(ctx context.Context, path string, specIDs []string, min int, checkOnly bool) (Report, error) {
	// Applied stays FALSE until the write actually lands. Setting it from
	// checkOnly up front makes a failed run report applied=true, which is the
	// one field a caller reads to decide whether the corpus changed.
	rep := Report{Min: min}

	s, err := store.Open(path)
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
	type pending struct {
		sr      SpecReport
		entries []abbrev.Entry
	}
	var todo []pending
	for _, id := range specIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		sr, entries, err := readSpec(ctx, s, id)
		if err != nil {
			return rep, err
		}
		todo = append(todo, pending{sr, entries})
		rep.Parsed += sr.Parsed
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
	if rep.Parsed < min {
		return rep, fmt.Errorf("parsed only %d abbreviations across %s, expected at least %d — "+
			"the Abbreviations clause was probably not found or not recognised",
			rep.Parsed, strings.Join(specIDs, ","), min)
	}

	if !checkOnly {
		// ONE TRANSACTION for the whole batch. Collecting before writing removes
		// the partial write a failed FLOOR would leave; it does nothing about a
		// failure on row 400 of 679, which would leave 399 high-precedence rows
		// behind just the same. All of them land or none do.
		var rows []model.Acronym
		for i := range todo {
			for _, e := range todo[i].entries {
				rows = append(rows, model.Acronym{
					Term:      e.Term,
					Expansion: e.Expansion,
					// Domain stays empty on purpose. The clause declares an
					// abbreviation, not which architecture owns it, and stamping
					// "5GC" on all 221 rows of 23.501 §3.2 would assert something
					// the source never said — 5G LAN and QoS live there too.
					Domain:       "",
					FirstRelease: todo[i].sr.Version,
					LastRelease:  todo[i].sr.Version,
					// The owning SPEC, not its two-digit series: it is what makes
					// the precedence above auditable, and nothing consumes the
					// series form.
					SourceSeries: todo[i].sr.Spec,
				})
			}
			todo[i].sr.Written = len(todo[i].entries)
		}
		if err := s.UpsertAcronyms(rows); err != nil {
			return rep, err
		}
		rep.Applied = true
	}
	for i := range todo {
		rep.Specs = append(rep.Specs, todo[i].sr)
		rep.Written += todo[i].sr.Written
	}
	rep.OK = true
	return rep, nil
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
	var body, path string
	for _, c := range clauses {
		if c.Version != best {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(c.Heading), "abbreviations") {
			body, path = c.Text, c.ClausePath
			break
		}
	}
	if body == "" {
		sr.Skipped = "no clause headed \"Abbreviations\" in " + best
		return sr, nil, nil
	}
	sr.Clause = path

	entries := abbrev.Parse(body)
	sr.Parsed = len(entries)
	return sr, entries, nil
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
