package abbrev

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE TWO ARMS MINE ABBREVIATIONS WITH DIFFERENT RULES, ON PURPOSE.
//
// This file is where that is RECORDED, in the sense armShared records a shared
// pipeline step: the divergence was found on 2026-09-08, judged correct, and then
// left written down nowhere. An undocumented divergence between two arms of a
// pipeline whose whole doctrine is "the two arms must be the same list twice" is
// a defect waiting for a tidy-up — someone reads both miners, sees two rules for
// one job, unifies them, and every gate stays green while thousands of correct
// rows disappear.
//
// MEASURED ON THE PUBLISHED CORPUS (2026-09-09), IN BOTH DIRECTIONS:
//
//	3GPP glossary  14 126 rows   ETSI glossary  28 154 rows
//
//   - adopting the ETSI rule on the 3GPP arm rejects 4 270 of 14 126 rows (30.2 %),
//     every one of them correct: "eMBB = Enhanced MBB", "C-TPDU = Command TPDU",
//     "TFA = TransFer Allowed" — 3GPP takes letters from INSIDE words, which is
//     the NWDAF case internal/abbrev/abbrev.go already names.
//   - adopting the 3GPP rule on the ETSI arm rejects 1 377 of 28 154 rows (4.9 %),
//     also correct: "3G-SGSN = Third Generation-Serving GPRS Support Node" (3 vs T),
//     "ATTM = ETSI TC-Access Terminals, Transmission and Multiplexing" (A vs E),
//     "ASN.1 = Abstract Syntax Notation no. one" (the dot reads as punctuation).
//
// SO NEITHER RULE IS THE BETTER ONE. They guard different failures, because the
// two SOURCES fail differently. A 3GPP abbreviation list survives .doc conversion
// as a TABLE, so the only thing that goes wrong is column misalignment, and the
// weakest rule that catches it — same first character — is the right one. ETSI
// ships PDFs, and `pdftotext -layout` prints a tall cell's expansion ABOVE its
// term, pairing term N with expansion N+1 for the rest of the list: 3 264 of
// 8 995 rows wrong (36.3 %) before the guard, including "IMSI = International
// Organization for Standardization". Only the strict initials rule catches that,
// and it is worth its 4.9 % of true losses there precisely because a wrong
// definition is an answer the caller cannot check.
//
// This test fails if either rule drifts toward the other.
func TestTheTwoGlossaryRulesAreDeliberatelyDifferent(t *testing.T) {
	// 1. The 3GPP rule must keep what the ETSI rule would reject. Each of these is
	//    a real row from the published 3GPP glossary whose letters are not word
	//    initials — the 30.2 % that a "tidy-up" would delete.
	for _, c := range []struct{ term, expansion string }{
		{"NWDAF", "Network Data Analytics Function"},
		{"eMBB", "Enhanced MBB"},
		{"C-TPDU", "Command TPDU"},
		{"TFA", "TransFer Allowed"},
		{"5GC", "5G Core Network"},
	} {
		if !Plausible(c.term, c.expansion) {
			t.Errorf("the 3GPP rule rejected %q = %q; it has been tightened toward the ETSI rule, "+
				"which would drop 4 270 correct rows (30.2%%) from this arm", c.term, c.expansion)
		}
	}

	// 2. And it must stay a real guard: a misaligned column is what it exists for.
	for _, c := range []struct{ term, expansion string }{
		{"AMF", "Network Slice Selection Function"},
		{"UICC", "Date"},
	} {
		if Plausible(c.term, c.expansion) {
			t.Errorf("the 3GPP rule accepted the misalignment %q = %q; it has been loosened past "+
				"the point of catching anything", c.term, c.expansion)
		}
	}

	// 3. THE DIVERGENCE ITSELF, in one line: this pair passes on the 3GPP arm (same
	//    first letter) and is exactly what the ETSI arm's initials rule was written
	//    to refuse. If it ever stops passing here, the arms have been unified.
	if !Plausible("IMSI", "International Organization for Standardization") {
		t.Error("the 3GPP rule now refuses a same-first-letter pair — the two arms have been " +
			"unified on the strict rule, which costs this arm 4 270 correct rows")
	}
}

// TestTheETSIArmStillGuardsItsGlossary reads the SHIPPED Rust miner rather than
// trusting that it still does what this file says.
//
// The lesson is the pipeline's own, learned four times on 2026-09-08: a test that
// asserts what the code SHOULD say, instead of reading what it DOES say, passes
// while the behaviour is gone. The ETSI guard is a `filter` — deleting one line
// removes it and nothing else changes shape.
func TestTheETSIArmStillGuardsItsGlossary(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(root, "rust", "ingest", "src", "bin", "ingest_glossary.rs")
	b, err := os.ReadFile(src)
	if err != nil {
		t.Skipf("the ETSI miner is not in this tree (%v)", err)
	}
	s := string(b)
	if !strings.Contains(s, "fn initials_match(") {
		t.Fatal("initials_match is gone from the ETSI glossary miner: without it, 36.3 % of the " +
			"rows mined from ETSI PDFs carry another term's expansion (measured: 3 264 of 8 995)")
	}
	if !strings.Contains(s, "initials_match(&a.term, &a.expansion)") {
		t.Error("initials_match is still defined but no longer APPLIED — the guard is dead code, " +
			"which is the shape a 'no functional change' cleanup leaves behind")
	}
	// The file-level consistency gate is the second half of the same guard: a whole
	// file whose columns shifted produces rows that individually pass by coincidence.
	//
	// DECLARED IS NOT ENFORCED. Looking only for the identifier would accept a
	// tree where the constant survives and the comparison that uses it is gone —
	// the same dead-code shape the initials_match assertion above guards against,
	// which is exactly how a "no functional change" cleanup removes a gate. So
	// require BOTH the declaration and a use that is a comparison.
	if !strings.Contains(s, "const MIN_FILE_CONSISTENCY") {
		t.Error("the per-file consistency threshold is gone; row-level plausibility alone does not " +
			"catch a file whose every pairing is shifted by one")
	}
	var compared bool
	for _, line := range strings.Split(s, lineBreak) {
		if !strings.Contains(line, "MIN_FILE_CONSISTENCY") || strings.Contains(line, "const ") {
			continue
		}
		if strings.ContainsAny(line, "<>") {
			compared = true
		}
	}
	if !compared {
		t.Error("MIN_FILE_CONSISTENCY is declared but never compared against anything — the " +
			"per-file gate is dead code, and a shifted file is accepted whole")
	}
}

// lineBreak is the newline this file splits Rust source on, named rather than
// inlined so the escape survives every tool that rewrites this file.
const lineBreak = "\n"
