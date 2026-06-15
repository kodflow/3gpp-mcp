package li

import (
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// fixtures lifted verbatim from TS 33.128 Rel-19 (via the live MCP) so the prose
// miner is pinned against the REAL normative sentences, not invented ones.
func liFixtures() []model.Clause {
	mk := func(path, heading, text string) model.Clause {
		return model.Clause{SpecID: "33.128", Release: "Rel-19", Version: "19.6.0", ClausePath: path, Heading: heading, Text: text}
	}
	return []model.Clause{
		mk("7.7.2.1.6", "Start of interception with established PDU session",
			"The IRI-POI in the NEF shall generate an xIRI containing an NEFStartOfInterceptionWithEstablishedPDUSession record when..."),
		mk("7.8.2.1.6", "Start of interception",
			"The IRI-POI in the SCEF/IWK-SCEF shall generate an xIRI containing an SCEFStartOfInterceptionWithEstablishedPDNConnection record when..."),
		mk("7.19.3.1", "Charging data",
			"The IRI-POI in the charging function shall only generate xIRI containing ChargingDataEvent record when..."),
		mk("6.3.2.2.1", "MME identifier association",
			"The IRI-POI in the MME shall only generate xIRI containing the MMEIdentifierAssociation and MMEIDentifierDeassociation record when..."),
		mk("7.13.3.6.1.1", "RCS message",
			"The IRI-POI in the RCS Server shall generate the xIRI record for the following..."),
		mk("6.2.3.2.2", "Communication content",
			"The CC-POI in the UPF shall generate xCC containing a UPFCCPDU record when..."),
		mk("6.2.2.2", "LI at AMF", "LI at AMF"), // a section heading with no record prose
	}
}

func TestMinePOIEventsRealProse(t *testing.T) {
	evs := MinePOIEvents(liFixtures())
	got := map[string]Event{}
	for _, e := range evs {
		got[e.NF+"|"+e.Interface+"|"+e.Name] = e
	}
	want := []struct{ nf, iface, name string }{
		{"NEF", "LI_X2", "NEFStartOfInterceptionWithEstablishedPDUSession"},
		{"SCEF/IWK-SCEF", "LI_X2", "SCEFStartOfInterceptionWithEstablishedPDNConnection"},
		{"charging function", "LI_X2", "ChargingDataEvent"},
		{"MME", "LI_X2", "MMEIdentifierAssociation"},
		{"MME", "LI_X2", "MMEIDentifierDeassociation"},
		{"UPF", "LI_X3", "UPFCCPDU"},
	}
	for _, w := range want {
		if _, ok := got[w.nf+"|"+w.iface+"|"+w.name]; !ok {
			t.Errorf("missing mined event: %s / %s / %s", w.nf, w.iface, w.name)
		}
	}
}

func TestNFInventoryCoversClause7AndInterfaces(t *testing.T) {
	inv := NFInventory(liFixtures())
	by := map[string]NFImpact{}
	for _, i := range inv {
		by[i.NF] = i
	}
	// Clause-7 service NFs the ASN.1 core + clause-6 headings missed must appear.
	for _, nf := range []string{"NEF", "SCEF/IWK-SCEF", "charging function", "RCS Server", "MME", "UPF"} {
		if _, ok := by[nf]; !ok {
			t.Errorf("NFInventory missing %q", nf)
		}
	}
	// Interface attribution: an xIRI NF carries LI_X2 + implied LI_X1; an xCC NF LI_X3 + LI_X1.
	nef := by["NEF"]
	if !hasIface(nef.Interfaces, "LI_X2") || !hasIface(nef.Interfaces, "LI_X1") {
		t.Errorf("NEF interfaces=%v, want LI_X1+LI_X2", nef.Interfaces)
	}
	upf := by["UPF"]
	if !hasIface(upf.Interfaces, "LI_X3") || !hasIface(upf.Interfaces, "LI_X1") {
		t.Errorf("UPF interfaces=%v, want LI_X1+LI_X3", upf.Interfaces)
	}
	if by["MME"].EventCount < 2 {
		t.Errorf("MME event_count=%d, want >=2 (assoc+deassoc)", by["MME"].EventCount)
	}
}

func hasIface(list []string, want string) bool {
	for _, x := range list {
		if x == want {
			return true
		}
	}
	return false
}
