// Package releaseview computes release-scoped views over clause availability:
// what a spec/clause contains in a baseline release, what that release
// introduced vs the previous one, and — as an annex the caller surfaces — what
// LATER releases add or what is already obsolete. It is generic (any spec), used
// by the core get_spec tool and reused by domain subjects. Lives outside any
// vertical so the core does not depend on a subject package.
package releaseview

import (
	"fmt"
	"sort"

	"github.com/kodflow/3gpp-mcp/internal/store"
)

// ReleaseView is a release-scoped answer over a set of clauses.
type ReleaseView struct {
	Spec          string                       `json:"spec_id"`
	Baseline      string                       `json:"baseline"`
	InBaseline    []store.ClauseRel            `json:"in_baseline"`
	NewInBaseline []store.ClauseRel            `json:"new_in_baseline"`         // vs previous release
	AddedLater    map[string][]store.ClauseRel `json:"added_in_later_releases"` // release -> clauses absent from baseline
	RemovedBefore []store.ClauseRel            `json:"removed_before_baseline"` // present only in EARLIER releases (obsolete by baseline)
	Note          string                       `json:"note"`
}

// BuildReleaseView splits clause availability around the baseline release.
// ordered is oldest-first (e.g. [Rel-17 Rel-18 Rel-19]).
func BuildReleaseView(spec string, avail []store.ClauseRel, ordered []string, baseline string) ReleaseView {
	idx := IndexOf(ordered, baseline)
	var prev string
	if idx > 0 {
		prev = ordered[idx-1]
	}
	rv := ReleaseView{Spec: spec, Baseline: baseline, AddedLater: map[string][]store.ClauseRel{}}
	for _, cr := range avail {
		switch {
		case Has(cr.Releases, baseline):
			rv.InBaseline = append(rv.InBaseline, cr)
			if prev != "" && !Has(cr.Releases, prev) {
				rv.NewInBaseline = append(rv.NewInBaseline, cr)
			}
		case idx >= 0:
			// not in baseline: either added later, or only in earlier releases
			// (i.e. removed before the baseline = obsolete from its standpoint).
			if first := EarliestAfter(cr.Releases, ordered, idx); first != "" {
				rv.AddedLater[first] = append(rv.AddedLater[first], cr)
			} else {
				rv.RemovedBefore = append(rv.RemovedBefore, cr)
			}
		}
	}
	rv.Note = noteFor(rv)
	return rv
}

func noteFor(rv ReleaseView) string {
	n := 0
	rels := make([]string, 0, len(rv.AddedLater))
	for r, cs := range rv.AddedLater {
		n += len(cs)
		rels = append(rels, fmt.Sprintf("%s:+%d", r, len(cs)))
	}
	sort.Strings(rels)
	parts := ""
	if n > 0 {
		parts += fmt.Sprintf(" ⚠️ Later releases ADD %d element(s) not in your baseline (%v) — see added_in_later_releases.", n, rels)
	}
	if len(rv.RemovedBefore) > 0 {
		parts += fmt.Sprintf(" ⚠️ %d element(s) existed in EARLIER releases but are gone by %s (obsolete) — see removed_before_baseline.", len(rv.RemovedBefore), rv.Baseline)
	}
	if parts == "" {
		return fmt.Sprintf("Scoped to %s. No later-release additions or removals under this scope.", rv.Baseline)
	}
	return "Scoped to " + rv.Baseline + "." + parts
}

// IndexOf returns the position of v in xs, or -1.
func IndexOf(xs []string, v string) int {
	for i, x := range xs {
		if x == v {
			return i
		}
	}
	return -1
}

// Has reports whether v is in xs.
func Has(xs []string, v string) bool { return IndexOf(xs, v) >= 0 }

// EarliestAfter returns the first release in ordered[afterIdx+1:] that is in rels.
func EarliestAfter(rels, ordered []string, afterIdx int) string {
	for i := afterIdx + 1; i < len(ordered); i++ {
		if Has(rels, ordered[i]) {
			return ordered[i]
		}
	}
	return ""
}
