package li

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// X2NFEvents is the set of Lawful-Interception events a single NE/NF reports
// over LI_X2 (xIRI from the IRI-POI in the NF to the MDF2), per TS 33.128
// clause 6. The clause-heuristic complement of the authoritative ASN.1 registry,
// kept for reproducible analysis from get_spec JSON.
type X2NFEvents struct {
	NF         string         `json:"nf"`
	NFSection  string         `json:"nf_section"`
	X2Section  string         `json:"x2_section"`
	X2Heading  string         `json:"x2_heading"`
	Generation string         `json:"generation"`
	Events     []model.Clause `json:"events"`
	Count      int            `json:"count"`
	Citation   model.Citation `json:"citation"`
}

var (
	reX2gen    = regexp.MustCompile(`(?i)generation of xiri.*over\s+li_?x2`)
	reX2Boiler = regexp.MustCompile(`(?i)^(general|void|introduction|common.*|.*simple data types.*|definitions? for .*|.*message types?)$`)
	reX2NFcand = regexp.MustCompile(`(?i)\b(?:at|in)\s+([A-Za-z][A-Za-z0-9/]{1,9})`)
	x2Stop     = map[string]bool{"IRI": true, "POI": true, "LI": true, "CC": true, "TF": true, "THE": true, "IRI-POI": true}
)

// ExtractLIX2Events is the pure core: given the clause-6 clauses of a TS 33.128
// document, return the per-NF X2->MDF2 event breakdown. Reproducible end-to-end
// (a client can run it on get_spec JSON). For each "Generation of xIRI over
// LI_X2" clause, an event is a non-boilerplate direct child sub-clause.
func ExtractLIX2Events(clauses []model.Clause) []X2NFEvents {
	head := make(map[string]string, len(clauses))
	for _, c := range clauses {
		head[c.ClausePath] = c.Heading
	}
	var out []X2NFEvents
	for _, c := range clauses {
		if !reX2gen.MatchString(c.Heading) {
			continue
		}
		ancestor := depth3(c.ClausePath)
		nf := x2NFFromHeading(c.Heading)
		if nf == "" {
			nf = trimLI(head[ancestor])
		}
		var events []model.Clause
		for _, child := range clauses {
			if isDirectChild(c.ClausePath, child.ClausePath) &&
				!reX2Boiler.MatchString(strings.TrimSpace(child.Heading)) {
				events = append(events, child)
			}
		}
		out = append(out, X2NFEvents{
			NF:         nf,
			NFSection:  ancestor,
			X2Section:  c.ClausePath,
			X2Heading:  c.Heading,
			Generation: generationOf(c.ClausePath, head),
			Events:     events,
			Count:      len(events),
			Citation:   c.Cite(),
		})
	}
	return out
}

// X2Events fetches TS 33.128 clause 6 from the store and extracts events.
// specID defaults to "33.128"; an empty version resolves to the latest.
func X2Events(ctx context.Context, st *store.Store, specID, version string) ([]X2NFEvents, string, error) {
	if specID == "" {
		specID = "33.128"
	}
	if version == "" {
		_, v, ok, err := st.LatestVersion(ctx, specID)
		if err != nil {
			return nil, "", err
		}
		if !ok {
			return nil, "", fmt.Errorf("spec %s not in corpus", specID)
		}
		version = v
	}
	clauses, err := st.GetClauses(ctx, specID, version, "6")
	if err != nil {
		return nil, version, err
	}
	return ExtractLIX2Events(clauses), version, nil
}

func x2NFFromHeading(h string) string {
	matches := reX2NFcand.FindAllStringSubmatch(h, -1)
	for i := len(matches) - 1; i >= 0; i-- { // last candidate wins ("in SMF" over "at IRI-POI")
		cand := strings.ToUpper(matches[i][1])
		if !x2Stop[cand] {
			return cand
		}
	}
	return ""
}

func generationOf(path string, head map[string]string) string {
	parts := strings.Split(path, ".")
	if len(parts) < 2 {
		return ""
	}
	switch head[strings.Join(parts[:2], ".")] {
	case "5G":
		return "5G"
	case "4G":
		return "4G"
	case "3G":
		return "3G"
	}
	return ""
}
