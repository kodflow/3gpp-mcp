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
	// reNFsectAny matches an NE/NF interception section anywhere LI defines one: the
	// clause-6 per-NF sections (6.x.y) AND the clause-7 service-specific sections
	// (7.7 NEF, 7.8 SCEF, 7.9 AKMA, …) the clause-6-only reNFsect missed.
	reNFsectAny = regexp.MustCompile(`^[67]\.\d+(\.\d+)?$`)
	reHeadNF    = regexp.MustCompile(`(?i)^LI (?:at|for|support at|in) `)
	reIfaceAll  = regexp.MustCompile(`(?i)li_?x1|li_?x2|li_?x3`)
)

// NFImpact summarises one NE/NF's Lawful-Interception footprint: which interception
// interfaces touch it (subset of LI_X1/LI_X2/LI_X3) and how many distinct events it
// reports, with a citation to a defining clause. This is the "who is impacted by
// X1/X2/X3" inventory — far broader than the clause-6 ASN.1 IRI core: it covers every
// clause-7 service NF (NEF, SCEF/IWK-SCEF, AKMA AAnF/AF, LMF, EES, charging function,
// MCData/PTC/RCS/MMS servers, …) by mining the canonical normative prose.
type NFImpact struct {
	NF         string         `json:"nf"`
	Domain     string         `json:"domain"`
	Interfaces []string       `json:"interfaces"` // sorted subset of LI_X1 / LI_X2 / LI_X3
	EventCount int            `json:"event_count"`
	Clause     string         `json:"clause"`
	Citation   model.Citation `json:"citation"`
}

// rePOI is the AUTHORITATIVE event-attribution sentence TS 33.128 repeats for every
// NF across clauses 6 AND 7: "The IRI-POI [present] in the <NF> shall [only] generate
// [an|the] xIRI [containing … <Record> record]" (and the CC-POI/xCC variant). Group 1
// = IRI|CC (→ LI_X2/LI_X3), group 2 = NF, group 3 = xIRI|xCC, group 4 = the tail we
// scan for the <Record> type(s). This is literal-prose mining, not heading heuristics,
// so it catches every service NF the ASN.1 core + clause-6 headings miss.
var (
	rePOI       = regexp.MustCompile(`(?i)\b(IRI|CC)-POI\s+(?:present\s+)?in the\s+(.{2,40}?)\s+shall\s+(?:only\s+)?generate\s+(?:an?\s+|the\s+)?(xIRI|xCC)\b([^.]{0,200})`)
	reRecordTok = regexp.MustCompile(`[A-Z][A-Za-z0-9]{4,}`) // record-type CamelCase token (≥5 chars)
	reWS        = regexp.MustCompile(`\s+`)
)

// MinePOIEvents extracts the LI events from the canonical POI prose across ALL of TS
// 33.128 (clauses 6, 7 and annexes), attributing each to its NF + interface (xIRI →
// LI_X2, xCC → LI_X3) and naming the ASN.1 record it carries. Literal-prose, citable,
// dedupe per (NF, interface, record). This is what makes li_events comprehensive.
func MinePOIEvents(clauses []model.Clause) []Event {
	var out []Event
	seen := map[string]bool{}
	for _, c := range clauses {
		hay := reWS.ReplaceAllString(c.Heading+" "+c.Text, " ")
		for _, m := range rePOI.FindAllStringSubmatch(hay, -1) {
			nf := cleanNF(m[2])
			if nf == "" {
				continue
			}
			iface := "LI_X2"
			if strings.EqualFold(m[3], "xCC") {
				iface = "LI_X3"
			}
			// Record type(s) the POI emits: scan the enumeration up to the first
			// "record" word ("the X and Y record" → X, Y; "an XyZ record" → XyZ).
			tail := m[4]
			if i := strings.Index(strings.ToLower(tail), "record"); i >= 0 {
				tail = tail[:i]
			}
			names := reRecordTok.FindAllString(tail, -1)
			if len(names) == 0 {
				names = []string{""} // POI prose with no explicit record type → one generic event
			}
			for _, name := range names {
				key := strings.ToUpper(nf) + "\x00" + iface + "\x00" + name
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, Event{
					NF: nf, Domain: domainOf(c.ClausePath), Name: name, Interface: iface,
					Source: "33.128/prose", Citation: c.Cite(),
				})
			}
		}
	}
	return out
}

