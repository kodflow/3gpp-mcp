package li

import "testing"

func TestAuditTokens(t *testing.T) {
	cases := map[string][]string{
		"RESTORE_DATA":     {"restore", "data"},
		"EUTRAN_ATTACH":    {"eutran", "attach"}, // hyphen normalised: matches "e-utran"
		"START_OF_INTCPT":  {},                   // all tokens are stop/short
		"MAP_SEND_ROUTING": {"map", "send", "routing"},
	}
	for in, want := range cases {
		got := auditTokens(in)
		if len(got) != len(want) {
			t.Errorf("auditTokens(%q) = %v, want %v", in, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("auditTokens(%q)[%d] = %q, want %q", in, i, got[i], want[i])
			}
		}
	}
}

func TestFracIn(t *testing.T) {
	// "e-utran attach procedure" normalised drops the hyphen -> "eutran" matches.
	txt := auditNorm("E-UTRAN attach procedure")
	if f := fracIn([]string{"eutran", "attach"}, txt); f < 1.0 {
		t.Errorf("fracIn co-located tokens = %v, want 1.0", f)
	}
	if f := fracIn([]string{"restore", "data"}, "the create session request"); f != 0 {
		t.Errorf("fracIn absent tokens = %v, want 0", f)
	}
}

func TestKnownHomeOverride(t *testing.T) {
	for _, ev := range []string{"RESTORE_DATA", "REGISTRATION_REFRESH"} {
		if kh, ok := knownHome[ev]; !ok || kh.spec == "" || kh.clause == "" {
			t.Errorf("knownHome[%q] missing or incomplete: %+v", ev, kh)
		}
	}
}
