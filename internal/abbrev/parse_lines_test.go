package abbrev

import "testing"

// Two excerpts of TS 21.905 v19.2.0's Abbreviations region, VERBATIM from the
// corpus (2026-09-11), where the wrap rule reads the clause wrongly.
//
// In the first, "Serving Network" and "Sequence Number" are further expansions of
// SN whose term cell lost its tab in conversion. In the second, OSP:IHOSS is
// rejected by Plausible (a colon in the term) and its wrapped tail "Service " is
// then glued onto OSP — the last entry that was ACCEPTED.
const (
	ts21905SN = "SN\tSerial Number\n\nServing Network\n\nSequence Number\n\n\tSubscriber Number\n\n" +
		"SNDCP\tSub-Network Dependent Convergence Protocol"
	ts21905OSP = "OSI RM\tOSI Reference Model\n\nOSP\tOctet Stream Protocol\n\n" +
		"OSP:IHOSS\tOctet Stream Protocol for Internet Hosted Octet Stream\nService \n\n\nOTA\tOver-The-Air"
)

func expansionOf(es []Entry, term string) (string, bool) {
	for _, e := range es {
		if e.Term == term {
			return e.Expansion, true
		}
	}
	return "", false
}

// PARSELINES READS THE PAIR THE LINE PRINTS, where Parse glues on text that is not
// the rest of it. The glossary seed asks it one question — does TS 21.905 still
// declare the key a seeded row carries? — and with Parse alone the answer was "no"
// for four of the 896 TS 21.905 entries the seed has taken over (SN, OSP, REQ,
// X2-U, measured 2026-09-11), so those four would have been deleted.
//
// Parse's readings are pinned too, because they are why ParseLines exists: the
// day the wrap rule stops gluing across a rejected entry, the second half of this
// test says so, and the seed's union can be revisited.
func TestParseLinesReadsTheLineWhereTheWrapRuleGuessesWrong(t *testing.T) {
	for _, c := range []struct {
		name, text, term, line, wrapped string
	}{
		{"alternative expansions", ts21905SN, "SN", "Serial Number",
			"Serial Number Serving Network Sequence Number"},
		{"tail of a rejected entry", ts21905OSP, "OSP", "Octet Stream Protocol",
			"Octet Stream Protocol Service"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got, ok := expansionOf(ParseLines(c.text), c.term); !ok || got != c.line {
				t.Errorf("ParseLines read %s = %q (found=%v), want %q as the line prints it — "+
					"a wrap was joined, so the seed cannot see that TS 21.905 declares this pair",
					c.term, got, ok, c.line)
			}
			if got, _ := expansionOf(Parse(c.text), c.term); got != c.wrapped {
				t.Errorf("Parse read %s = %q, want the documented misreading %q", c.term, got, c.wrapped)
			}
		})
	}
}

// PARSELINES IS PARSE WITH THE WRAP RULE OFF, AND NOTHING ELSE — the same split,
// the same guard, the same keys — so a pair it returns has the shape of the keys
// the seeded rows carry. Checked on the shapes Parse handles: a wrapped expansion
// (kept whole by Parse, first line only here), a rejected row, a change marker, and
// a space-aligned clause, where Parse joins nothing and the two must agree.
func TestParseLinesIsParseWithoutTheWrapRule(t *testing.T) {
	tabbed := "Intro paragraph without a tab.\n" +
		"NSSAAF\tNetwork Slice-Specific and SNPN\nAuthentication and Authorization Function\n" +
		"AMF\tNetwork Slice Selection Function\n" + // misaligned: Plausible rejects it
		"***** END SET OF CHANGES *****\n" +
		"UE\tUser  Equipment"
	want := []Entry{
		{"NSSAAF", "Network Slice-Specific and SNPN"},
		{"UE", "User Equipment"},
	}
	got := ParseLines(tabbed)
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("ParseLines(tabbed) = %+v, want %+v", got, want)
	}
	if e, _ := expansionOf(Parse(tabbed), "NSSAAF"); e != "Network Slice-Specific and SNPN Authentication and Authorization Function" {
		t.Errorf("Parse no longer joins the NSSAAF wrap (%q) — the switch leaked into the miner", e)
	}

	spaced := "5GC 5G Core Network\nABBA Anti-Bidding down Between Architectures\nNG-RAN 5G Radio Access Network"
	p, l := Parse(spaced), ParseLines(spaced)
	if len(p) != 2 || len(l) != len(p) || p[0] != l[0] || p[1] != l[1] {
		t.Errorf("on a space-aligned clause Parse = %+v and ParseLines = %+v; they must agree", p, l)
	}
}
