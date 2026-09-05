// Command seed-glossary writes the abbreviations a spec declares about itself
// into the corpus glossary, so resolve_term can answer from the document that
// defines the term instead of from whatever else spelled it the same way.
//
// Usage:
//
//	seed-glossary --db data/3gpp.duckdb [--specs 23.501,23.401,...]
//	              [--min 150] [--check-only] [--report json]
//
// WHAT IT REPAIRS. Measured 2026-09-05 on the shipped corpus: of the 30 main 5GC
// network functions, resolve_term was right about two, wrong about nine, and
// silent about nineteen. The 3GPP half's glossary came only from TS 21.905,
// which does not name them; the ETSI half supplied "ATM Mapping Function" for
// AMF because in that corpus it is one.
//
// The fix is not a list of network functions typed out by hand — that is correct
// once and stale from the next release, with nothing to say so. It reads the
// Abbreviations clause each spec already carries: 221 rows in TS 23.501 §3.2,
// every 5GC function among them, and it keeps working when a release adds the
// next one.
//
// PRECEDENCE IS THE SPEC'S OWN RULE, not a preference invented here. TS 23.501
// §3.2 opens: "An abbreviation defined in the present document takes precedence
// over the definition of the same abbreviation, if any, in TR 21.905 [1]."
// Store.ResolveTerm ranks these rows above the general vocabulary for that
// reason and no other.
//
// It is additive and idempotent: rows are upserted on (term, expansion, domain),
// so re-running changes nothing and the existing 21.905 and ETSI entries are
// left in place. A term with two meanings keeps both; what changes is which one
// a reader is shown first.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/abbrev"
	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// defaultSpecs are the specs that DEFINE the system vocabulary this corpus is
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
// with a different loser. These six are where the vocabulary is DECLARED; the
// flag exists for anyone who needs another.
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
// declaring a term no spec declares, which is the failure mode this command was
// written to end.
const defaultSpecs = "23.501,23.401,24.501,33.501,23.548,23.682"

// specReport is what one spec contributed.
type specReport struct {
	Spec    string `json:"spec"`
	Version string `json:"version"`
	Clause  string `json:"clause"`
	Parsed  int    `json:"parsed"`
	Written int    `json:"written"`
	Skipped string `json:"skipped,omitempty"`
}

type report struct {
	Specs   []specReport `json:"specs"`
	Parsed  int          `json:"parsed_total"`
	Written int          `json:"written_total"`
	Min     int          `json:"min_required"`
	Applied bool         `json:"applied"`
	OK      bool         `json:"ok"`
	Error   string       `json:"error,omitempty"`
}

func main() {
	db := flag.String("db", "", "path to the corpus DuckDB")
	specs := flag.String("specs", defaultSpecs, "comma-separated spec ids whose Abbreviations clause to read")
	min := flag.Int("min", 150, "fail if fewer than this many abbreviations are parsed in total")
	checkOnly := flag.Bool("check-only", false, "parse and report, write nothing")
	format := flag.String("report", "text", "text | json")
	flag.Parse()
	if *db == "" {
		fmt.Fprintln(os.Stderr, "seed-glossary: --db is required")
		os.Exit(2)
	}

	rep, err := run(context.Background(), *db, strings.Split(*specs, ","), *min, *checkOnly)
	if err != nil {
		rep.Error = err.Error()
	}
	emit(rep, *format)
	if err != nil {
		os.Exit(1)
	}
}

func run(ctx context.Context, path string, specIDs []string, min int, checkOnly bool) (report, error) {
	rep := report{Min: min, Applied: !checkOnly}

	s, err := store.Open(path)
	if err != nil {
		return rep, err
	}
	defer func() { _ = s.Close() }()

	for _, id := range specIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		sr, entries, err := readSpec(ctx, s, id)
		if err != nil {
			return rep, err
		}
		if !checkOnly {
			for _, e := range entries {
				if err := s.UpsertAcronym(model.Acronym{
					Term:      e.Term,
					Expansion: e.Expansion,
					// Domain stays empty on purpose. The clause declares an
					// abbreviation, not which architecture owns it, and
					// stamping "5GC" on all 221 rows of 23.501 §3.2 would
					// assert something the source never said — 5G LAN and QoS
					// live there too.
					Domain:       "",
					FirstRelease: sr.Version,
					LastRelease:  sr.Version,
					// The owning SPEC, not its two-digit series: it is what
					// makes the precedence above auditable, and nothing
					// consumes the series form.
					SourceSeries: sr.Spec,
				}); err != nil {
					return rep, fmt.Errorf("%s %s: %w", id, e.Term, err)
				}
				sr.Written++
			}
		}
		rep.Specs = append(rep.Specs, sr)
		rep.Parsed += sr.Parsed
		rep.Written += sr.Written
	}

	// THE FLOOR. Every failure this command exists to prevent is silent: the
	// clause moves, the heading is spelled differently, the parse yields three
	// rows instead of hundreds — and a run that seeded almost nothing reports
	// success just as loudly as one that worked. A corpus that holds these specs
	// has hundreds of abbreviations; anything far below that is a broken read,
	// not a small vocabulary.
	if rep.Parsed < min {
		return rep, fmt.Errorf("parsed only %d abbreviations across %s, expected at least %d — "+
			"the Abbreviations clause was probably not found or not recognised",
			rep.Parsed, strings.Join(specIDs, ","), min)
	}
	rep.OK = true
	return rep, nil
}

// readSpec finds a spec's newest version, locates its Abbreviations clause and
// parses it.
func readSpec(ctx context.Context, s *store.Store, specID string) (specReport, []abbrev.Entry, error) {
	sr := specReport{Spec: specID}

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

	// Newest version, compared NUMERICALLY. Lexical max is wrong the moment a
	// spec reaches double digits: "9.5.0" sorts above "20.2.0" as a string, so
	// the glossary would be seeded from a decade-old release.
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

func emit(rep report, format string) {
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
	fmt.Printf("seed-glossary: parsed=%d written=%d (floor %d)\n", rep.Parsed, rep.Written, rep.Min)
}
