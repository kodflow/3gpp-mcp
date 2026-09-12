package mcp

import (
	"sort"
	"strconv"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// ONE VERSION, FILED UNDER SEVERAL RELEASES, IS ONE DOCUMENT — SERVE IT ONCE.
//
// The 3GPP catalogue files some versions under more than one release: a draft TR
// listed in each release its study spanned (30.531 1.62.0 under nine, Rel-11 to
// Rel-19), a spec whose version was carried into the next release's column
// (22.890 0.7.0 under Rel-17 and Rel-18). The corpus keeps one copy of the text
// per filing, and Store.GetClauses selects by spec and version only, so it hands
// back every copy. Measured with the real server over the published corpus,
// 2026-09-11: get_spec(22.890, release=Rel-17) returned 328 clauses — each of
// the 164 twice, half of them cited as Rel-18 — and resources/read of
// 3gpp://30.531/Rel-12@1.62.0 returned 1.96 MB, the same 64 clauses nine times.
//
// Over the published 3GPP corpus, 35 (spec, version) pairs are filed under
// several releases, 22 of them with clauses under more than one, and in all 22
// the copies are IDENTICAL (same clause paths, headings and text). So the
// honest answer is the document once, and the list of releases it is filed
// under — which is what the catalogue says, and what a reader needs to cite it.
//
// Only identical copies are folded. Copies that differed would be two documents
// sharing a version number, and choosing one would hide the other: they are
// served as they come, and the caller still gets filed_under.
//
// Served here rather than in Store.GetClauses: internal/store is in the Impl of
// the validate steps, and the fold is a presentation rule, not a query.
//
// AMENDING #347's ONE LEFT-AS-MEASURED CASE. This file shipped saying of 26.510
// v18.4.0, filed under Rel-20 with its text stored only there: "That is what the
// DynaReport row says; changing it is a write-side decision, not a serve-time
// one." The second half held. The first did not. Measured 2026-09-12: today's
// status report does not file 26.510 under Rel-20 at all. The corpus was
// carrying a filing the catalogue had WITHDRAWN — 74 rows had a release their
// version's major contradicts, 56 of them still attested and legitimate (a
// 3.x.y Rel-99 document filed under Rel-4 that Rel-4 never re-issued IS the
// Rel-4 text), and 16 not. So it was never a catalogue fact reported faithfully;
// it was an append-only corpus holding a fact the catalogue had dropped, because
// nothing in the write path retires a filing.
//
// PR #350 stopped new ones being written, and cmd/repair-release-filing moved
// the twelve that carried text — 4 112 occurrences, in every case the only copy
// of that version in the corpus — to the release each version names. 26.510
// v18.4.0 now serves 364 clauses under Rel-18, and Rel-20 keeps the catalogue's
// bookkeeping row with no text behind it. The fold below is unchanged: it was
// never the thing at fault. See docs/diagnostics/release-filing-anomaly.md.

// oneFiling returns the clauses of a single filing of one version, and — when
// the version is filed under more than one release — every release it is filed
// under, in release order. prefer is the release the caller asked for; when the
// version is not filed there, the release its major version names is used, and
// failing that the earliest filing.
func oneFiling(clauses []model.Clause, prefer string) ([]model.Clause, []string) {
	byRelease := map[string][]model.Clause{}
	version := ""
	for i, c := range clauses {
		if i == 0 {
			version = c.Version
		} else if c.Version != version {
			return clauses, nil // several versions: not one document
		}
		byRelease[c.Release] = append(byRelease[c.Release], c)
	}
	if len(byRelease) < 2 {
		return clauses, nil
	}
	releases := make([]string, 0, len(byRelease))
	for r := range byRelease {
		releases = append(releases, r)
	}
	sortReleases(releases)

	first := textsOf(byRelease[releases[0]])
	for _, r := range releases[1:] {
		if !sameTexts(first, textsOf(byRelease[r])) {
			return clauses, releases
		}
	}

	keep := releases[0]
	if _, ok := byRelease[prefer]; ok {
		keep = prefer
	} else if major := model.VersionMajor(version); major >= 3 {
		if r := model.ReleaseFromMajor(major); byRelease[r] != nil {
			keep = r
		}
	}
	return byRelease[keep], releases
}

// sameRelease reports whether every clause carries the same release.
func sameRelease(cs []model.Clause) bool {
	for _, c := range cs[1:] {
		if c.Release != cs[0].Release {
			return false
		}
	}
	return true
}

// sortReleases orders release labels by their ordinal; a label with none (a
// draft bucket, "GSM") sorts first, alphabetically.
func sortReleases(rs []string) {
	sort.Slice(rs, func(i, j int) bool {
		oi, iok := model.ReleaseOrdinal(rs[i])
		oj, jok := model.ReleaseOrdinal(rs[j])
		if iok != jok {
			return !iok
		}
		if oi != oj {
			return oi < oj
		}
		return rs[i] < rs[j]
	})
}

// textsOf is a filing's content as a sorted multiset. Sorted, because the order
// GetClauses returns rows that share a clause path is not fixed, and two
// identical copies must not compare unequal over it.
func textsOf(cs []model.Clause) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		// is_normative is part of what get_spec returns per clause, so two filings
		// that disagree on it are not interchangeable (Qodo, #347).
		out[i] = strings.Join([]string{c.ClausePath, c.Heading, c.Text, strconv.FormatBool(c.IsNormative)}, "\x1f")
	}
	sort.Strings(out)
	return out
}

func sameTexts(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// filingNote says what filed_under means for the answer.
//
// scope is what was actually compared. A clause filter is applied by the store
// BEFORE these rows reach the fold, so on a filtered call the comparison covers
// the returned clauses and not the whole document, and the note must not claim
// more than it checked (Qodo, #347).
func filingNote(specID, version string, releases []string, served string, folded bool, scope string) string {
	list := strings.Join(releases, ", ")
	if folded {
		return specID + " " + version + " is filed under " + list + " in the 3GPP catalogue, and " + scope +
			" is identical in each: it is served once, cited as " + served + "."
	}
	return specID + " " + version + " is filed under " + list + ", and " + scope + " DIFFERS between them: " +
		"every copy is served, each cited with its own release."
}

// filingScope names what a call compared: the whole document, or the clauses a
// clause filter left.
func filingScope(clause string) string {
	if strings.TrimSpace(clause) == "" {
		return "the text"
	}
	return "the text under clause " + clause
}
