package li

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/model"
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
//
// AMBIGUOUS exists so the report stops calling a draw a hallucination. NOT_FOUND
// is a claim about the corpus — "this text is nowhere" — and it must not be used
// for "the name does not carry enough signal to decide". Separating "I cannot
// measure" from "the condition is violated" is the same rule the pipeline's
// guards follow.
const (
	VConfirmed = "CONFIRMED"           // cited clause exists AND name supported by its text
	VParentRef = "REAL_PARENT_REF"     // cited clause number synthetic, parent clause supports it
	VInCited   = "FOUND_IN_CITED_SPEC" // name present in cited spec, clause ref imprecise
	VWrongSpec = "WRONG_SPEC_REF"      // absent from cited spec, located in ANOTHER indexed spec
	VAmbiguous = "AMBIGUOUS"           // the name does not identify one clause (see Why)
	VNotFound  = "NOT_FOUND"           // no trace anywhere (candidate hallucination)
)

// Score floors. A verdict is only as good as the evidence that clears its floor,
// so each one is named rather than inlined.
const (
	clauseFloor   = 0.5  // tokens co-located in the cited (or parent) clause
	inCitedFloor  = 0.75 // tokens co-located in some clause of the cited spec
	relocateFloor = 0.66 // tokens co-located in ANOTHER spec's heading
)

// Finding is the per-event audit result. When Verdict==WRONG_SPEC_REF, the
// Real* fields give the event's true normative home (e.g. TS 29.002 §8.10.3).
// When Verdict==AMBIGUOUS, Why says what stopped the decision.
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
	Why         string  `json:"why,omitempty"`
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
//
// Every lexical lookup goes through searchDistinct, because the corpus holds one
// row per RELEASE: a raw top-K is a top-K of versions, not of clauses, and it
// hides the answer behind copies of a single near-miss. And a search that cannot
// separate two candidate homes returns AMBIGUOUS with its reason, never
// NOT_FOUND — the latter is a claim that the text does not exist.
func AuditCatalog(ctx context.Context, st store.Reader, events []SentinelEvent) ([]Finding, error) {
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
					// Front matter — Contents, Foreword, Introduction — carries no
					// clause path, so every one of those rows collapses into
					// byPath[""]: last write wins, and which row that is depends on
					// storage order. TS 33.108's table of contents is 19 KB listing
					// every annex title, so it "contains" the tokens of almost any
					// operation name. 43 events cite "33.108 §Annex", whose parent
					// path is "" — 30 of them were once confirmed against that table
					// of contents, non-deterministically. An unpathed clause is not
					// addressable by a citation, so it is not indexed.
					if c.ClausePath == "" {
						continue
					}
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
		// An empty parent means the citation has no parent to fall back to
		// ("Annex" holds no dot), not that the parent is the unpathed front
		// matter. Checking one against the other is how a table of contents
		// became evidence.
		parent := parentClause(e.Clause)
		switch {
		case e.Clause != "" && fracIn(tk, si.byPath[e.Clause]) >= clauseFloor:
			f.Verdict, f.Score = VConfirmed, 1
		case parent != "" && fracIn(tk, si.byPath[parent]) >= clauseFloor:
			f.Verdict, f.Score = VParentRef, 0.9
		default:
			// Co-located match somewhere in the cited spec? (BM25-ranked clause.)
			if _, _, sc := bestHit(ctx, st, e.Event, tk, e.Spec); sc >= inCitedFloor {
				f.Verdict, f.Score = VInCited, sc
				break
			}
			r := relocate(ctx, st, tk, e.Event, e.Spec)
			switch {
			case r.score >= relocateFloor && r.rivals == 0:
				f.Verdict, f.Score = VWrongSpec, r.score
				f.RealSpec, f.RealClause, f.RealHeading = r.spec, r.clause, r.heading
			case r.why != "":
				f.Verdict, f.Score, f.Why = VAmbiguous, r.score, r.why
			default:
				f.Verdict, f.Score = VNotFound, r.score
			}
		}
		out = append(out, f)
	}
	return out, nil
}

