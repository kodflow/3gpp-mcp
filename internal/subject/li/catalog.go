// Package li builds a traceable Lawful-Interception event catalogue from the
// normative specs (TS 33.128 for 5GC/EPS-over-5G-LI, TS 33.108 for legacy
// 3G/EPS/IMS). Every event carries its source clause citation so the catalogue
// can act as an arbiter ("here is exactly what the spec says, clause by clause"),
// not a hand-curated approximation.
//
// Confidence is explicit per event (Source):
//   - "33.128/clause"  : event is a named sub-clause heading — high confidence,
//     literal parse.
//   - "33.108/prose"   : event is derived from a prose trigger bullet under a
//     record clause (BEGIN/CONTINUE/END/REPORT) — best-effort, lower confidence,
//     because 33.108 does not enumerate events as clean tables.
//
// NE/NF attribution for 33.108 (which is organised by domain, not by NE) goes
// through an explicit, versioned domain→NE mapping (see mapping.go), so the
// interpretive step is traceable, never magic.
package li

import (
	"regexp"
	"sort"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// Event is one LI event a network element/function reports, with provenance.
type Event struct {
	NF        string         `json:"nf"`        // "AMF", "MME", ...
	Domain    string         `json:"domain"`    // "5GC" | "EPC" | "IMS" | "3G" | ...
	Name      string         `json:"name"`      // event heading / trigger phrase
	Interface string         `json:"interface"` // "LI_X2" | "LI_HI2" | "LI_X3" | ...
	Source    string         `json:"source"`    // "33.128/clause" | "33.108/prose"
	Citation  model.Citation `json:"citation"`
}

// NFEvents groups a network function's distinct events.
type NFEvents struct {
	NF     string  `json:"nf"`
	Domain string  `json:"domain"`
	Count  int     `json:"count"`
	Events []Event `json:"events"`
}

var (
	reGenIface = regexp.MustCompile(`(?i)generation of x?(iri|cc) .*over\s+(li_?x2|li_?x3|li_?hi2|li_?hi3)`)
	reIface    = regexp.MustCompile(`(?i)(li_?x2|li_?x3|li_?hi2|li_?hi3)`)
	reBoiler   = regexp.MustCompile(`(?i)^(general|void|introduction|common.*|.*simple data types.*|definitions? for .*|.*message types?|provisioning over.*)$`)
	reNFin     = regexp.MustCompile(`(?i)\b(?:at|in)\s+(?:the\s+|a\s+|IRI-POI\s+(?:in\s+the\s+|in\s+)?|CC-POI\s+(?:in\s+the\s+|in\s+)?)*([A-Z][A-Za-z0-9/-]{1,9})`)
	reNFsect   = regexp.MustCompile(`^6\.\d+\.\d+$`)
	nfStop     = map[string]bool{"IRI": true, "POI": true, "LI": true, "CC": true, "TF": true, "THE": true, "IRI-POI": true, "CC-POI": true, "UE": true}
)

// Extract128 builds the high-confidence event list from a TS 33.128 clause-6
// clause set: for every "Generation of xIRI/xCC over LI_<iface>" section it
// collects the leaf event sub-clauses (excluding boilerplate), attributing each
// to the NF named in the section heading or its depth-3 ancestor NF section.
// Events are deduplicated per (NF, Name) across interfaces.
func Extract128(clauses []model.Clause) []NFEvents {
	head := make(map[string]string, len(clauses))
	for _, c := range clauses {
		head[c.ClausePath] = c.Heading
	}
	// per NF: name -> Event (first/highest-interface wins), preserving order
	type acc struct {
		domain string
		order  []string
		byName map[string]Event
	}
	nfs := map[string]*acc{}
	getNF := func(name, domain string) *acc {
		a := nfs[name]
		if a == nil {
			a = &acc{domain: domain, byName: map[string]Event{}}
			nfs[name] = a
		}
		return a
	}

	for _, c := range clauses {
		if !reGenIface.MatchString(c.Heading) {
			continue
		}
		iface := canonIface(reIface.FindString(c.Heading))
		nf := nfFromHeading(c.Heading)
		anc := depth3(c.ClausePath)
		if nf == "" {
			nf = trimLI(head[anc])
		}
		domain := domainOf(c.ClausePath)
		a := getNF(nf, domain)
		for _, child := range clauses {
			if !isDirectChild(c.ClausePath, child.ClausePath) {
				continue
			}
			name := strings.TrimSpace(child.Heading)
			if name == "" || reBoiler.MatchString(name) {
				continue
			}
			if _, dup := a.byName[name]; dup {
				continue // same event, another interface — dedupe per NF
			}
			a.order = append(a.order, name)
			a.byName[name] = Event{
				NF: nf, Domain: domain, Name: name, Interface: iface,
				Source: "33.128/clause", Citation: child.Cite(),
			}
		}
	}

	out := make([]NFEvents, 0, len(nfs))
	for nf, a := range nfs {
		evs := make([]Event, 0, len(a.order))
		for _, n := range a.order {
			evs = append(evs, a.byName[n])
		}
		out = append(out, NFEvents{NF: nf, Domain: a.domain, Count: len(evs), Events: evs})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NF < out[j].NF })
	return out
}

// NFSections returns the NE/NF that TS 33.128 clause 6 defines an interception
// section for ("LI at AMF" -> AMF), each cited — the official 5GC/EPS NF list.
func NFSections(clauses []model.Clause) []Event {
	var out []Event
	for _, c := range clauses {
		if reNFsect.MatchString(c.ClausePath) && strings.HasPrefix(strings.TrimSpace(c.Heading), "LI ") {
			out = append(out, Event{
				NF: trimLI(c.Heading), Domain: domainOf(c.ClausePath),
				Name: c.Heading, Source: "33.128/clause", Citation: c.Cite(),
			})
		}
	}
	return out
}

func canonIface(s string) string {
	s = strings.ToUpper(strings.ReplaceAll(s, "LI", "LI_"))
	s = strings.ReplaceAll(s, "LI__", "LI_")
	if !strings.HasPrefix(s, "LI_") && s != "" {
		s = "LI_" + s
	}
	return s
}

func nfFromHeading(h string) string {
	for _, m := range reverse(reNFin.FindAllStringSubmatch(h, -1)) {
		cand := strings.ToUpper(m[1])
		if !nfStop[cand] {
			return cand
		}
	}
	return ""
}

func domainOf(path string) string {
	switch {
	case strings.HasPrefix(path, "6.2"):
		return "5GC"
	case strings.HasPrefix(path, "6.3"):
		return "EPC"
	case strings.HasPrefix(path, "6.4"):
		return "3G"
	default:
		return ""
	}
}

func trimLI(h string) string {
	return strings.TrimSpace(strings.NewReplacer(
		"LI at ", "", "LI for ", "", "LI support at ", "", "LI in ", "").Replace(h))
}

func depth3(path string) string {
	p := strings.Split(path, ".")
	if len(p) >= 3 {
		return strings.Join(p[:3], ".")
	}
	return path
}

func isDirectChild(parent, child string) bool {
	if !strings.HasPrefix(child, parent+".") {
		return false
	}
	return !strings.Contains(child[len(parent)+1:], ".")
}

func reverse(in [][]string) [][]string {
	for i, j := 0, len(in)-1; i < j; i, j = i+1, j-1 {
		in[i], in[j] = in[j], in[i]
	}
	return in
}
