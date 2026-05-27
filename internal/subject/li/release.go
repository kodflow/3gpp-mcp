package li

import (
	"fmt"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/releaseview"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// EventRel is one LI event with the releases it appears in.
type EventRel struct {
	Clause    string   `json:"clause"`
	Heading   string   `json:"event"`
	Interface string   `json:"interface"`
	Releases  []string `json:"releases"`
}

// NFEventView is the release-scoped event list for one NE/NF.
type NFEventView struct {
	NF         string                `json:"nf"`
	Baseline   string                `json:"baseline"`
	Count      int                   `json:"count"`
	Events     []EventRel            `json:"events"` // present in the baseline release
	AddedLater map[string][]EventRel `json:"added_in_later_releases"`
	Note       string                `json:"note"`
}

// NFEventCatalog extracts, from a TS 33.128 clause-6 availability set, the LI
// events of one NE/NF, split into the baseline release and a later-release
// annex. nf is matched case-insensitively as a token of the resolved NF section
// (so "UPF" matches the "SMF/UPF" section, "SGW" matches "SGW/PGW and ePDG").
func NFEventCatalog(avail []store.ClauseRel, ordered []string, baseline, nf string) NFEventView {
	head := make(map[string]string, len(avail))
	rels := make(map[string][]string, len(avail))
	for _, cr := range avail {
		head[cr.Path] = cr.Heading
		rels[cr.Path] = cr.Releases
	}
	nfU := strings.ToUpper(nf)
	view := NFEventView{NF: nfU, Baseline: baseline, AddedLater: map[string][]EventRel{}}
	idx := releaseview.IndexOf(ordered, baseline)
	seen := map[string]bool{}
	for _, sec := range avail { // generation sections
		if !reGenIface.MatchString(sec.Heading) {
			continue
		}
		secNF := nfFromHeading(sec.Heading)
		if secNF == "" {
			secNF = trimLI(head[depth3(sec.Path)])
		}
		if !nfToken(secNF, nfU) {
			continue
		}
		iface := canonIface(reIface.FindString(sec.Heading))
		for _, ch := range avail {
			if !isDirectChild(sec.Path, ch.Path) || reBoiler.MatchString(strings.TrimSpace(ch.Heading)) {
				continue
			}
			if seen[ch.Path] {
				continue
			}
			seen[ch.Path] = true
			e := EventRel{Clause: ch.Path, Heading: strings.TrimSpace(ch.Heading), Interface: iface, Releases: rels[ch.Path]}
			switch {
			case releaseview.Has(e.Releases, baseline):
				view.Events = append(view.Events, e)
			case idx >= 0:
				if first := releaseview.EarliestAfter(e.Releases, ordered, idx); first != "" {
					view.AddedLater[first] = append(view.AddedLater[first], e)
				}
			}
		}
	}
	view.Count = len(view.Events)
	later := 0
	for _, cs := range view.AddedLater {
		later += len(cs)
	}
	if later == 0 {
		view.Note = fmt.Sprintf("%s events in %s: %d. No later-release additions.", nfU, baseline, view.Count)
	} else {
		view.Note = fmt.Sprintf("%s events in %s: %d. ⚠️ Later releases ADD %d more event(s) — see added_in_later_releases.", nfU, baseline, view.Count, later)
	}
	return view
}

// nfToken reports whether want is a slash-separated token of the resolved NF
// section name (case-insensitive).
func nfToken(sectionNF, want string) bool {
	for _, t := range strings.FieldsFunc(strings.ToUpper(sectionNF), func(r rune) bool { return r == '/' || r == ' ' }) {
		if t == want {
			return true
		}
	}
	return strings.EqualFold(sectionNF, want)
}
