package abbrev

import (
	"strings"
	"testing"
)

// realClause is TS 23.501 §3.2 as the shipped corpus actually holds it: the
// introductory paragraph hard-wrapped across five lines, entries separated by
// blank lines, tab between the columns, and one entry (NSSAAF) whose expansion
// wraps onto a line of its own.
//
// It is reproduced verbatim, wrapping included. Tidying it would delete the two
// cases this parser exists to survive.
const realClause = "For the\n" +
	"purposes of the present document, the abbreviations given in\n" +
	"TR 21.905 [1] and the following apply. An abbreviation\n" +
	"defined in the present document takes precedence over the definition\n" +
	"of the same abbreviation, if any, in TR 21.905 [1].\n" +
	"\n" +
	"5GC\t5G Core Network\n" +
	"\n" +
	"5G-EIR\t5G-Equipment Identity Register\n" +
	"\n" +
	"AMF\tAccess and Mobility Management Function\n" +
	"\n" +
	"NSSAAF\tNetwork Slice-specific and SNPN Authentication and\n" +
	"Authorization Function\n" +
	"\n" +
	"NWDAF\tNetwork Data Analytics Function\n" +
	"\n" +
	"UPF\tUser Plane Function\n"

func TestParseReadsTheRealClause(t *testing.T) {
	got := Parse(realClause)
	want := []Entry{
		{"5GC", "5G Core Network"},
		{"5G-EIR", "5G-Equipment Identity Register"},
		{"AMF", "Access and Mobility Management Function"},
		{"NSSAAF", "Network Slice-specific and SNPN Authentication and Authorization Function"},
		{"NWDAF", "Network Data Analytics Function"},
		{"UPF", "User Plane Function"},
	}
	if len(got) != len(want) {
		t.Fatalf("parsed %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// The wrap, on its own, because it is the failure that LOOKS like success: a
// parser that reads only tabbed lines stores "Network Slice-specific and SNPN
// Authentication and" and reports one entry seeded, correctly, forever.
func TestParseJoinsAWrappedExpansion(t *testing.T) {
	got := Parse("NSSAAF\tNetwork Slice-specific and SNPN Authentication and\nAuthorization Function\n")
	if len(got) != 1 {
		t.Fatalf("parsed %d entries, want 1: %+v", len(got), got)
	}
	const want = "Network Slice-specific and SNPN Authentication and Authorization Function"
	if got[0].Expansion != want {
		t.Errorf("expansion = %q, want %q", got[0].Expansion, want)
	}
}

// The intro paragraph is prose that happens to sit in the same clause. It has no
// tabs, so it must not become entries — and it must not be appended to an entry
// either, since it arrives BEFORE the first one.
func TestParseIgnoresTheIntroductoryParagraph(t *testing.T) {
	intro := "For the\npurposes of the present document, the abbreviations given in\n" +
		"TR 21.905 [1] and the following apply.\n"
	if got := Parse(intro); len(got) != 0 {
		t.Fatalf("prose produced %d entries, want 0: %+v", len(got), got)
	}
	// And with an entry after it, the prose must not be glued to that entry.
	got := Parse(intro + "\nAMF\tAccess and Mobility Management Function\n")
	if len(got) != 1 || got[0].Expansion != "Access and Mobility Management Function" {
		t.Fatalf("got %+v, want exactly the AMF entry", got)
	}
}

func TestParseEdges(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		want     int
	}{
		{"empty", "", 0},
		{"blank lines only", "\n\n\n", 0},
		{"CRLF line endings", "AMF\tAccess and Mobility Management Function\r\n", 1},
		{"several tabs between the columns", "AMF\t\t\tAccess and Mobility Management Function\n", 1},
		{"duplicate rows collapse", "UPF\tUser Plane Function\n\nUPF\tUser Plane Function\n", 1},
		{"same term, two expansions, both kept", "AMF\tAccess and Mobility Management Function\n\nAMF\tAuthentication Management Field\n", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Parse(tc.in); len(got) != tc.want {
				t.Errorf("parsed %d entries, want %d: %+v", len(got), tc.want, got)
			}
		})
	}
}

// THE GUARD. A shifted column produces rows that look like entries; the
// same-initial rule is what refuses them.
func TestPlausible(t *testing.T) {
	for _, tc := range []struct {
		name, term, expansion string
		want                  bool
	}{
		{"ordinary entry", "AMF", "Access and Mobility Management Function", true},
		{"digits", "5GC", "5G Core Network", true},
		{"hyphen in both", "5G-EIR", "5G-Equipment Identity Register", true},
		{"space inside the term", "5G-AN PDB", "5G Access Network Packet Delay Budget", true},
		{"letter from inside a word", "NWDAF", "Network Data Analytics Function", true},

		// Misalignment: the expansion belongs to a different term.
		{"shifted column", "UPF", "Access and Mobility Management Function", false},
		{"expansion is the next term", "AMF", "NSSAAF", false},
		// Prose that reached the parser through a stray tab.
		{"a sentence is not a term", "For the purposes of the present document", "For the purposes", false},
		{"lowercase prose term", "and the following apply", "and so on", false},
		{"term carries punctuation", "TR 21.905 [1]", "TR 21.905", false},
		{"empty term", "", "Access and Mobility Management Function", false},
		{"empty expansion", "AMF", "", false},
		{"one-character expansion", "AMF", "A", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Plausible(tc.term, tc.expansion); got != tc.want {
				t.Errorf("Plausible(%q, %q) = %v, want %v", tc.term, tc.expansion, got, tc.want)
			}
		})
	}
}

