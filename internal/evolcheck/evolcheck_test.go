package evolcheck

import (
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// TestNamesTermIsTokenNotSubstring locks the property the whole citation check
// rests on: an edge is credited only when the cited clause names its target as a
// WORD. A substring test would credit "PCF" to a clause that only ever says
// "PCFICH" — the check would then pass on precisely the mis-anchored citations it
// exists to catch, which is how the previous seed shipped three of them.
func TestNamesTermIsTokenNotSubstring(t *testing.T) {
	cases := []struct {
		name string
		body string
		term string
		want bool
	}{
		{"plain mention", "The AMF handles registration.", "AMF", true},
		{"start of text", "AMF is the access and mobility function.", "AMF", true},
		{"end of text", "…registration is handled by the AMF", "AMF", true},
		{"parenthesised", "the Policy Control Function (PCF) decides", "PCF", true},
		{"comma separated", "AMF, SMF and UPF", "SMF", true},
		{"hyphenated compound counts", "the AMF-set holds several AMFs", "AMF", true},

		// The failures that matter.
		{"substring of a longer token", "the PCFICH carries the indicator", "PCF", false},
		{"substring inside a word", "SAMFLOW is not a network function", "AMF", false},
		{"absent entirely", "Service-based interfaces between NFs.", "gNB", false},

		// Multi-word terms survive a line break in the extracted text.
		{"two-word term, single space", "the TSN AF exposes the bridge", "TSN AF", true},
		{"two-word term, newline between", "the TSN\nAF exposes the bridge", "TSN AF", true},
		{"two-word term absent", "the AF exposes the bridge", "TSN AF", false},

		// Case-insensitive: the extracted heading may be title-cased.
		{"case insensitive", "The Unified Data Management (udm) stores…", "UDM", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NamesTerm(tc.body, tc.term); got != tc.want {
				t.Errorf("NamesTerm(%q, %q) = %v, want %v", tc.body, tc.term, got, tc.want)
			}
		})
	}
}

// TestNamesTermEmptyTermIsVacuouslyTrue — the "new in 5G" edges carry no
// from-term, and it is the TO-term that gets checked. An empty term must not be
// reported as a failure, or every such edge would warn forever.
func TestNamesTermEmptyTermIsVacuouslyTrue(t *testing.T) {
	if !NamesTerm("anything at all", "") {
		t.Error("an empty term must not count as unnamed")
	}
	if !NamesTerm("anything at all", "   ") {
		t.Error("a whitespace-only term must not count as unnamed")
	}
}

// TestDescribeRendersTheNewIn5GEdges keeps the log line readable: an edge with no
// predecessor prints as "(new in 5G) -> DCCF", not as " -> DCCF".
func TestDescribeRendersTheNewIn5GEdges(t *testing.T) {
	if got, want := Describe(model.Evolution{FromTerm: "", ToTerm: "DCCF"}), "(new in 5G) -> DCCF"; got != want {
		t.Errorf("Describe = %q, want %q", got, want)
	}
	if got, want := Describe(model.Evolution{FromTerm: "MME", ToTerm: "AMF"}), "MME -> AMF"; got != want {
		t.Errorf("Describe = %q, want %q", got, want)
	}
}
