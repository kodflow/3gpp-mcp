// Command anchorcheck verifies the delta anchor against the corpus it describes.
//
// The anchor (`corpus-index.json`, "spec|Rel -> highest indexed version") is what
// `discover` trusts to decide "already indexed, skip". It ships as a SEPARATE
// artefact alongside the DB snapshot, so it can disagree with the DB it claims to
// describe — and the two failure directions are not symmetric:
//
//   - UNDER-claiming (anchor behind the DB) only costs needless re-work;
//   - OVER-claiming (anchor ahead of, or absent from, the DB) makes discover skip
//     a spec that was never indexed. No later step notices, because every later
//     step trusts the same anchor. The corpus simply has a hole, permanently.
//
// `seedAnchor` in internal/goal guards the byte-for-byte case; this checks the
// semantic one. It found 56 such holes in the published snapshot, 24 of which the
// drift computation cannot see because the site version equals the anchor version.
//
//	anchorcheck --db data/3gpp.duckdb --index .local/corpus-index.json
//
// Exit code 1 when a true hole exists, so it can be used as a gate.
//
// Read-only.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/store"
)

var Version = "dev"

func main() {
	dbPath := flag.String("db", "", "corpus DuckDB to verify against (required)")
	idxPath := flag.String("index", "", "corpus-index.json to verify (required)")
	quiet := flag.Bool("quiet", false, "print only the counters, not the offending keys")
	emitState := flag.String("emit-state", "", "write the per-key corpus state as JSON to this path")
	emitRepair := flag.String("emit-repair", "", "write the missing_content keys, one spec|release per line, for discover --repair-plan")
	accept := flag.String("accept", "", "file of keys accepted as permanently absent; they are reported but do not fail the run")
	flag.Parse()
	if *dbPath == "" || *idxPath == "" {
		fmt.Fprintln(os.Stderr, "anchorcheck: --db and --index are required")
		os.Exit(2)
	}
	holes, err := run(*dbPath, *idxPath, *quiet, *emitState, *emitRepair, *accept)
	if err != nil {
		fmt.Fprintln(os.Stderr, "anchorcheck:", err)
		os.Exit(2)
	}
	if holes > 0 {
		os.Exit(1)
	}
}

// Corpus is what the DB actually holds, in the three shapes the check needs.
type Corpus struct {
	// ByCatalogue is spec|Rel -> highest version in spec_versions.
	ByCatalogue map[string]string
	// ByClause is spec|Rel -> highest version that actually has indexed TEXT. A
	// spec_versions row with no clause is catalogue metadata, and treating it as
	// indexed is exactly how a hole becomes invisible.
	ByClause map[string]string
	// SpecVersionSeen is "spec@version" for every (spec, version) with clauses,
	// under ANY release.
	SpecVersionSeen map[string]bool
}

// Verdict is the CORPUS state of one anchor key — deliberately a different
// vocabulary from the upstream state the anchor records.
//
// The whole defect this tool exists for is that one value, "the anchor names
// version X", was doing duty for two independent propositions: "upstream is at X"
// and "the corpus holds X". They are not the same and never were. The anchor
// answers only the first. These states answer the second.
//
// NonContent is the one that must stay explicit. 61 keys legitimately have no
// text of their own, and if that stays an implicit exception then sooner or later
// someone silences them with a filter — and silences the real gaps with the same
// gesture. Expected absence is a state, not an exception.
type Verdict int

const (
	// Indexed: the anchor's claim is backed by clause text.
	Indexed Verdict = iota
	// OverClaim: the anchor names a version newer than anything indexed.
	OverClaim
	// NonContent: no text under this release, but the SAME spec+version is indexed
	// under a neighbouring one. 3GPP routinely lists a spec's Rel-N entry at the
	// Rel-(N-1) version, so this is bookkeeping, not a gap.
	NonContent
	// MissingContent: no clause anywhere for that spec+version. The anchor is
	// asserting something false and discover will skip it forever.
	MissingContent
)

// Classify is the whole decision, kept pure so it can be tested without a DB.
func Classify(key, anchorVer string, c *Corpus) Verdict {
	if dv, ok := c.ByClause[key]; ok {
		if CmpVer(anchorVer, dv) > 0 {
			return OverClaim
		}
		return Indexed
	}
	spec := strings.SplitN(key, "|", 2)[0]
	if c.SpecVersionSeen[spec+"@"+anchorVer] {
		return NonContent
	}
	return MissingContent
}

