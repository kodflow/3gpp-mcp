package model

import "testing"

func TestNormalizeEtsiID(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"ETSI TS 103 221-1", "103 221-1", true},
		{"TS 103 221-1", "103 221-1", true},
		{"103 221-1", "103 221-1", true},
		{"TS 103 280", "103 280", true},
		{"103 280", "103 280", true},
		{"ETSI TS 103221-1", "103 221-1", true}, // no space between base groups
		{"23.501", "", false},                   // 3GPP id must NOT match
		{"TS 23.501", "", false},
		{"TR 38.913", "", false},
		{"hello", "", false},
	}
	for _, c := range cases {
		got, ok := NormalizeEtsiID(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("NormalizeEtsiID(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestEtsiDeliverURL(t *testing.T) {
	cases := []struct {
		id, version, want string
	}{
		{
			"103 221-1", "1.21.1",
			"https://www.etsi.org/deliver/etsi_ts/103200_103299/10322101/01.21.01_60/ts_10322101v012101p.pdf",
		},
		{
			"103 221-2", "1.4.1",
			"https://www.etsi.org/deliver/etsi_ts/103200_103299/10322102/01.04.01_60/ts_10322102v010401p.pdf",
		},
		{
			"103 280", "2.1.1", // no part suffix
			"https://www.etsi.org/deliver/etsi_ts/103200_103299/103280/02.01.01_60/ts_103280v020101p.pdf",
		},
		{
			"103 221-1", "", // unknown version -> landing folder (cite-the-pointer)
			"https://www.etsi.org/deliver/etsi_ts/103200_103299/10322101/",
		},
		{
			"103 221-1", "1.21", // partial version -> landing folder, never fabricate
			"https://www.etsi.org/deliver/etsi_ts/103200_103299/10322101/",
		},
		{"23.501", "16.0.0", ""}, // not ETSI-shaped -> empty
	}
	for _, c := range cases {
		if got := EtsiDeliverURL(c.id, c.version); got != c.want {
			t.Errorf("EtsiDeliverURL(%q,%q) =\n  %q\nwant\n  %q", c.id, c.version, got, c.want)
		}
	}
}
