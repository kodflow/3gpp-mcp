package mcp

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// ONE get_changelog ANSWER WAS A MEGABYTE. Measured on the corpus published
// 2026-09-11 (image sha256:8b1868e6…), over stdio, before this paging existed:
//
//	get_changelog(23.501)             3 064 records   1 013 076 bytes of JSON text
//	get_changelog(24.501)             4 514 records   1 523 645 bytes
//	get_changelog(23.501, Rel-18)       631 records     210 730 bytes
//
// about 331 bytes — roughly 83 tokens — per record. One call on TS 23.501 was a
// quarter of a million tokens, larger than the context most clients can hold, so
// the answer a caller asked for could simply not be read. And it was the common
// case for the specs people ask about: the largest histories are the core ones.
//
// THE DEFAULT PAGE, and why 100. Over the 2 169 specs that carry any record, the
// median holds 11, the 90th percentile 220, and 1 797 of them (82.8 %) hold 100
// or fewer — so most specs still answer in ONE page, whole, exactly as before.
// A page of 100 is ~33 KB, ~8 k tokens. The ceiling a caller may ask for is 500
// (~165 KB, ~41 k tokens): enough to take a release in one go (Rel-18 of 23.501
// is 631, two pages), small enough that one call cannot drown a client again.
const (
	changelogDefaultLimit = 100
	changelogMaxLimit     = 500
)

// sortChanges puts the records in ONE deterministic order before any page is cut.
//
// A PAGE OF AN UNORDERED LIST IS NOT A PAGE. store.GetChangelog orders by
// to_version alone, and 631 records of 23.501 share Rel-18's handful of
// to_versions: within a version, the order is whatever the scan returned, and
// `compact` rewrites the physical order between builds. Offset 100 of one call
// and offset 100 of the next would then be different records, and paging would
// silently skip some and repeat others — the lie this whole file exists to avoid.
//
// So the order is total, over every field a record carries: to_version (the
// store's primary key, compared numerically — "9.0.0" before "18.0.0"), then
// from_version, CR number, revision, and the remaining text. Two records equal on
// all of them are the same record, and their order cannot be observed.
func sortChanges(cs []model.Change) {
	sort.SliceStable(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		if c := compareVersions(a.ToVersion, b.ToVersion); c != 0 {
			return c < 0
		}
		if c := compareVersions(a.FromVersion, b.FromVersion); c != 0 {
			return c < 0
		}
		for _, p := range [][2]string{
			{a.CRNumber, b.CRNumber},
			{fmt.Sprintf("%010d", a.CRRevision), fmt.Sprintf("%010d", b.CRRevision)},
			{a.SpecID, b.SpecID},
			{a.Category, b.Category},
			{a.Meeting, b.Meeting},
			{a.TDocURL, b.TDocURL},
			{a.Summary, b.Summary},
			{strings.Join(a.Clauses, "\x1f"), strings.Join(b.Clauses, "\x1f")},
		} {
			if p[0] != p[1] {
				return p[0] < p[1]
			}
		}
		return false
	})
}

// changelogLimit reads the caller's page size: absent or <= 0 is the default, and
// anything above the ceiling is clamped — and SAID to be clamped, by the caller of
// this function, so a client that asked for 3 000 is not left to assume it got them.
func changelogLimit(asked int) (limit int, clamped bool) {
	switch {
	case asked <= 0:
		return changelogDefaultLimit, false
	case asked > changelogMaxLimit:
		return changelogMaxLimit, true
	default:
		return asked, false
	}
}

// changelogQueryHash binds a cursor to the question it pages through. The bounds
// and the clause filter decide WHICH records are paged, so a cursor minted for one
// range must not be replayed against another: offset 200 of Rel-18 is not offset
// 200 of the whole history. The page size is deliberately NOT part of it — the
// cursor is a record offset, and a caller may change the page size between calls.
func changelogQueryHash(specID string, from, to changelogBound, clause string) string {
	return queryHash("get_changelog", strings.TrimSpace(specID), from.raw, to.raw, clause)
}

// pageNote tells the caller, in words, that the list is a page and how to get the
// rest. A `next_cursor` field alone is easy to miss; a truncated answer that does
// not say so reads as the whole history.
func pageNote(total, offset, returned, limit int, clamped bool, next string) string {
	var b strings.Builder
	if clamped {
		fmt.Fprintf(&b, "limit capped at %d. ", changelogMaxLimit)
	}
	if returned == total && offset == 0 {
		return strings.TrimSpace(b.String())
	}
	if returned == 0 {
		fmt.Fprintf(&b, "%d records match, and this page (from record %d) holds none of them.", total, offset+1)
		return b.String()
	}
	fmt.Fprintf(&b, "%d records match; this page returns records %d-%d (ordered by to_version, then CR number).",
		total, offset+1, offset+returned)
	if next != "" {
		fmt.Fprintf(&b, " Pass cursor=%q for the next %d.", next, min(limit, total-offset-returned))
	}
	return b.String()
}
