package glossaryseed

import "testing"

// newerVersion is what picks WHICH release of a spec the glossary is seeded
// from. Getting it wrong is silent: the run succeeds, the counts look right, and
// the corpus is seeded from a decade-old release.
func TestNewerVersion(t *testing.T) {
	for _, tc := range []struct {
		name, a, b string
		want       bool
	}{
		// THE CASE THAT MOTIVATES COMPARING NUMBERS. As strings "9.5.0" sorts
		// above "20.2.0", so a lexical max would seed 23.501 from Rel-9.
		{"double digits beat single", "20.2.0", "9.5.0", true},
		{"single digits lose to double", "9.5.0", "20.2.0", false},

		{"higher minor wins", "20.2.0", "20.1.0", true},
		{"higher patch wins", "18.12.1", "18.12.0", true},
		{"equal is not newer", "20.2.0", "20.2.0", false},
		{"shorter but higher major", "20", "19.9.9", true},
		{"missing components count as zero", "20.0.0", "20", false},
		{"anything beats nothing", "1.0.0", "", true},
		{"nothing beats nothing", "", "", false},

		// A version that is not dotted-numeric must not panic or win by
		// accident; the non-numeric parts read as zero.
		{"non-numeric component", "18.a.0", "18.0.0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := newerVersion(tc.a, tc.b); got != tc.want {
				t.Errorf("newerVersion(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// The default set is a decision with reasons attached; this pins the two that a
// future edit is most likely to undo without noticing.
func TestDefaultSpecs(t *testing.T) {
	if !contains(DefaultSpecs, "23.501") {
		t.Error("23.501 is the spec the measured failure was about; it cannot leave the default set")
	}
	// 23.502 is a core 5GC spec and looks like an omission. It is not: its
	// Abbreviations clause is 282 characters of introduction that defer wholly
	// to 23.501, so adding it prints "parsed=0" forever and reads like a broken
	// parser.
	if contains(DefaultSpecs, "23.502") {
		t.Error("23.502 declares no abbreviations of its own; listing it reports parsed=0 on every run")
	}
}

func contains(csv, id string) bool {
	for _, s := range splitCSV(csv) {
		if s == id {
			return true
		}
	}
	return false
}

func splitCSV(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	return append(out, cur)
}

// THE PREFERRED SPECS MUST BE READ LAST, and this test is here because getting it
// backwards changes nothing measurable.
//
// store.UpsertAcronyms keeps the LAST row it sees for a (term, expansion, domain)
// key. Reading the preferred specs first — which is what reads naturally, and what
// this code did when the sweep was written — makes them the rows that get
// overwritten: the count is identical, every gate passes, and the glossary quietly
// cites an obscure spec where citing TS 23.501 was the entire purpose.
func TestPreferredSpecsAreReadLastSoTheyWinTheKey(t *testing.T) {
	sweep := []string{"21.905", "23.401", "23.501", "38.331", "99.999"}
	preferred := map[string]bool{"23.501": true, "23.401": true}

	got := readOrder(sweep, preferred)
	if len(got) != len(sweep) {
		t.Fatalf("readOrder dropped specs: %v", got)
	}
	seen := map[string]bool{}
	for _, id := range got {
		seen[id] = true
	}
	for _, id := range sweep {
		if !seen[id] {
			t.Errorf("readOrder lost %q — every spec in the corpus must be read", id)
		}
	}
	// The last two must be the preferred ones, in any order between themselves.
	for _, id := range got[len(got)-2:] {
		if !preferred[id] {
			t.Errorf("%q is read after a preferred spec, so it would overwrite it", id)
		}
	}
	for _, id := range got[:len(got)-2] {
		if preferred[id] {
			t.Errorf("preferred spec %q is read early and would be overwritten", id)
		}
	}
}

// A sweep with no preference is still the whole corpus, in corpus order.
func TestReadOrderWithNoPreferenceKeepsEverySpec(t *testing.T) {
	sweep := []string{"a", "b", "c"}
	got := readOrder(sweep, map[string]bool{})
	if len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Errorf("readOrder(%v, {}) = %v", sweep, got)
	}
}
