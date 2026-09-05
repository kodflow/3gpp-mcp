// Package abbrev reads a 3GPP "Abbreviations" clause and returns the glossary
// entries it declares.
//
// WHY THIS EXISTS. Measured on the shipped corpus, 2026-09-05: asked for the 30
// main 5GC network functions, resolve_term answered CORRECTLY for two, WRONGLY
// for nine, and had no entry at all for nineteen.
//
//	AMF  -> "Authentication Management Field"   (not: Access and Mobility Management Function)
//	UPF  -> "User Port Function"                (not: User Plane Function)
//	SMF  -> "Service Management Function"       (not: Session Management Function)
//	NEF  -> "Network Element Function"          (not: Network Exposure Function)
//
// Not one of those is a typo. The 3GPP half's glossary is seeded ONLY from
// TS 21.905 (1 300 terms), which does not carry the 5GC network-function names;
// the ETSI half then fills the gap from the Abbreviations clauses of its own
// deliverables, where AMF really is an ATM Mapping Function. Every row is
// honestly sourced, and the answer is still confidently wrong — the one thing
// CLAUDE.md §1 says this server must never produce.
//
// THE SOURCE WAS ALWAYS THERE. Every 3GPP spec carries its own Abbreviations
// clause; TS 23.501 §3.2 holds 221 rows and defines every 5GC network function,
// including the ones no prose could yield (LMF, N3IWF, SMSF, UDSF). Nothing
// read it.
//
// AND THE PRECEDENCE IS NORMATIVE, not a ranking invented here. That same clause
// opens by stating it:
//
//	"For the purposes of the present document, the abbreviations given in
//	 TR 21.905 [1] and the following apply. An abbreviation defined in the
//	 present document takes precedence over the definition of the same
//	 abbreviation, if any, in TR 21.905 [1]."
//
// So a spec's own Abbreviations clause outranks the general vocabulary because
// 3GPP says it does. Store.ResolveTerm implements exactly that and nothing more.
//
// WHY PARSED, NOT CURATED. A hand-written list of network functions is correct
// the day it is written and stale the day a release adds the next one — with
// nothing to say so. Reading the clause keeps working across releases without an
// edit, and every entry it yields is justified by a clause a reader can open.
//
// The package is CGO-free and holds the parsing rule only, so it is testable
// without a corpus. The CLI that reads clauses and writes rows is
// cmd/seed-glossary.
package abbrev

import "strings"

// Entry is one declared abbreviation.
type Entry struct {
	Term      string
	Expansion string
}

// maxTermLen bounds what may be read as an abbreviation. The longest real one
// in TS 23.501 §3.2 is "5G-AN PDB" at nine characters; the bound is generous
// enough for the outliers and tight enough that a wrapped sentence cannot pass
// as a term.
const maxTermLen = 16

// Parse returns the abbreviations a clause body declares, in document order and
// deduplicated on (term, expansion).
//
// The body is tab-separated — "AMF\tAccess and Mobility Management Function" —
// with a blank line between entries and an introductory paragraph on top.
//
// TWO SHAPES MUST BE HANDLED OR ENTRIES ARE SILENTLY TRUNCATED:
//
//  1. the intro paragraph has no tabs and must not be read as entries. It is
//     skipped by starting only at the first tabbed line.
//  2. a long expansion WRAPS onto the following line, which then carries no tab
//     of its own. In TS 23.501 §3.2 that is how NSSAAF ends up as "Network
//     Slice-Specific and SNPN" with "Authorization Function" stranded on the
//     next line. A parser that only reads tabbed lines stores the truncated
//     half and looks like it worked, so a continuation is joined to the entry
//     above it.
//
// A THIRD SHAPE: some specs align the columns with SPACES instead of a tab.
// TS 33.501 §3.2 is written that way — "5GC 5G Core Network", "ABBA
// Anti-Bidding down Between Architectures" — and a tab-only parser reads none of
// its 4 700 characters while reporting success. Where the split lands is then a
// guess, because the term itself may contain a space ("5G AV 5G Authentication
// Vector"), so splitFields takes the SHORTEST prefix that Plausible accepts and
// drops the line when nothing does. A row this cannot split unambiguously is
// skipped, never guessed: "NG-RAN 5G Radio Access Network" is lost because its
// expansion does not open on N, and losing it is the right trade against
// inventing a term boundary.
func Parse(text string) []Entry {
	var out []Entry
	seen := make(map[Entry]bool)
	started := false

	text = strings.ReplaceAll(text, "\r\n", "\n")
	// One decision for the whole clause. Mixing the two rules line by line would
	// let a stray tab in a space-aligned clause change how its neighbours parse.
	tabbedClause := strings.Contains(text, "\t")

	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, " \t")
		if strings.TrimSpace(line) == "" {
			continue
		}
		term, exp, tabbed := strings.Cut(line, "\t")
		if !tabbed && !tabbedClause {
			// Space-aligned clause: try to split, and fall through to the
			// continuation rule when the line is not an entry at all.
			if t, e, ok := splitFields(line); ok {
				term, exp, tabbed = t, e, true
			}
		}
		if !tabbed {
			// A continuation of the entry above — but only once entries have
			// begun, or the intro paragraph would be appended to nothing.
			//
			// AND ONLY IN A TABBED CLAUSE. Where a tab marks the columns, a line
			// without one can only be a wrap. Where spaces do, a line that
			// splitFields rejected is far more likely to be an entry this parser
			// could not split — "NG-RAN 5G Radio Access Network" — and gluing it
			// to the row above CORRUPTS a good entry into
			// "Anti-Bidding down Between Architectures NG-RAN 5G Radio Access
			// Network". Skipping it loses one row; joining it silently falsifies
			// another, and the falsified one still looks like a citation.
			if tabbedClause && started && len(out) > 0 {
				if j := join(out[len(out)-1].Expansion, strings.TrimSpace(line)); j != "" {
					delete(seen, out[len(out)-1])
					out[len(out)-1].Expansion = j
					seen[out[len(out)-1]] = true
				}
			}
			continue
		}
		term = strings.TrimSpace(term)
		// Several tabs can separate the columns; everything after the first is
		// part of the expansion.
		exp = strings.TrimSpace(strings.ReplaceAll(exp, "\t", " "))
		exp = strings.Join(strings.Fields(exp), " ")
		if !Plausible(term, exp) {
			continue
		}
		started = true
		e := Entry{Term: term, Expansion: exp}
		if seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	return out
}

