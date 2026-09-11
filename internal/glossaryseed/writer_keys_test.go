package glossaryseed

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// rustGlossary is TS 21.905's writer, whose line rule writerKeys reproduces.
var rustGlossary = filepath.Join("..", "..", "rust", "parse", "src", "glossary.rs")

// THE PATTERN IS THE RUST ONE, translated and nothing else — read from the Rust
// source, so an edit to the writer fails here instead of leaving writerKeys
// answering for a writer that no longer exists. A hand-back re-creates what
// writerKeys says the writer stores; if the two drift, the hand-back invents
// rows or deletes TS 21.905's.
//
// The loop body is held the same way, statement by statement: the trims, the tab
// replacement and the three filters are as much the rule as the pattern is.
func TestWriterLineIsTheRustPattern(t *testing.T) {
	src, err := os.ReadFile(rustGlossary)
	if err != nil {
		t.Fatal(err)
	}
	found := regexp.MustCompile(`Regex::new\(r"([^"]*)"\)`).FindAllStringSubmatch(string(src), -1)
	if len(found) != 1 {
		t.Fatalf("%s holds %d Regex::new(r\"…\") patterns, want the one line pattern", rustGlossary, len(found))
	}
	rust := found[0][1]
	const measured = `^([A-Za-z0-9][A-Za-z0-9._/-]{0,19})(?:\t+|\s{2,})(\S.{2,})$`
	if rust != measured {
		t.Errorf("the writer's pattern is now %q, not %q — writerLine and its tests must follow it", rust, measured)
	}
	translated := strings.ReplaceAll(strings.ReplaceAll(rust, `\S`, `[^`+whiteSpace+`]`), `\s`, `[`+whiteSpace+`]`)
	if writerLine.String() != translated {
		t.Errorf("writerLine is %q, want the Rust pattern with its white-space classes spelled out: %q",
			writerLine.String(), translated)
	}
	for _, stmt := range []string{
		`for line in c.text.split('\n') {`,
		`let line = line.trim_end_matches([' ', '\t']);`,
		`let term = m[1].trim().to_string();`,
		`let exp = m[2].trim().replace('\t', " ");`,
		`if exp.len() < 4 || term.eq_ignore_ascii_case(&exp) || is_all_digits(&exp) {`,
		`!s.is_empty() && s.bytes().all(|b| b.is_ascii_digit())`,
		// THE REGION, not only the line: generalRegion reproduces the writer's
		// heading test and its walk, and the retirement deletes on what it finds.
		`if !c.heading.to_lowercase().contains("abbreviation") {`,
		`if root.is_empty() || root.starts_with("Annex") {`,
		`if !d.clause_path.is_empty() && !is_descendant(&root, &d.clause_path) {`,
	} {
		if !strings.Contains(string(src), stmt) {
			t.Errorf("%s no longer says %q — writerKeys reproduces that statement, and must follow it",
				rustGlossary, stmt)
		}
	}
}

// WRITERKEYS READS TS 21.905 AS ITS WRITER DID — the entries verbatim from
// v19.2.0 as the local corpus holds it on 2026-09-11, each with the key its "21"
// row carries there (or none), plus the edges where Go and Rust would part
// company if the translation were naive.
func TestWriterKeysReadTS21905AsItsWriterDid(t *testing.T) {
	for _, c := range []struct {
		name, text string
		want       []string // "term = expansion"
	}{
		{"the Rust test's own case",
			"AMF\tAccess and Mobility Management Function\nSMF  Session Management Function\nThis document is not an entry",
			[]string{"AMF = Access and Mobility Management Function", "SMF = Session Management Function"}},
		// A wrap is cut at the line: the "21" row is the first line alone.
		{"ADM wraps", "ADM\tAccess condition to an EF which is under the control of the\nauthority which creates this file\n",
			[]string{"ADM = Access condition to an EF which is under the control of the"}},
		// Nothing is collapsed: the "21" rows keep the double space.
		{"CFNRc and SB keep their double space",
			"CFNRc\tCall Forwarding on mobile subscriber  Not Reachable\n\nSB\tSynchronization  Burst",
			[]string{"CFNRc = Call Forwarding on mobile subscriber  Not Reachable", "SB = Synchronization  Burst"}},
		// The pattern refuses a space or an ampersand in a term: no "21" row.
		{"terms the writer refuses",
			"JAR file\tJava Archive File\n\nO&M\tOperations & Maintenance\n\nWLAN UE\tWLAN User Equipment",
			nil},
		// Untabbed alternatives are not entries; the tabbed line is.
		{"SN's untabbed alternatives", "SN\tSerial Number\n\nServing Network\n\nSequence Number",
			[]string{"SN = Serial Number"}},
		// Two expansions of one term, each on its own line: both stored.
		{"EF twice", "EF\tElementary File (on the UICC)\n\nEF\tElementary File",
			[]string{"EF = Elementary File (on the UICC)", "EF = Elementary File"}},
		// Tabs: any run separates, and one inside the expansion becomes a space.
		{"tabs", "AB\t\tAlpha\tBeta \t", []string{"AB = Alpha Beta"}},
		// Rust's \s is Unicode White_Space: two no-break spaces, or two vertical
		// tabs, separate the columns there. Go's \s would not see either.
		{"Unicode white space separates", "AB\u00a0\u00a0Alpha Beta\nCD\v\vCharlie Delta",
			[]string{"AB = Alpha Beta", "CD = Charlie Delta"}},
		// The filters: under four BYTES, equal to the term in ASCII case, all digits.
		{"filters", "AB\tabc\nABCD\tabcd\nN1\t1234\nEF\tEée", []string{"EF = Eée"}},
		// eq_ignore_ascii_case, not Unicode folding: the Kelvin sign is not "K" to
		// the writer, so this row is stored.
		{"the Kelvin sign", "KAB\t\u212aAB", []string{"KAB = \u212aAB"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			for _, k := range writerKeys(c.text) {
				got = append(got, k.Term+" = "+k.Expansion)
			}
			if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
				t.Errorf("writerKeys read\n  %q\nwant what the writer stores\n  %q", got, c.want)
			}
		})
	}
}

// THE TS 21.905 FLOOR FOLLOWS ITS DERIVATION — the numbers measured 2026-09-11
// with writerKeys on every stored version, pinned so that a change to either the
// floor or the reading has to say so here. See ts21905Min.
func TestTS21905FloorFollowsItsDerivation(t *testing.T) {
	const (
		today        = 1300 // v19.2.0
		sinceRel10   = 1255 // v10.3.0 and every version after it
		smallestOfM4 = 95   // M, the smallest of the four largest letter clauses (S 125, C 125, P 101)
	)
	if ts21905Min > sinceRel10 {
		t.Errorf("ts21905Min = %d is above %d, what every version since Rel-10 yields: a sound read "+
			"of an older corpus would be refused", ts21905Min, sinceRel10)
	}
	if ts21905Min <= today-smallestOfM4 {
		t.Errorf("ts21905Min = %d lets a read that lost all of M (%d keys left) pass as sound",
			ts21905Min, today-smallestOfM4)
	}
}
