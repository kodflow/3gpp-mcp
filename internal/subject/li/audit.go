package li

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/store"
)

// SentinelEvent is one row of the external R17 oracle (docs/inputs/...).
type SentinelEvent struct {
	NF     string `json:"nf"`
	Event  string `json:"event"`
	Spec   string `json:"spec"`
	Clause string `json:"clause"`
	Alias  bool   `json:"alias"`
}

// Verdicts for an audited event.
const (
	VConfirmed = "CONFIRMED"           // cited clause exists AND name supported by its text
	VParentRef = "REAL_PARENT_REF"     // cited clause number synthetic, parent clause supports it
	VInCited   = "FOUND_IN_CITED_SPEC" // name present in cited spec, clause ref imprecise
	VWrongSpec = "WRONG_SPEC_REF"      // absent from cited spec, located in ANOTHER indexed spec
	VNotFound  = "NOT_FOUND"           // no trace anywhere (candidate hallucination)
)

// Finding is the per-event audit result. When Verdict==WRONG_SPEC_REF, the
// Real* fields give the event's true normative home (e.g. TS 29.002 §8.10.3).
type Finding struct {
	NF          string  `json:"nf"`
	Event       string  `json:"event"`
	Alias       bool    `json:"alias"`
	CitedSpec   string  `json:"cited_spec"`
	CitedClause string  `json:"cited_clause"`
	Verdict     string  `json:"verdict"`
	RealSpec    string  `json:"real_spec,omitempty"`
	RealClause  string  `json:"real_clause,omitempty"`
	RealHeading string  `json:"real_heading,omitempty"`
	Score       float64 `json:"score"`
}

// knownHome maps event names whose true normative home was verified by hand
// (the keyword heuristic mis-classifies them because their common-word tokens
// co-locate by accident in the cited spec's large annexes). Each entry is a
// real, indexed clause confirmed this session.
var knownHome = map[string]struct{ spec, clause, heading string }{
	"RESTORE_DATA":         {"29.002", "8.10.3", "MAP_RESTORE_DATA service"},
	"REGISTRATION_REFRESH": {"29.228", "6.1.2", "S-CSCF registration/deregistration notification (Cx Server-Assignment-Type RE_REGISTRATION)"},
}

var auditStop = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "req": true, "info": true,
	"of": true, "to": true, "in": true, "an": true, "intcpt": true, "ms": true, "start": true,
}

func auditNorm(s string) string { return strings.ReplaceAll(strings.ToLower(s), "-", "") }

var reAuditTok = regexp.MustCompile(`[^a-z0-9]+`)

func auditTokens(name string) []string {
	var out []string
	for _, p := range reAuditTok.Split(strings.ToLower(name), -1) {
		if len(p) >= 3 && !auditStop[p] {
			out = append(out, strings.ReplaceAll(p, "-", ""))
		}
	}
	return out
}

func fracIn(tokens []string, text string) float64 {
	if len(tokens) == 0 {
		return 0
	}
	n := 0
	for _, t := range tokens {
		if strings.Contains(text, t) {
			n++
		}
	}
	return float64(n) / float64(len(tokens))
}

func parentClause(p string) string {
	if i := strings.LastIndex(p, "."); i >= 0 {
		return p[:i]
	}
	return ""
}