// CmpVer orders dotted numeric versions component-wise. String comparison gets
// this wrong in the way that matters here: "2.10.0" sorts BEFORE "2.9.0".
func CmpVer(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		x, y := part(as, i), part(bs, i)
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// part returns the i-th dotted component as an integer; a missing or
// non-numeric component counts as 0 so a malformed version never panics and
// never sorts above a real one.
func part(s []string, i int) int {
	if i >= len(s) {
		return 0
	}
	n := 0
	for _, r := range s[i] {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func run(dbPath, idxPath string, quiet bool, emitState, emitRepair, accept string) (int, error) {
	accepted, err := loadAccepted(accept)
	if err != nil {
		return 0, err
	}
	raw, err := os.ReadFile(idxPath)
	if err != nil {
		return 0, err
	}
	anchor := map[string]string{}
	if err := json.Unmarshal(raw, &anchor); err != nil {
		return 0, fmt.Errorf("parse anchor %q: %w", idxPath, err)
	}

	s, err := store.OpenReadOnly(dbPath)
	if err != nil {
		return 0, err
	}
	defer func() { _ = s.Close() }()

	c, err := loadCorpus(s)
	if err != nil {
		return 0, err
	}

	counts := map[Verdict]int{}
	var offenders []string
	fatal, acceptedHit := 0, 0
	for k, av := range anchor {
		v := Classify(k, av, c)
		counts[v]++
		if v != MissingContent && v != OverClaim {
			continue
		}
		if accepted[k] {
			acceptedHit++
			continue
		}
		fatal++
		offenders = append(offenders, fmt.Sprintf("%s\t%s\tanchor=%s", name(v), k, av))
	}
	sort.Strings(offenders)

	fmt.Printf("anchor_keys=%d catalogue_keys=%d clause_keys=%d\n",
		len(anchor), len(c.ByCatalogue), len(c.ByClause))
	fmt.Printf("indexed=%d non_content=%d over_claim=%d missing_content=%d accepted_absent=%d unaccounted=%d\n",
		counts[Indexed], counts[NonContent], counts[OverClaim], counts[MissingContent], acceptedHit, fatal)
	// An accept entry that no longer matches anything is stale, and a stale accept
	// list is how a real gap gets silently pre-forgiven later. Say so.
	for k := range accepted {
		if av, ok := anchor[k]; !ok || Classify(k, av, c) == Indexed {
			fmt.Printf("  STALE_ACCEPT\t%s\tno longer absent — remove it from the accept list\n", k)
		}
	}
	if !quiet {
		for _, o := range offenders {
			fmt.Println(" ", o)
		}
	}
	if emitState != "" {
		if err := writeState(emitState, anchor, c); err != nil {
			return 0, err
		}
	}
	if emitRepair != "" {
		if err := writeRepairKeys(emitRepair, anchor, c); err != nil {
			return 0, err
		}
	}
	return fatal, nil
}

// loadAccepted reads the checked-in list of keys acknowledged as permanently
// absent. An EMPTY path means no acknowledgements — never "accept everything".
// A missing FILE is an error rather than an empty set: silently treating a
// mistyped path as "nothing accepted" would be safe, but silently treating it as
// a working gate would not, and the two are hard to tell apart from a green run.
func loadAccepted(path string) (map[string]bool, error) {
	out := map[string]bool{}
	if path == "" {
		return out, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("accept list %q: %w", path, err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		l := strings.TrimSpace(line)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		out[l] = true
	}
	return out, nil
}

// writeRepairKeys emits exactly the keys a repair pass must re-acquire: the ones
// the anchor claims and the corpus cannot back. `non_content` is deliberately NOT
// included — it is expected absence, and re-fetching it forever would be the
// mirror-image mistake of skipping the real gaps.
func writeRepairKeys(path string, anchor map[string]string, c *Corpus) error {
	var keys []string
	for k, av := range anchor {
		switch Classify(k, av, c) {
		case MissingContent, OverClaim:
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func name(v Verdict) string {
	switch v {
	case OverClaim:
		return "over_claim"
	case MissingContent:
		return "missing_content"
	case NonContent:
		return "non_content"
	default:
		return "indexed"
	}
}

// CorpusState is the persisted answer to "what does the corpus actually hold",
// kept deliberately separate from the anchor, which answers only "what version did
// we last observe upstream". Conflating the two is the entire defect.
type CorpusState struct {
	Schema int            `json:"schema"`
	Counts map[string]int `json:"counts"`
	// Keys carries only the states worth acting on. `indexed` is the overwhelming
	// majority and listing it would bury the rest; its count is in Counts.
	Keys map[string]KeyState `json:"keys"`
}

// KeyState is one (spec|release) and why it is in the state it is in.
type KeyState struct {
	State  string `json:"state"`
	Anchor string `json:"anchor"`
	DB     string `json:"db,omitempty"`
}

// writeState persists the classification so downstream tools consume a decided
// state rather than re-deriving it — and so `non_content` survives as a recorded
// fact instead of being re-inferred, differently, by whoever needs it next.
func writeState(path string, anchor map[string]string, c *Corpus) error {
	st := CorpusState{Schema: 1, Counts: map[string]int{}, Keys: map[string]KeyState{}}
	for k, av := range anchor {
		v := Classify(k, av, c)
		st.Counts[name(v)]++
		if v == Indexed {
			continue
		}
		st.Keys[k] = KeyState{State: name(v), Anchor: av, DB: c.ByClause[k]}
	}
	b, err := json.MarshalIndent(st, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func loadCorpus(s *store.Store) (*Corpus, error) {
	c := &Corpus{
		ByCatalogue:     map[string]string{},
		ByClause:        map[string]string{},
		SpecVersionSeen: map[string]bool{},
	}
	if err := each(s, `SELECT spec_id, release, version FROM spec_versions`,
		func(spec, rel, ver string) {
			keepHighest(c.ByCatalogue, spec+"|"+rel, ver)
		}); err != nil {
		return nil, err
	}
	if err := each(s, `SELECT DISTINCT spec_id, release, version FROM clauses`,
		func(spec, rel, ver string) {
			keepHighest(c.ByClause, spec+"|"+rel, ver)
			c.SpecVersionSeen[spec+"@"+ver] = true
		}); err != nil {
		return nil, err
	}
	return c, nil
}

func keepHighest(m map[string]string, k, ver string) {
	if cur, ok := m[k]; !ok || CmpVer(ver, cur) > 0 {
		m[k] = ver
	}
}

func each(s *store.Store, q string, fn func(spec, rel, ver string)) error {
	rows, err := s.QueryContext(context.Background(), q)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var spec, rel, ver string
		if err := rows.Scan(&spec, &rel, &ver); err != nil {
			return err
		}
		fn(spec, rel, ver)
	}
	return rows.Err()
}