// splitFields splits a space-aligned row into term and expansion.
//
// The boundary is not marked, so it is DERIVED rather than assumed: the shortest
// leading run of words that Plausible accepts as the term of what follows wins.
// Shortest, because a term is short by nature and a longer prefix would eat the
// front of the expansion — "5GC | 5G Core Network" is right and "5GC 5G | Core
// Network" is not, and only the length rule separates them.
//
// maxTermWords bounds the search at three: "5G HE AV" is the longest real term
// of this shape, and letting it run further turns a prose line into a term with
// a plausible-looking tail.
func splitFields(line string) (term, expansion string, ok bool) {
	w := strings.Fields(line)
	const maxTermWords = 3
	for n := 1; n <= maxTermWords && n < len(w); n++ {
		t := strings.Join(w[:n], " ")
		e := strings.Join(w[n:], " ")
		if Plausible(t, e) {
			return t, e, true
		}
	}
	return "", "", false
}

// join appends a wrapped tail to an expansion, refusing anything that does not
// read as a continuation: a tail that starts a new sentence, or that is long
// enough to be a paragraph, is prose the clause happens to carry rather than the
// rest of an entry.
func join(head, tail string) string {
	if head == "" || tail == "" || len(tail) > 60 {
		return ""
	}
	if strings.HasSuffix(head, ".") {
		return ""
	}
	return head + " " + strings.Join(strings.Fields(tail), " ")
}

// Plausible reports whether a parsed row can be believed.
//
// THIS IS THE GUARD. The columns are tab-separated, so there is no clever
// extraction to get wrong — what goes wrong is alignment: a stray tab inside the
// intro, a table whose columns shifted, a heading that happens to contain one.
// Those all produce a row where the "term" is not an abbreviation of the
// "expansion", and a glossary full of those is worse than an empty one because
// each looks checkable and is not.
//
// The rule is deliberately the weakest one that catches misalignment: the term
// and the expansion must START WITH THE SAME CHARACTER. Measured against all 221
// rows of TS 23.501 §3.2, not one disagrees — while a shifted column disagrees
// almost always.
//
// It is NOT the stricter "the acronym is the initials of the words". 3GPP does
// not name things that way: NWDAF is Net-Work Data Analytics Function, with the
// W taken from inside the first word, and 5GC is 5G Core Network, whose final C
// is not the last word's initial. A rule that demanded initials would reject the
// correct answers — which is precisely the failure being repaired here.
func Plausible(term, expansion string) bool {
	if term == "" || expansion == "" {
		return false
	}
	if len(term) > maxTermLen || len(expansion) < 2 {
		return false
	}
	// An abbreviation is a token, not a sentence.
	if strings.Count(term, " ") > 2 || strings.ContainsAny(term, ".,;:()[]") {
		return false
	}
	// It has to carry at least one capital or digit; a lowercase word is prose.
	if !strings.ContainsFunc(term, func(r rune) bool {
		return (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
	}) {
		return false
	}
	return foldFirst(term) == foldFirst(expansion)
}

// foldFirst returns the first alphanumeric character, upper-cased.
func foldFirst(s string) rune {
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
	}
	return 0
}