// AuditCatalog audits every event against the normative text. The key
// discriminator is CO-LOCATION: the event-name tokens must appear together in a
// single clause, not merely somewhere in the spec (otherwise common words like
// "restore"/"data" match by accident). It checks the cited clause, its parent,
// then the best-matching clause WITHIN the cited spec; when no clause co-locates
// the tokens it searches the WHOLE index to relocate the event to its true spec
// — turning a bare "suspect" into "WRONG_SPEC_REF, real home = X §Y".
func AuditCatalog(ctx context.Context, st *store.Store, events []SentinelEvent) ([]Finding, error) {
	type idx struct{ byPath map[string]string }
	cache := map[string]idx{}
	loadSpec := func(spec string) idx {
		if si, ok := cache[spec]; ok {
			return si
		}
		si := idx{byPath: map[string]string{}}
		if _, v, ok, _ := st.LatestVersion(ctx, spec); ok {
			if cs, err := st.GetClauses(ctx, spec, v, ""); err == nil {
				for _, c := range cs {
					si.byPath[c.ClausePath] = auditNorm(c.Heading + " " + c.Text)
				}
			}
		}
		cache[spec] = si
		return si
	}

	out := make([]Finding, 0, len(events))
	for _, e := range events {
		tk := auditTokens(e.Event)
		f := Finding{NF: e.NF, Event: e.Event, Alias: e.Alias, CitedSpec: e.Spec, CitedClause: e.Clause}
		// Manually-verified cross-spec homes for operations the keyword heuristic
		// cannot disambiguate (common-word names whose tokens co-locate by accident
		// in the cited spec's annexes). Transparent, cited overrides — same spirit
		// as the evolutions seed. Verified against the index in this session.
		if kh, ok := knownHome[e.Event]; ok {
			f.Verdict, f.RealSpec, f.RealClause, f.RealHeading, f.Score = VWrongSpec, kh.spec, kh.clause, kh.heading, 1
			out = append(out, f)
			continue
		}
		si := loadSpec(e.Spec)
		switch {
		case fracIn(tk, si.byPath[e.Clause]) >= 0.5:
			f.Verdict, f.Score = VConfirmed, 1
		case e.Clause != "" && fracIn(tk, si.byPath[parentClause(e.Clause)]) >= 0.5:
			f.Verdict, f.Score = VParentRef, 0.9
		default:
			// Co-located match somewhere in the cited spec? (BM25-ranked clause.)
			if _, _, sc := bestHit(ctx, st, e.Event, tk, e.Spec); sc >= 0.75 {
				f.Verdict, f.Score = VInCited, sc
			} else if rs, rc, rh, sc := relocate(ctx, st, tk, e.Event, e.Spec); sc >= 0.66 {
				f.Verdict, f.RealSpec, f.RealClause, f.RealHeading, f.Score = VWrongSpec, rs, rc, rh, sc
			} else {
				f.Verdict, f.Score = VNotFound, sc
			}
		}
		out = append(out, f)
	}
	return out, nil
}

// bestHit returns the best token-coverage clause for name, optionally scoped to
// onlySpec (empty = whole index). It ranks via the store's lexical search then
// re-scores each hit by event-token co-location.
func bestHit(ctx context.Context, st *store.Store, name string, tk []string, onlySpec string) (string, string, float64) {
	hits, err := st.SearchClauses(ctx, store.SearchQuery{Text: name, Filter: store.SpecFilter{SpecID: onlySpec}, TopK: 8})
	if err != nil {
		return "", "", 0
	}
	best := 0.0
	var bc, bh string
	for _, h := range hits {
		if score := fracIn(tk, auditNorm(h.Clause.Heading+" "+h.Clause.Text)); score > best {
			best, bc, bh = score, h.Clause.ClausePath, h.Clause.Heading
		}
	}
	return bc, bh, best
}

// relocate finds the event's true home in a spec OTHER than the cited one.
func relocate(ctx context.Context, st *store.Store, tk []string, name, citedSpec string) (string, string, string, float64) {
	hits, err := st.SearchClauses(ctx, store.SearchQuery{Text: name, TopK: 12})
	if err != nil {
		return "", "", "", 0
	}
	best := 0.0
	var bs, bc, bh string
	for _, h := range hits {
		if h.Clause.SpecID == citedSpec {
			continue
		}
		// Score on the HEADING only: a clause that is the operation's true home
		// NAMES it in its title (e.g. "MAP_RESTORE_DATA service"). Body-text
		// token coincidences (e.g. a charging spec mentioning "bearer deletion")
		// would otherwise produce false relocations.
		if score := fracIn(tk, auditNorm(h.Clause.Heading)); score > best {
			best, bs, bc, bh = score, h.Clause.SpecID, h.Clause.ClausePath, h.Clause.Heading
		}
	}
	return bs, bc, bh, best
}

// LoadSentinel reads the oracle JSON.
func LoadSentinel(path string) ([]SentinelEvent, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var evs []SentinelEvent
	return evs, json.Unmarshal(b, &evs)
}

// Summary tallies findings by verdict (sorted keys for stable output).
func Summary(fs []Finding) ([]string, map[string]int) {
	m := map[string]int{}
	for _, f := range fs {
		m[f.Verdict]++
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, m
}
