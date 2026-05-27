package li

import (
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// synthetic TS 33.128-shaped clause-6 tree (no corpus needed).
func liFixture() []model.Clause {
	mk := func(path, heading string) model.Clause {
		return model.Clause{SpecID: "33.128", Release: "Rel-19", Version: "19.6.0", ClausePath: path, Heading: heading}
	}
	return []model.Clause{
		mk("6.2", "5G"),
		mk("6.2.2", "LI at AMF"),
		mk("6.2.2.2", "Generation of xIRI over LI_X2"),
		mk("6.2.2.2.1", "General"),                    // boilerplate -> excluded
		mk("6.2.2.2.1A", "Simple data types for AMF"), // boilerplate -> excluded
		mk("6.2.2.2.2", "Registration"),
		mk("6.2.2.2.3", "Handovers"),
		mk("6.2.2.2.2.1", "sub-detail"), // grandchild -> not a direct child
		mk("6.2.3", "LI for SMF/UPF"),
		mk("6.2.3.5", "Generation of xIRI at UPF over LI_X2"),
		mk("6.2.3.5.1", "Packet header reporting"),
		mk("6.2.3.5.2", "Fragmentation"),
	}
}

func TestExtractLIX2Events(t *testing.T) {
	nfs := ExtractLIX2Events(liFixture())
	got := map[string]X2NFEvents{}
	for _, n := range nfs {
		got[n.NF] = n
	}

	amf, ok := got["AMF"]
	if !ok {
		t.Fatalf("AMF not found; got %v", keys(got))
	}
	if amf.Count != 2 { // Registration, Handovers (General + data types + grandchild excluded)
		t.Errorf("AMF count = %d, want 2 (events: %v)", amf.Count, headings(amf))
	}
	if amf.X2Section != "6.2.2.2" || amf.NFSection != "6.2.2" {
		t.Errorf("AMF sections wrong: x2=%s nf=%s", amf.X2Section, amf.NFSection)
	}
	if amf.Citation.SpecID != "33.128" || amf.Citation.Clause != "6.2.2.2" || amf.Citation.URL == "" {
		t.Errorf("AMF citation incomplete: %+v", amf.Citation)
	}

	upf, ok := got["UPF"] // NF resolved from the X2 heading ("at UPF")
	if !ok || upf.Count != 2 {
		t.Errorf("UPF: ok=%v count=%d, want 2", ok, upf.Count)
	}
}

func keys(m map[string]X2NFEvents) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func headings(n X2NFEvents) []string {
	var out []string
	for _, e := range n.Events {
		out = append(out, e.Heading)
	}
	return out
}
