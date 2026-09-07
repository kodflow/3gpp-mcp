package goal

import "testing"

// VALIDATE MUST NOT BE ABLE TO RUN BEFORE THE ETSI INDEX IS FROZEN.
//
// The contract carries --require-etsi, which asserts the ETSI half's HNSW is
// FROZEN. The only step that freezes it is index-etsi. With just "index"
// declared, validate ran BETWEEN the two indexes and failed on a corpus that was
// entirely correct:
//
//	[FAIL] require-etsi: clauses_with_text=2000550 vectors=2000550 (missing=0)
//	       hnsw_state="building" — the server would REFUSE the ETSI index
//
// Measured on build 24 (2026-09-07), the first run of the strengthened contract,
// after 2 h 18 of pipeline. The ordering was wrong long before that; the weak
// contract never looked at the ETSI half, so nothing could see it.
//
// THE ASSERTION IS THE DEPENDENCY CLOSURE, not a position. The first version of
// this test compared topological DEPTHS and passed with the fix reverted —
// index-etsi sits on a shorter chain than validate, so its depth is lower either
// way. A test that passes in both directions proves nothing, so it is written
// against the property that actually matters: index-etsi must be reachable from
// validate through Deps, which is what forbids the runner from ordering them the
// wrong way round.
func TestValidateCannotRunBeforeTheETSIIndex(t *testing.T) {
	closure := transitiveDeps(t, "validate")

	for _, want := range []string{"index", "index-etsi"} {
		if !closure[want] {
			t.Errorf("validate does not depend on %s, transitively or otherwise — the contract "+
				"asserts that step's HNSW is frozen, and only that step freezes it", want)
		}
	}
}

// And the gates must precede what they gate: an unchecked corpus must never be
// smoke-tested as if it had passed, nor published.
func TestSmokeAndPublishDependOnValidate(t *testing.T) {
	for _, name := range []string{"smoke", "publish"} {
		if !transitiveDeps(t, name)["validate"] {
			t.Errorf("%s does not depend on validate — it would run on a corpus no gate has "+
				"accepted", name)
		}
	}
}

// transitiveDeps walks Deps and AnyDeps from one step and returns everything
// reachable. AnyDeps counts: a step with two possible producers still cannot run
// before whichever one supplies it.
func transitiveDeps(t *testing.T, start string) map[string]bool {
	t.Helper()
	byName := map[string]*Step{}
	for _, s := range Pipeline() {
		byName[s.Name] = s
	}
	if _, ok := byName[start]; !ok {
		t.Fatalf("step %q is not in the pipeline", start)
	}

	out := map[string]bool{}
	var walk func(string)
	walk = func(name string) {
		s, ok := byName[name]
		if !ok {
			t.Fatalf("step %q is declared as a dependency but is not in the pipeline", name)
		}
		for _, d := range append(append([]string{}, s.Deps...), s.AnyDeps...) {
			if out[d] {
				continue
			}
			out[d] = true
			walk(d)
		}
	}
	walk(start)
	return out
}
