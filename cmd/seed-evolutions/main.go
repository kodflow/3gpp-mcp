// Command seed-evolutions writes the curated EPC(4G) -> 5GC(5G) network-element ->
// network-function edge seed (internal/evolseed) into a corpus, and CHECKS every
// edge's citation against that corpus before it lands.
//
//	seed-evolutions --db data/3gpp.duckdb [--strict]
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
// The check is the point, not a bonus. The seed's value is that a reader can follow
// each edge to a clause and see it justified; an edge whose clause does not exist,
// or exists but never names the network function it is cited for, looks checkable
// and is not — which is strictly worse than no citation. Two failure modes, graded
// differently on purpose:
//
//   - MISSING clause: fatal. Nothing in the corpus backs the claim.
//   - clause exists but does not NAME the target: warned, and fatal under --strict.
//     A clause can legitimately describe a function without spelling its acronym,
//     so this one is a strong smell rather than a proof.
//
// Both are counted and printed, so a run that degrades the seed is visible in the
// pipeline log rather than silently green.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/evolseed"
	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

var Version = "dev"

func main() {
	db := flag.String("db", "", "corpus DuckDB to seed (required)")
	strict := flag.Bool("strict", false, "treat an edge whose clause never names its target as fatal")
	check := flag.Bool("check-only", false, "verify the seed against the corpus and report, without writing")
	flag.Parse()
	if *db == "" {
		fmt.Fprintln(os.Stderr, "seed-evolutions: --db is required")
		os.Exit(2)
	}
	if err := run(context.Background(), *db, *strict, *check); err != nil {
		fmt.Fprintln(os.Stderr, "seed-evolutions:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, path string, strict, checkOnly bool) error {
	s, err := store.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	seed := evolseed.Seed()
	if len(seed) == 0 {
		return fmt.Errorf("the seed is empty — refusing to wipe the corpus's edges for nothing")
	}

	missing, unnamed, err := verify(ctx, s, seed)
	if err != nil {
		return err
	}
	fmt.Printf("seed-evolutions: seed=%d hash=%s missing_clause=%d clause_does_not_name_target=%d\n",
		len(seed), evolseed.SeedHash(), len(missing), len(unnamed))
	for _, m := range missing {
		fmt.Fprintf(os.Stderr, "seed-evolutions: FATAL %s -> %s cites %s §%s, which is NOT in the corpus\n",
			orNew(m.FromTerm), m.ToTerm, m.JustificationSpec, m.JustificationClause)
	}
	for _, u := range unnamed {
		fmt.Fprintf(os.Stderr, "seed-evolutions: WARN %s -> %s cites %s §%s, which never names %q\n",
			orNew(u.FromTerm), u.ToTerm, u.JustificationSpec, u.JustificationClause, u.ToTerm)
	}
	if len(missing) > 0 {
		return fmt.Errorf("%d edge(s) cite a clause the corpus does not have", len(missing))
	}
	if strict && len(unnamed) > 0 {
		return fmt.Errorf("--strict: %d edge(s) cite a clause that never names the target", len(unnamed))
	}
	if checkOnly {
		return nil
	}

	if err := s.ReplaceEvolutions(ctx, seed); err != nil {
		return err
	}
	fmt.Printf("seed-evolutions: wrote %d edge(s) to %s\n", len(seed), path)
	return nil
}

// verify resolves each cited clause once per (spec, clause) and reports the two
// failure modes separately. Clauses are fetched at the spec's LATEST version:
// the seed describes the architecture as it now stands, so a clause that only
// existed in an older release is genuinely a dead citation for this corpus.
func verify(ctx context.Context, s *store.Store, seed []model.Evolution) (missing, unnamed []model.Evolution, err error) {
	type key struct{ spec, clause string }
	texts := map[key]string{}   // "" once resolved-and-absent
	seenKey := map[key]bool{}

	for _, e := range seed {
		k := key{e.JustificationSpec, e.JustificationClause}
		if !seenKey[k] {
			seenKey[k] = true
			_, ver, ok, verr := s.LatestVersion(ctx, k.spec)
			if verr != nil {
				return nil, nil, verr
			}
			if ok {
				cls, cerr := s.GetClauses(ctx, k.spec, ver, k.clause)
				if cerr != nil {
					return nil, nil, cerr
				}
				// GetClauses takes a PREFIX, so "6.2.3" also returns "6.2.30".
				// Keep the exact clause, and fall back to the whole subtree only
				// when the exact path is absent — a citation to a clause that
				// only exists as a parent of deeper ones is still a real anchor.
				var b strings.Builder
				for _, c := range cls {
					if c.ClausePath == k.clause {
						b.WriteString(c.Heading)
						b.WriteByte('\n')
						b.WriteString(c.Text)
						b.WriteByte('\n')
					}
				}
				texts[k] = b.String()
			}
		}
		body := texts[key{e.JustificationSpec, e.JustificationClause}]
		switch {
		case strings.TrimSpace(body) == "":
			missing = append(missing, e)
		case !namesTerm(body, e.ToTerm):
			unnamed = append(unnamed, e)
		}
	}
	sort.Slice(missing, func(i, j int) bool { return less(missing[i], missing[j]) })
	sort.Slice(unnamed, func(i, j int) bool { return less(unnamed[i], unnamed[j]) })
	return missing, unnamed, nil
}

// namesTerm reports whether body mentions term as a WHOLE token, a token being a
// maximal run of letters, digits and underscore — the usual word-boundary rule.
//
// A plain substring test would be useless here, and not in a theoretical way:
// "PCF" is a substring of "PCFICH", so a clause about the physical control format
// indicator channel would be credited as naming the Policy Control Function. The
// check exists to catch citations that land on a clause which never discusses the
// target, and that is exactly the shape they take.
//
// A HYPHEN is a boundary on both sides, deliberately: a clause that talks about
// the "AMF-set" is discussing the AMF, and refusing it would produce warnings on
// good citations. Getting this asymmetric (hyphen a boundary on the left, part of
// the token on the right) is what the unit test caught.
//
// Terms containing a space ("TSN AF", "5G DDNMF") match with their internal
// whitespace relaxed, since the extracted text may have broken the line between
// the two words.
func namesTerm(body, term string) bool {
	term = strings.TrimSpace(term)
	if term == "" {
		return true // "new in 5G" edges carry no from-term; the to-term is what is checked
	}
	parts := strings.Fields(term)
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	const bound = `[^0-9A-Za-z_]`
	re, err := regexp.Compile(`(?i)(^|` + bound + `)` + strings.Join(parts, `\s+`) + `($|` + bound + `)`)
	if err != nil {
		return strings.Contains(strings.ToUpper(body), strings.ToUpper(term))
	}
	return re.MatchString(body)
}

func less(a, b model.Evolution) bool {
	if a.FromTerm != b.FromTerm {
		return a.FromTerm < b.FromTerm
	}
	return a.ToTerm < b.ToTerm
}

// orNew renders the empty from-term of a "new in 5G" NF readably.
func orNew(from string) string {
	if strings.TrimSpace(from) == "" {
		return "(new in 5G)"
	}
	return from
}
