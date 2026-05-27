package glossary

import "testing"

func TestAbbrevRegex(t *testing.T) {
	ok := map[string]string{
		"AMF\tAccess and Mobility Management Function": "AMF",
		"gNB\tNext Generation NodeB":                   "gNB",
		"5GC\t5G Core network":                         "5GC",
		"MDF2  Mediation and Delivery Function 2":      "MDF2", // 2+ spaces
	}
	for line, term := range ok {
		m := reAbbrev.FindStringSubmatch(line)
		if m == nil || m[1] != term {
			t.Errorf("reAbbrev(%q) failed; got %v", line, m)
		}
	}
	// prose must NOT match (single-space separators)
	for _, prose := range []string{"This document defines the procedures", "The present specification"} {
		if reAbbrev.MatchString(prose) {
			t.Errorf("reAbbrev wrongly matched prose: %q", prose)
		}
	}
}
