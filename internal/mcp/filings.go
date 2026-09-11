package mcp

import (
	"sort"
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
		out[i] = strings.Join([]string{c.ClausePath, c.Heading, c.Text}, "\x1f")
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
func filingNote(specID, version string, releases []string, served string, folded bool) string {
	list := strings.Join(releases, ", ")
	if folded {
		return specID + " " + version + " is filed under " + list + " in the 3GPP catalogue, with identical " +
			"text in each: it is served once, cited as " + served + "."
	}
	return specID + " " + version + " is filed under " + list + ", and the copies DIFFER: every copy is " +
		"served, each cited with its own release."
}