// TS 33.501 §3.2 aligns its columns with SPACES, not a tab. A tab-only parser
// reads none of its 4 700 characters and reports success, which is how a spec
// silently contributes nothing. The term itself may contain a space, so where
// the split lands is derived, never assumed.
const spaceAlignedClause = "For the purposes of the present document, the abbreviations given in\n" +
	"3GPP TR 21.905 [1] and the following apply.\n" +
	"\n" +
	"5GC 5G Core Network\n" +
	"5G-AN 5G Access Network\n" +
	"5G AV 5G Authentication Vector\n" +
	"5G HE AV 5G Home Environment Authentication Vector\n" +
	"ABBA Anti-Bidding down Between Architectures\n" +
	"NG-RAN 5G Radio Access Network\n"

func TestParseReadsASpaceAlignedClause(t *testing.T) {
	got := Parse(spaceAlignedClause)
	want := map[string]string{
		"5GC":      "5G Core Network",
		"5G-AN":    "5G Access Network",
		"5G AV":    "5G Authentication Vector",
		"5G HE AV": "5G Home Environment Authentication Vector",
		"ABBA":     "Anti-Bidding down Between Architectures",
	}
	// NG-RAN is deliberately absent: its expansion opens on "5G", so no split
	// of that line can be confirmed. Skipping it is the point — the alternative
	// is guessing a term boundary and writing a row nobody can check.
	if len(got) != len(want) {
		t.Fatalf("parsed %d entries, want %d: %+v", len(got), len(want), got)
	}
	for _, e := range got {
		if w, seen := want[e.Term]; !seen || w != e.Expansion {
			t.Errorf("entry %+v is not one of the expected rows", e)
		}
		if e.Term == "NG-RAN" {
			t.Errorf("NG-RAN was split on a guess; it must be skipped")
		}
		if strings.HasPrefix(e.Term, "For") || strings.HasPrefix(e.Term, "3GPP") {
			t.Errorf("intro prose parsed as an entry: %+v", e)
		}
	}
}

func TestSplitFieldsPrefersTheShortestTerm(t *testing.T) {
	// "5GC 5G Core Network" must split as 5GC | 5G Core Network, never as
	// "5GC 5G" | "Core Network": under a longer prefix both halves still open
	// on the right letter, and only the shortest-wins rule separates them.
	term, exp, ok := splitFields("5GC 5G Core Network")
	if !ok || term != "5GC" || exp != "5G Core Network" {
		t.Fatalf("splitFields = (%q, %q, %v), want (\"5GC\", \"5G Core Network\", true)", term, exp, ok)
	}
	if _, _, ok := splitFields("NG-RAN 5G Radio Access Network"); ok {
		t.Error("a row whose expansion does not open on the term's letter must not be split")
	}
	if _, _, ok := splitFields("For the purposes of the present document, the abbreviations given in"); ok {
		t.Error("introductory prose must not split into an entry")
	}
}
