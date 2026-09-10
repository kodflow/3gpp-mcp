package abbrev

import (
	"strings"
	"testing"
)

// ts33802v020s33 is TS 33.802 v0.2.0 §3.3 exactly as the shipped corpus stores it
// (chunk 10722491, read 2026-09-11): 18 entries, and after the last one the
// change-request markers the conversion split across four lines. It is the clause
// seed-glossary chooses for 33.802, and the source of the stored row
//
//	UA | User Agent ***** END SET OF CHANGES ***** ***** BEGIN SET OF CHANGES *****
const ts33802v020s33 = "For\nthe purposes of the present document, the following abbreviations\n" +
	"apply, TS 21.905 [7] contains additional applicable\nabbreviations:\n\n" +
	"AAA\tAuthentication Authorisation Accounting\n\nAKA\tAuthentication and key agreement\n\n" +
	"CSCF\tCall Session Control Function\n\nHSS\tHome Subscriber Server\n\nIM\tIP Multimedia\n\n" +
	"IMPI\tIM Private Identity\n\nIMPU\tIM Public Identity\n\n" +
	"IMS\tIP Multimedia Core Network Subsystem\n\nISIM\tIM Services Identity Module\n\n" +
	"MAC\tMessage Authentication Code\n\nME\tMobile Equipment\n\n" +
	"NAPT\tNetwork Address and Port Translation\n\nNAT\tNetwork Address Translation\n\n" +
	"SA\tSecurity Association\n\nSEG\tSecurity Gateway\n\nSDP\tSession Description Protocol\n\n" +
	"SIP\tSession Initiation Protocol\n\nUA\tUser Agent\n\n\n\n\n*****\nEND SET OF CHANGES *****\n\n\n\n" +
	"*****\nBEGIN SET OF CHANGES *****"

// THE UA ROW, REPRODUCED AND FIXED. The markers must be neither glued onto UA as
// its wrapped tail nor read as entries, and the other 17 entries must not move.
func TestParseDropsTheChangeRequestMarkersOf33802(t *testing.T) {
	got := Parse(ts33802v020s33)
	if len(got) != 18 {
		t.Fatalf("parsed %d entries from 33.802 v0.2.0 §3.3, want the 18 it declares: %+v", len(got), got)
	}
	for _, e := range got {
		if strings.Contains(e.Expansion, "*") || strings.Contains(strings.ToUpper(e.Expansion), "SET OF CHANGES") {
			t.Errorf("%s = %q: a change-request marker was glued onto the expansion", e.Term, e.Expansion)
		}
	}
	if last := got[len(got)-1]; last != (Entry{"UA", "User Agent"}) {
		t.Errorf("the last entry is %+v, want UA = \"User Agent\"", last)
	}
}

// THE RULE MATCHES THE MARKER SHAPE, NOT A CHARACTER OR A WORD. Every shape below
// on the left was measured in the corpus's clause text (2026-09-11), plus the ones
// CRs are known to use; every line on the right is a real near-miss from the same
// corpus — a footnote mark, a table cell, prose, an expansion that ends in
// "Change" — and must be left to the rules that were already reading it.
func TestChangeMarkerMatchesTheMarkerShapeOnly(t *testing.T) {
	for _, line := range []string{
		"*****",
		"END SET OF CHANGES *****",
		"BEGIN SET OF CHANGES *****",
		"* * NEXT CHANGE * * * *",
		"-------------------------- NEXT CHANGE",
		"END OF CHANGES TS 23.272 [3]*****",
		"NEXT CHANGE TS 23.272 [3] *****",
		"FIRST CHANGE TS 23.401 [2]*****",
		"START OF CHANGE IN TS 25.212 ====================================",
		"END OF CHANGE IN TS25.214 ================================",
		"START OF CHANGES INTENDED FOR TS38.101-2 >",
		"NEXT CHANGES >",
		"END OF CHANGES************************************",
		"***** NEXT CHANGE *****",
		"*** First change ***",
		"<<<<<<< Start of change 2 >>>>>>>",
		"* * * * *",
		"=====",
	} {
		if !changeMarker(line) {
			t.Errorf("%q is a change-request marker and was not recognised — it would be "+
				"glued onto the entry above it", line)
		}
	}
	for _, line := range []string{
		"(*)",
		"Access Point Name (*)",
		"GERAN\tGSM/EDGE Radio Access Network (*)",
		"(***) IF THE MS THAT NEEDS ONLY GPRS SERVICES AND \"SMS-ONLY",
		"**",
		"Change",
		"Conditional PSCell Addition or Change",
		"Next Change Indication",
		"END OF CHANGES",
		"NEXT CHANGE 60",
		"SECOND CHANGE THIS FIELD IS CONDITIONAL, AND INCLUDED ONLY IF THE",
		"BEGINNING OF THE NEXT MODIFICATION PERIOD. THE UE SHALL READ AT LEAST",
		"For the purposes of the present document, the abbreviations given in TR 21.905 [1] apply",
	} {
		if changeMarker(line) {
			t.Errorf("%q was taken for a change-request marker — it is text, and dropping it "+
				"loses a legitimate entry or wrapped tail", line)
		}
	}
}

// A MARKER CLOSES THE ENTRY ABOVE IT. Text on the far side of a change boundary
// belongs to another change; joined, it would falsify the entry the marker ended.
// The control shows the same tail IS joined without the marker, so it is the
// marker — not the tail's shape — that stops it.
func TestAChangeMarkerClosesTheEntryAboveIt(t *testing.T) {
	got := Parse("UA\tUser Agent\n***** END SET OF CHANGES *****\nand text of the next change\n" +
		"UE\tUser Equipment")
	want := []Entry{{"UA", "User Agent"}, {"UE", "User Equipment"}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Parse across a marker = %+v, want %+v", got, want)
	}

	control := Parse("UA\tUser Agent\nand text of the next change")
	if len(control) != 1 || control[0].Expansion != "User Agent and text of the next change" {
		t.Fatalf("the control failed: without a marker the tail should join, got %+v — "+
			"the first half proves nothing", control)
	}
}

// LEGITIMATE ASTERISKS AND "CHANGE" WORDS SURVIVE, as entries and as wrapped
// tails: the stored "(*)" footnote rows, and an expansion that wraps onto a line
// reading "Next Change Indication".
func TestParseKeepsLegitimateAsterisksAndChanges(t *testing.T) {
	got := Parse("APN\tAccess Point Name (*)\n\nCPAC\tConditional PSCell Addition or\nChange\n\n" +
		"NCI\tNotification of\nNext Change Indication")
	want := []Entry{
		{"APN", "Access Point Name (*)"},
		{"CPAC", "Conditional PSCell Addition or Change"},
		{"NCI", "Notification of Next Change Indication"},
	}
	if len(got) != len(want) {
		t.Fatalf("Parse = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
