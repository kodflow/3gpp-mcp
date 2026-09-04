// Package evolcheck verifies the curated NE->NF evolution seed against the corpus
// it is about to be written into.
//
// The seed's value is that a reader can follow each edge to a clause and see it
// justified. An edge whose clause does not exist, or exists but never names the
// network function it is cited for, LOOKS checkable and is not — which is
// strictly worse than no citation, and is exactly the defect the current
// forty-five-edge seed was written to fix (the previous one pointed PCRF->PCF at
// "Data Storage architectures" and eNB->gNB at "Service-based interfaces").
//
// It lives here rather than in cmd/seed-evolutions because cmd binaries are thin
// CLIs that wire internal packages together (cmd/CLAUDE.md); the checking is
// behaviour, and behaviour with its own tests belongs in a package that other
// callers — a future gate, a report — can reuse.
package evolcheck

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// ClauseSource is the corpus surface the check needs: resolve a spec's latest
// version, then read a clause out of it. store.Reader satisfies it.
type ClauseSource interface {
	LatestVersion(ctx context.Context, specID string) (release, version string, ok bool, err error)
	GetClauses(ctx context.Context, specID, version, clausePrefix string) ([]model.Clause, error)
}

// Result separates the two failure modes on purpose, because they are graded
// differently: a MISSING clause means nothing in the corpus backs the claim,
// while a clause that exists but does not name its target is a strong smell —
// a clause can legitimately describe a function without spelling its acronym.
type Result struct {
	Seed    int               `json:"seed"`
	Missing []model.Evolution `json:"missing_clause"`
	Unnamed []model.Evolution `json:"clause_does_not_name_target"`
}

// OK reports whether the seed may be written without --strict.
func (r Result) OK() bool { return len(r.Missing) == 0 }

// Verify resolves each cited clause once per (spec, clause) and reports both
// failure modes. Clauses are fetched at the spec's LATEST version: the seed
// describes the architecture as it now stands, so a clause that only existed in
// an older release is genuinely a dead citation for this corpus.
func Verify(ctx context.Context, src ClauseSource, seed []model.Evolution) (Result, error) {
	res := Result{Seed: len(seed)}

	type key struct{ spec, clause string }
	texts := map[key]string{}
	resolved := map[key]bool{}

	for _, e := range seed {
		k := key{e.JustificationSpec, e.JustificationClause}
		if !resolved[k] {
			resolved[k] = true
			_, ver, ok, err := src.LatestVersion(ctx, k.spec)
			if err != nil {
				return res, err
			}
			if ok {
				cls, cerr := src.GetClauses(ctx, k.spec, ver, k.clause)
				if cerr != nil {
					return res, cerr
				}
				// GetClauses takes a PREFIX, so "6.2.3" also returns "6.2.30".
				// Only the exact clause counts as the anchor.
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
			res.Missing = append(res.Missing, e)
		case !NamesTerm(body, e.ToTerm):
			res.Unnamed = append(res.Unnamed, e)
		}
	}
	sort.Slice(res.Missing, func(i, j int) bool { return less(res.Missing[i], res.Missing[j]) })
	sort.Slice(res.Unnamed, func(i, j int) bool { return less(res.Unnamed[i], res.Unnamed[j]) })
	return res, nil
}

// NamesTerm reports whether body mentions term as a WHOLE token, a token being a
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
// good citations. Getting this asymmetric (a boundary on the left, part of the
// token on the right) is what the unit test caught.
//
// Terms containing a space ("TSN AF", "5G DDNMF") match with their internal
// whitespace relaxed, since the extracted text may have broken the line between
// the two words.
func NamesTerm(body, term string) bool {
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

// Describe renders an edge readably, giving the "new in 5G" functions — which
// carry no predecessor — a name instead of an empty string.
func Describe(e model.Evolution) string {
	from := strings.TrimSpace(e.FromTerm)
	if from == "" {
		from = "(new in 5G)"
	}
	return from + " -> " + e.ToTerm
}

func less(a, b model.Evolution) bool {
	if a.FromTerm != b.FromTerm {
		return a.FromTerm < b.FromTerm
	}
	return a.ToTerm < b.ToTerm
}
