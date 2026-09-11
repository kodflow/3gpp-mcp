package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// A RESOURCE URI WRITTEN THE WAY ITS TEMPLATE DESCRIBES IT MUST BE READABLE.
//
// The whole-spec template was described as "3gpp://<spec_id>/<release>[@<version>]"
// and was the single template `3gpp://{spec_id}/{release}`, whose {release} cannot
// match "@": every URI written with a version answered "handler not found for
// resource URI", on both halves — and for an ETSI deliverable, whose release is
// the constant "ETSI", the version is the only way to name one publication.
//
// Driven over the real stdio transport, and each read checks WHICH version came
// back: a template that matched and then served the newest version would pass a
// test that only looked for "no error".
func TestAWholeSpecURIWithAVersionReadsThatVersion(t *testing.T) {
	st, e := memStore(t), memStore(t)
	for _, v := range []struct{ ver, text string }{
		{"19.5.0", "the registration text as it read in 19.5.0"},
		{"19.6.0", "the registration text as it reads in 19.6.0"},
	} {
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "33.128", Release: "Rel-19", Version: v.ver})
		if err := st.InsertClauses([]model.Clause{{ChunkID: chunkOf(v.ver), SpecID: "33.128", Release: "Rel-19",
			Version: v.ver, ClausePath: "2", Heading: "References", Text: v.text}}); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.UpsertSpec(model.Spec{SpecID: "33.128", Series: "33", DocType: "TS"})
	for _, v := range []struct{ ver, text string }{
		{"1.22.1", "X1 as published in V1.22.1"},
		{"1.23.1", "X1 as published in V1.23.1"},
	} {
		_ = e.UpsertVersion(model.SpecVersion{SpecID: "ETSI TS 103 221-1", Release: "ETSI", Version: v.ver})
		if err := e.InsertClauses([]model.Clause{{ChunkID: chunkOf(v.ver), SpecID: "ETSI TS 103 221-1", Release: "ETSI",
			Version: v.ver, ClausePath: "5", Heading: "X1", Text: v.text}}); err != nil {
			t.Fatal(err)
		}
	}
	_ = e.UpsertSpec(model.Spec{SpecID: "ETSI TS 103 221-1", DocType: "TS"})

	srv, _ := New(st, "test", "", nil, e)
	rig := startStdio(t, srv)

	read := func(uri string) (string, string) {
		t.Helper()
		rep := rig.request(t, "resources/read", map[string]any{"uri": uri})
		if rep.Error != nil {
			return "", rep.Error.Message
		}
		var res struct {
			Contents []struct {
				Text string `json:"text"`
			} `json:"contents"`
		}
		if err := json.Unmarshal(rep.Result, &res); err != nil {
			t.Fatalf("%s: %v", uri, err)
		}
		var b strings.Builder
		for _, c := range res.Contents {
			b.WriteString(c.Text)
		}
		return b.String(), ""
	}

	for _, tc := range []struct{ uri, want, not string }{
		// The forms the descriptions advertise, on both halves.
		{"3gpp://33.128/Rel-19@19.5.0", "read in 19.5.0", "reads in 19.6.0"},
		{"3gpp://33.128/Rel-19", "reads in 19.6.0", "read in 19.5.0"},
		{"3gpp://ETSI%20TS%20103%20221-1/ETSI@1.22.1", "V1.22.1", "V1.23.1"},
		{"3gpp://ETSI%20TS%20103%20221-1/ETSI", "V1.23.1", "V1.22.1"},
		// And the clause template, unchanged by the new one.
		{"3gpp://33.128/Rel-19/2@19.5.0", "read in 19.5.0", "reads in 19.6.0"},
		{"3gpp://ETSI%20TS%20103%20221-1/ETSI/5@1.22.1", "V1.22.1", "V1.23.1"},
	} {
		body, errMsg := read(tc.uri)
		if errMsg != "" {
			t.Errorf("resources/read %s: %s", tc.uri, errMsg)
			continue
		}
		if !strings.Contains(body, tc.want) || strings.Contains(body, tc.not) {
			t.Errorf("resources/read %s served the wrong version: %q", tc.uri, body)
		}
	}

	// A URI whose release and version name two different documents is refused,
	// not served as the version's release (Qodo, #345); and a release that holds
	// no version of the spec is not found, not the newest version under its name.
	for _, tc := range []struct{ uri, want string }{
		{"3gpp://33.128/Rel-18@19.5.0", "published in Rel-19, not Rel-18"},
		{"3gpp://33.128/Rel-18/2@19.5.0", "published in Rel-19, not Rel-18"},
		{"3gpp://33.128/Rel-15", "no version of 33.128 in Rel-15"},
		{"3gpp://ETSI%20TS%20103%20221-1/Rel-19@1.22.1", "published in ETSI, not Rel-19"},
	} {
		body, errMsg := read(tc.uri)
		if !strings.Contains(errMsg, tc.want) {
			t.Errorf("resources/read %s: want an error saying %q; got error %q, body %q", tc.uri, tc.want, errMsg, body)
		}
	}

	// The template list says so too: the versioned form is advertised by a
	// template that can match it.
	rep := rig.request(t, "resources/templates/list", map[string]any{})
	var tl struct {
		ResourceTemplates []struct {
			URITemplate string `json:"uriTemplate"`
		} `json:"resourceTemplates"`
	}
	if err := json.Unmarshal(rep.Result, &tl); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, x := range tl.ResourceTemplates {
		got = append(got, x.URITemplate)
	}
	if !strings.Contains(strings.Join(got, " "), "3gpp://{spec_id}/{release}@{version}") {
		t.Errorf("resources/templates/list = %v, want the versioned whole-spec template", got)
	}
}

func chunkOf(version string) uint64 {
	var n uint64
	for _, c := range version {
		n = n*31 + uint64(c)
	}
	return n
}
