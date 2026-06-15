package etsicat

import (
	"strings"
	"testing"
)

// a realistic Apache-style directory listing for a spec folder (version subdirs +
// the usual parent + sort-header anchors that must be ignored).
const specListing = `<html><head><title>Index of /deliver/etsi_ts/103200_103299/10322101</title></head>
<body><h1>Index of /deliver/etsi_ts/103200_103299/10322101</h1>
<table>
<tr><th><a href="?C=N;O=D">Name</a></th></tr>
<tr><td><a href="../">Parent Directory</a></td></tr>
<tr><td><a href="01.01.01_60/">01.01.01_60/</a></td></tr>
<tr><td><a href="01.21.01_60/">01.21.01_60/</a></td></tr>
<tr><td><a href="01.22.00_30/">01.22.00_30/</a></td></tr>
<tr><td><a href="01.04.01_60/">01.04.01_60/</a></td></tr>
</table></body></html>`

func TestExtractLinks(t *testing.T) {
	got, err := ExtractLinks(strings.NewReader(specListing))
	if err != nil {
		t.Fatal(err)
	}
	// Parent, sort-header (?C=...) must be dropped; the four version dirs kept.
	want := map[string]bool{"01.01.01_60/": true, "01.21.01_60/": true, "01.22.00_30/": true, "01.04.01_60/": true}
	if len(got) != len(want) {
		t.Fatalf("links=%v, want the %d version dirs only", got, len(want))
	}
	for _, l := range got {
		if !want[l] {
			t.Errorf("unexpected link %q (parent/sort header should be filtered)", l)
		}
	}
}

func TestParseVersionDir(t *testing.T) {
	cases := []struct {
		in   string
		ok   bool
		ver  string
		mile int
	}{
		{"01.21.01_60/", true, "1.21.1", 60},
		{"02.01.01_60", true, "2.1.1", 60},
		{"01.22.00_30/", true, "1.22.0", 30},
		{"../", false, "", 0},
		{"ts_10322101v012101p.pdf", false, "", 0},
		{"1.21.1", false, "", 0},
	}
	for _, c := range cases {
		v, ok := ParseVersionDir(c.in)
		if ok != c.ok {
			t.Errorf("ParseVersionDir(%q) ok=%v, want %v", c.in, ok, c.ok)
			continue
		}
		if ok && (v.String() != c.ver || v.Milestone != c.mile) {
			t.Errorf("ParseVersionDir(%q) = %s_%d, want %s_%d", c.in, v.String(), v.Milestone, c.ver, c.mile)
		}
	}
}

func TestLatestPublished(t *testing.T) {
	links, _ := ExtractLinks(strings.NewReader(specListing))
	// 01.22.00 is the highest by rank BUT milestone 30 (draft) → must be ignored;
	// the latest PUBLISHED (milestone 60) is 1.21.1.
	v, ok := LatestPublished(links)
	if !ok {
		t.Fatal("expected a published version")
	}
	if v.String() != "1.21.1" {
		t.Errorf("LatestPublished = %s, want 1.21.1 (drafts excluded)", v.String())
	}

	// A directory with only drafts → no published version.
	draftsOnly := []string{"../", "01.00.00_20/", "01.01.00_30/"}
	if _, ok := LatestPublished(draftsOnly); ok {
		t.Error("drafts-only dir should yield no published version")
	}
}

func TestDiff(t *testing.T) {
	index := map[string]string{
		"103 221-1": "1.20.1", // older than site → changed
		"103 280":   "2.1.1",  // same as site → unchanged
		"103 221-2": "1.4.1",  // not on site this run → not reported (only site keys)
	}
	site := map[string]string{
		"103 221-1": "1.21.1", // bumped
		"103 280":   "2.1.1",  // unchanged
		"103 120":   "1.16.1", // absent from index → new
	}
	changed := map[string]bool{}
	for _, id := range Diff(site, index) {
		changed[id] = true
	}
	if !changed["103 221-1"] {
		t.Error("103 221-1 bumped (1.20.1->1.21.1) should be in the diff")
	}
	if !changed["103 120"] {
		t.Error("103 120 (absent from index) should be in the diff")
	}
	if changed["103 280"] {
		t.Error("103 280 (unchanged) should NOT be in the diff")
	}
	if len(changed) != 2 {
		t.Errorf("diff = %v, want exactly {103 221-1, 103 120}", changed)
	}
}
