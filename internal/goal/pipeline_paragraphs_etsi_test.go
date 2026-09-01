package goal

import "testing"

// TestBothCorporaAreContentAddressed pins the fix for a defect that would have
// made WIDENING the corpus make the product WORSE.
//
// Store.SearchClauses branches on the corpus shape:
//
//	if s.contentAddressed { return s.searchClausesCA(ctx, q) }
//
// and the comment above that line records what the other branch does — "the whole
// twelve-hit window for CHECK_IMEI was one clause repeated across twelve releases,
// and the spec that answers it never entered the window". Only the conversion puts
// a corpus on the deduplicated side.
//
// The ETSI half had no conversion step. That was invisible for as long as the ETSI
// crawl kept ONE version per deliverable: with one version there is nothing to
// repeat. The moment the crawl keeps every published version — 11 822 of them over
// 5 142 deliverables, which is the point of keeping them — the ETSI half reproduces
// the exact failure the 3GPP half already paid for and fixed.
//
// So the graph, not a comment, has to carry it: every corpus that is embedded and
// then searched gets converted, and the steps that read the converted shape depend
// on that conversion rather than on the embed that precedes it.
func TestBothCorporaAreContentAddressed(t *testing.T) {
	steps := map[string]*Step{}
	for _, s := range Pipeline() {
		steps[s.Name] = s
	}

	for _, suffix := range []string{"", "-etsi"} {
		conv := "paragraphs" + suffix
		if steps[conv] == nil {
			t.Fatalf("no %q step: that corpus is served from the branch that ranks versions", conv)
		}
		// The work list a sparse pass exports comes out of the converted shape, so
		// the pass must not be schedulable before the conversion. Depending on the
		// embed instead — which is what the ETSI arm used to do — orders the two
		// only by accident.
		sparse := steps["sparse"+suffix]
		if sparse == nil {
			t.Fatalf("no sparse%s step", suffix)
		}
		if !contains(sparse.Deps, conv) {
			t.Errorf("sparse%s depends on %v, which does not include %q", suffix, sparse.Deps, conv)
		}
		idx := steps["index"+suffix]
		if idx == nil {
			t.Fatalf("no index%s step", suffix)
		}
		if !contains(idx.Deps, conv) {
			t.Errorf("index%s depends on %v, which does not include %q", suffix, idx.Deps, conv)
		}
	}

	// The two conversions must target DIFFERENT files. A copy-paste that left the
	// ETSI arm pointed at 3gpp.duckdb would convert the same corpus twice, and
	// migrate-paragraphs declines an already-converted input — so the step would be
	// recorded SUCCESS having never touched the ETSI corpus at all.
	c := &Ctx{Data: "data"}
	g3 := steps["paragraphs"].Outputs(c)
	ge := steps["paragraphs-etsi"].Outputs(c)
	if len(g3) == 0 || len(ge) == 0 || g3[0] == ge[0] {
		t.Errorf("the two conversions write %v and %v — they must be different corpora", g3, ge)
	}
}
