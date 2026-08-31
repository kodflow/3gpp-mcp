package goal

import (
	"reflect"
	"testing"
)

// TestEtsiScopeKnobIsReachable is the regression test for a knob that was READ but
// never WRITTEN: both ETSI steps called c.Cfg("etsi_scope") while nothing ever put
// that key into Ctx.Config, so the ETSI corpus was pinned to the built-in
// fourteen-spec LI suite and no flag, env var or config could widen it. The two
// helpers below are the seam; cmd/goal now supplies the value.
func TestEtsiScopeKnobIsReachable(t *testing.T) {
	cases := []struct {
		name     string
		scope    string
		wantArgs []string
		wantEnv  []string
	}{
		{"empty keeps the built-in LI suite", "", nil, nil},
		{"all widens to the whole deliver archive", "all", []string{"--all"}, []string{"ETSI_ALL=1"}},
		{"whitespace around all still means all", "  all  ", []string{"--all"}, []string{"ETSI_ALL=1"}},
		{
			"an explicit list scopes explicitly",
			"103 221-1,103 280",
			[]string{"--specs", "103 221-1,103 280"},
			[]string{"ETSI_SPECS=103 221-1,103 280"},
		},
		{
			// The trim must reach the VALUE, not just the dispatch: a leading
			// space forwarded to --specs becomes part of the first id and
			// resolves nothing.
			"whitespace around an explicit list is trimmed off the value too",
			"  103 280  ",
			[]string{"--specs", "103 280"},
			[]string{"ETSI_SPECS=103 280"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := etsiScopeArgs(tc.scope); !reflect.DeepEqual(got, tc.wantArgs) {
				t.Errorf("etsiScopeArgs(%q) = %v, want %v", tc.scope, got, tc.wantArgs)
			}
			if got := etsiScopeEnv(tc.scope); !reflect.DeepEqual(got, tc.wantEnv) {
				t.Errorf("etsiScopeEnv(%q) = %v, want %v", tc.scope, got, tc.wantEnv)
			}
		})
	}
}

// TestEtsiScopeArgsAndEnvAgree — discover-etsi is invoked twice for the same run:
// directly by the discover-etsi step, and again by scripts/etsi-corpus.sh through
// the environment. If the two translations of the scope ever disagreed, the work
// list the pipeline validates would not be the work list the builder fetches.
func TestEtsiScopeArgsAndEnvAgree(t *testing.T) {
	for _, scope := range []string{"", "all", "103 280"} {
		gotArgs, gotEnv := etsiScopeArgs(scope), etsiScopeEnv(scope)
		if (len(gotArgs) == 0) != (len(gotEnv) == 0) {
			t.Errorf("scope %q: args=%v but env=%v — one widens and the other does not", scope, gotArgs, gotEnv)
		}
	}
}