// NFInventory returns EVERY NE/NF that TS 33.128 impacts over X1/X2/X3, each tagged
// with the interfaces touching it and its event count. It unions two sources: the
// authoritative POI prose (MinePOIEvents — covers clause-7 services) and the clause
// "LI at/for <NF>" section headings (so a provisioning-only NF with no record prose
// still appears). LI_X1 is implied for any NF with an X2/X3 POI (every POI is tasked
// over LI_X1 — TS 33.128 §5.2). Cite-or-silent: an NF with no citable clause is dropped.
func NFInventory(clauses []model.Clause) []NFImpact {
	byNF := map[string]*NFImpact{}
	order := []string{}
	get := func(nf, clausePath string, cite model.Citation) *NFImpact {
		key := strings.ToUpper(nf)
		imp := byNF[key]
		if imp == nil {
			imp = &NFImpact{NF: nf, Domain: domainOf(clausePath), Clause: clausePath, Citation: cite}
			byNF[key] = imp
			order = append(order, key)
		}
		return imp
	}
	// 1) Prose events: the comprehensive source (NF + interface + per-event count).
	count := map[string]map[string]bool{} // nf -> set of interface\x00name
	for _, e := range MinePOIEvents(clauses) {
		imp := get(e.NF, e.Citation.Clause, e.Citation)
		imp.Interfaces = unionSorted(imp.Interfaces, []string{e.Interface})
		if count[strings.ToUpper(e.NF)] == nil {
			count[strings.ToUpper(e.NF)] = map[string]bool{}
		}
		count[strings.ToUpper(e.NF)][e.Interface+"\x00"+e.Name] = true
	}
	// 2) Section headings: add NFs that have an "LI at/for X" section but no record prose.
	for _, c := range clauses {
		if !reNFsectAny.MatchString(c.ClausePath) || !reHeadNF.MatchString(strings.TrimSpace(c.Heading)) {
			continue
		}
		nf := cleanNF(trimLI(c.Heading))
		if nf == "" {
			continue
		}
		imp := get(nf, c.ClausePath, c.Cite())
		imp.Interfaces = unionSorted(imp.Interfaces, mineInterfaces(c.ClausePath, clauses))
	}
	out := make([]NFImpact, 0, len(order))
	for _, k := range order {
		imp := byNF[k]
		imp.Interfaces = unionSorted(imp.Interfaces, nil) // X1-implied normalisation
		imp.EventCount = len(count[k])
		out = append(out, *imp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NF < out[j].NF })
	return out
}

// POIEventsForNF returns the mined POI events whose NF matches want (case-insensitive
// token match, so "UPF" matches "SMF/UPF" and "SCEF" matches "SCEF/IWK-SCEF"). This is
// the prose-miner fallback the li_events tool uses for NFs absent from the ASN.1 core
// (NEF, SCEF, AKMA, charging function, edge/MC services …).
func POIEventsForNF(clauses []model.Clause, want string) []Event {
	var out []Event
	for _, e := range MinePOIEvents(clauses) {
		if nfToken(e.NF, strings.ToUpper(want)) {
			out = append(out, e)
		}
	}
	return out
}

// cleanNF normalises a mined NF name: collapse whitespace, trim, drop a leading
// article/qualifier and surrounding punctuation. Keeps multiword names ("charging
// function", "RCS Server", "SCEF/IWK-SCEF") and rejects empties / stop tokens.
func cleanNF(s string) string {
	s = strings.TrimSpace(reWS.ReplaceAllString(s, " "))
	s = strings.Trim(s, " .,:;()")
	s = strings.TrimPrefix(s, "the ")
	s = strings.TrimPrefix(s, "The ")
	if s == "" || nfStop[strings.ToUpper(s)] {
		return ""
	}
	return s
}

// mineInterfaces returns the LI interfaces (subset of LI_X1/LI_X2/LI_X3) named in a
// section and its descendants. LI_X1 is implied once LI_X2 or LI_X3 is present (the
// POI is provisioned over LI_X1) — the standard 5GC-LI reference architecture.
func mineInterfaces(secPath string, clauses []model.Clause) []string {
	set := map[string]bool{}
	for _, c := range clauses {
		if c.ClausePath != secPath && !strings.HasPrefix(c.ClausePath, secPath+".") {
			continue
		}
		hay := c.Heading + " " + c.Text
		for _, m := range reIfaceAll.FindAllString(hay, -1) {
			set[canonIface(m)] = true
		}
		if reGenIface.MatchString(c.Heading) {
			set[canonIface(reIface.FindString(c.Heading))] = true
		}
	}
	// X1 provisioning is implied by the presence of any X2/X3 POI.
	if set["LI_X2"] || set["LI_X3"] {
		set["LI_X1"] = true
	}
	out := []string{}
	for _, i := range []string{"LI_X1", "LI_X2", "LI_X3"} {
		if set[i] {
			out = append(out, i)
		}
	}
	return out
}

// unionSorted merges two interface lists, deduped, in the canonical X1<X2<X3 order.
// It implies LI_X1 whenever LI_X2 or LI_X3 is present: every IRI/CC POI is tasked and
// provisioned over LI_X1 (TS 33.128 §5.2), so an X2/X3-touched NF is X1-impacted too.
func unionSorted(a, b []string) []string {
	set := map[string]bool{}
	for _, x := range a {
		set[x] = true
	}
	for _, x := range b {
		set[x] = true
	}
	if set["LI_X2"] || set["LI_X3"] {
		set["LI_X1"] = true
	}
	out := []string{}
	for _, i := range []string{"LI_X1", "LI_X2", "LI_X3"} {
		if set[i] {
			out = append(out, i)
		}
	}
	return out
}

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
