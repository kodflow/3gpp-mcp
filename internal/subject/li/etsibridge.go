package li

// etsibridge.go — the LI subject's bridge to the ETSI Lawful-Interception suite.
//
// 3GPP TS 33.128 (X1/X2/X3) and the ETSI LI deliverables (103 221-x, 103 120,
// 102 232-x, 103 280, …) are two independent SDOs — there is NO shared key. The
// relation surfaces two distinct, deliberately SEPARATED ways (the user's call):
//
//   1. CITED — ETSI ids actually mentioned in the TS 33.128 text, mined with a
//      regex and reported as factual citations. Zero curation; only what the spec
//      explicitly references.
//   2. MAPPING (curated) — the domain correspondence X1→103 221-1, X2/X3→103 221-2,
//      HI1→103 120, HI2/HI3→102 232-x, dictionary→103 280, … This is curated LI
//      knowledge (an interface-to-deliverable map), CLEARLY labelled as such so a
//      reader never mistakes it for something mined from the text.
//
// Both resolve through model.EtsiDeliverURL so every entry carries a citable ETSI
// deliver URL — and, once etsi.duckdb is attached at serve time, the cited ids can
// be resolved to the real ETSI clause/version in that index.

import (
	"regexp"
	"sort"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// ETSILink is one curated LI-interface → ETSI-deliverable correspondence.
type ETSILink struct {
	Interface string `json:"interface"` // LI_X1 | LI_X2 | LI_X3 | HI1 | HI2 | HI3 | (common)
	SpecID    string `json:"spec_id"`   // "ETSI TS 103 221-1"
	Title     string `json:"title"`
	Role      string `json:"role"` // why this deliverable governs that interface
	URL       string `json:"url"`  // ETSI /deliver pointer
}

// curatedETSI is the authoritative LI-interface → ETSI-deliverable map. Each entry's
// URL is derived from model.EtsiDeliverURL so it stays consistent with the ETSI ingest.
var curatedETSI = func() []ETSILink {
	rows := []struct{ iface, id, title, role string }{
		{"LI_X1", "103 221-1", "Internal Network Interface X1 for Lawful Interception", "X1 provisioning/tasking from the ADMF to the POIs — the ETSI realisation of 3GPP X1."},
		{"LI_X2", "103 221-2", "Internal Network Interface X2/X3 for Lawful Interception", "X2 (xIRI) transport of intercept-related information to the MDF — the ETSI X2/X3 NWI."},
		{"LI_X3", "103 221-2", "Internal Network Interface X2/X3 for Lawful Interception", "X3 (xCC) transport of communication content to the MDF — the ETSI X2/X3 NWI."},
		{"HI1", "103 120", "Handover Interface HI1 (ADMF task interface)", "HI1 administrative/task interface between the LEMF/ADMF — warrant and task management."},
		{"HI2", "102 232-1", "Handover specification — common parameters (HI2/HI3)", "HI2/HI3 delivery to the LEMF; per-service parts 102 232-2..7 (IP, email, mobile, IMS, …)."},
		{"HI3", "102 232-1", "Handover specification — common parameters (HI2/HI3)", "HI2/HI3 delivery to the LEMF; per-service parts 102 232-2..7 (IP, email, mobile, IMS, …)."},
		{"(common)", "103 280", "Dictionary for common parameters", "Common identifier/parameter dictionary shared across the ETSI LI deliverables."},
		{"(requirements)", "101 331", "Requirements of Law Enforcement Agencies", "LEA requirements underpinning the LI architecture."},
		{"(legacy HI)", "101 671", "Handover interface for the lawful interception of telecommunications traffic", "Legacy handover interface (pre-102 232) still referenced by older deployments."},
	}
	out := make([]ETSILink, 0, len(rows))
	for _, r := range rows {
		out = append(out, ETSILink{
			Interface: r.iface,
			SpecID:    "ETSI TS " + r.id,
			Title:     r.title,
			Role:      r.role,
			URL:       model.EtsiDeliverURL(r.id, ""),
		})
	}
	return out
}()

// CuratedETSILinks returns the curated LI-interface → ETSI-deliverable map, optionally
// narrowed to one interface (LI_X1/LI_X2/LI_X3). An empty filter returns all.
func CuratedETSILinks(ifaceFilter string) []ETSILink {
	if ifaceFilter == "" {
		return append([]ETSILink(nil), curatedETSI...)
	}
	out := make([]ETSILink, 0, 4)
	for _, l := range curatedETSI {
		if l.Interface == ifaceFilter {
			out = append(out, l)
		}
	}
	return out
}

// reETSIRef matches an ETSI deliverable id in prose: an optional "ETSI " prefix, a
// TS/TR/EN/EG token, then the 6-digit (1NN NNN) id with an optional "-P" part. Mirrors
// the cross-reference miner in internal/mcp so the LI bridge and find_cross_references
// agree on what an ETSI reference looks like.
var reETSIRef = regexp.MustCompile(`(?:ETSI\s+)?(?:TS|TR|EN|EG)\s?(1\d{2}\s?\d{3}(?:-\d+)?)`)

// MineCitedETSI returns the DISTINCT, sorted ETSI ids actually referenced in the given
// clauses' text/headings — the factual, cited half of the bridge (no curation). Each id
// is normalised to the "1NN NNN[-P]" form and prefixed "ETSI TS " for display.
func MineCitedETSI(clauses []model.Clause) []string {
	seen := map[string]bool{}
	for _, c := range clauses {
		hay := c.Heading + "\n" + c.Text
		for _, m := range reETSIRef.FindAllStringSubmatch(hay, -1) {
			id := strings.TrimSpace(m[1])
			// normalise "103221-1" / "103 221-1" → "103 221-1"
			id = strings.ReplaceAll(id, " ", "")
			if len(id) >= 6 {
				norm := id[:3] + " " + id[3:]
				seen["ETSI TS "+norm] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