// releaseFanOut is how many rows one clause occupies in `clauses`: the table
// holds every RELEASE of every spec, so a clause appears once per version — up
// to 17 times in this corpus, 5.6 on average. A raw TopK is therefore NOT a
// count of candidates. Measured on CHECK_IMEI, the whole 12-hit window was ONE
// clause (TS 29.002 §25.6.6) repeated twelve times, while the event's true home
// — TS 29.273 §5.2.3.35 "IMEI-Check-In-VPLMN-Result", heading coverage 1.0 —
// never entered it. Over-fetch, then fold the versions away.
const releaseFanOut = 20

// searchDistinct ranks clauses lexically and returns at most want hits with
// DISTINCT (spec, clause), keeping the best-ranked row of each.
func searchDistinct(ctx context.Context, st store.Reader, text, onlySpec string, want int) []model.SearchHit {
	hits, err := st.SearchClauses(ctx, store.SearchQuery{
		Text:   text,
		Filter: store.SpecFilter{SpecID: onlySpec},
		TopK:   want * releaseFanOut,
	})
	if err != nil {
		return nil
	}
	seen := make(map[string]bool, want)
	out := make([]model.SearchHit, 0, want)
	for _, h := range hits {
		key := h.Clause.SpecID + "\x00" + h.Clause.ClausePath
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, h)
		if len(out) == want {
			break
		}
	}
	return out
}

// bestHit returns the best token-coverage clause for name, optionally scoped to
// onlySpec (empty = whole index). It ranks via the store's lexical search then
// re-scores each hit by event-token co-location.
func bestHit(ctx context.Context, st store.Reader, name string, tk []string, onlySpec string) (string, string, float64) {
	best := 0.0
	var bc, bh string
	for _, h := range searchDistinct(ctx, st, name, onlySpec, 8) {
		if score := fracIn(tk, auditNorm(h.Clause.Heading+" "+h.Clause.Text)); score > best {
			best, bc, bh = score, h.Clause.ClausePath, h.Clause.Heading
		}
	}
	return bc, bh, best
}

// relocation is a cross-spec placement attempt: where the event's name is best
// supported, how well, and how many other specs support it just as well.
type relocation struct {
	spec, clause, heading string
	score                 float64
	rivals                int    // OTHER specs tying with the winner
	why                   string // set when the attempt cannot decide
}

// relocate finds the event's true home in a spec OTHER than the cited one.
func relocate(ctx context.Context, st store.Reader, tk []string, name, citedSpec string) relocation {
	// One token is not evidence. auditTokens drops fragments under three
	// characters, so IP_RELEASE reduces to ["release"] and every heading
	// carrying that word scores a perfect 1.0 — the "winner" is then whichever
	// row the ranker happened to return first, a coin toss reported as a
	// finding. That is how PGW/IP_RELEASE landed in TS 38.331, NR radio
	// resource control.
	if len(tk) < 2 {
		if len(tk) == 1 {
			return relocation{why: fmt.Sprintf("one usable token in the name (%q)", tk[0])}
		}
		return relocation{why: "no usable token in the name"}
	}
	best := 0.0
	var bs, bc, bh string
	ties := map[string]bool{}
	for _, h := range searchDistinct(ctx, st, name, "", 12) {
		if h.Clause.SpecID == citedSpec {
			continue
		}
		// Score on the HEADING only: a clause that is the operation's true home
		// NAMES it in its title (e.g. "MAP_RESTORE_DATA service"). Body-text
		// token coincidences (e.g. a charging spec mentioning "bearer deletion")
		// would otherwise produce false relocations.
		score := fracIn(tk, auditNorm(h.Clause.Heading))
		switch {
		case score > best:
			best, bs, bc, bh = score, h.Clause.SpecID, h.Clause.ClausePath, h.Clause.Heading
			ties = map[string]bool{h.Clause.SpecID: true}
		case score == best && best > 0:
			ties[h.Clause.SpecID] = true
		}
	}
	r := relocation{spec: bs, clause: bc, heading: bh, score: best, rivals: len(ties) - 1}
	// Two specs that name the operation equally well are two candidate homes,
	// and picking the first is not a measurement.
	if best >= relocateFloor && r.rivals > 0 {
		r.why = fmt.Sprintf("%d specs name it equally well (%.2f): %s", len(ties), best, strings.Join(sortedKeys(ties), ", "))
	}
	return r
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
