package eval

import (
	"context"
	"encoding/json"
	"os"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// Query is one eval entry: a free-text query with known-relevant clause refs.
type Query struct {
	ID       string     `json:"id"`
	Query    string     `json:"query"`
	Intent   string     `json:"intent,omitempty"`
	Domain   string     `json:"domain,omitempty"`
	Tags     []string   `json:"tags,omitempty"`
	Relevant []Relevant `json:"relevant"`
	Release  string     `json:"release,omitempty"`
	Notes    string     `json:"notes,omitempty"`
}

// Set is a query set.
type Set []Query

// Load reads a query set from a JSON file.
func Load(path string) (Set, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Set
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return s, nil
}

// Ranker runs one retrieval system for a query and returns ranked clause refs
// (best-first). Different systems (lexical / dense / hybrid / +rerank) implement
// this so the harness scores them through the identical metric path.
type Ranker func(ctx context.Context, q Query) ([]Ref, error)

// HitsToRefs projects search hits to (spec, clause) refs.
func HitsToRefs(hits []model.SearchHit) []Ref {
	out := make([]Ref, len(hits))
	for i, h := range hits {
		out[i] = Ref{SpecID: h.Clause.SpecID, Clause: h.Clause.ClausePath}
	}
	return out
}

// Run executes a ranker over the whole set and returns per-query metrics plus
// the macro average.
func Run(ctx context.Context, set Set, rank Ranker) ([]Metrics, Metrics, error) {
	per := make([]Metrics, 0, len(set))
	for _, q := range set {
		ranked, err := rank(ctx, q)
		if err != nil {
			return nil, Metrics{}, err
		}
		per = append(per, Score(ranked, q.Relevant))
	}
	return per, Mean(per), nil
}
