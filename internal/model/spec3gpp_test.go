package model

import "testing"

func TestDecodeVersionCode(t *testing.T) {
	cases := []struct {
		code, rel, ver string
		ok             bool
	}{
		{"j60", "Rel-19", "19.6.0", true},
		{"i80", "Rel-18", "18.8.0", true},
		{"hf0", "Rel-17", "17.15.0", true}, // f = 15
		{"k10", "Rel-20", "20.1.0", true},
		{"3a0", "Rel-99", "3.10.0", true}, // major 3 == Rel-99
		{"500", "Rel-5", "5.0.0", true},   // legacy GSM numeric code (#129); dir overrides rel to GSM
		{"700", "Rel-7", "7.0.0", true},   // legacy GSM numeric code (#129)
		{"zz", "", "", false},             // too short
		{"!!!", "", "", false},            // invalid chars
	}
	for _, c := range cases {
		rel, ver, ok := DecodeVersionCode(c.code)
		if ok != c.ok || rel != c.rel || ver != c.ver {
			t.Errorf("DecodeVersionCode(%q) = (%q,%q,%v), want (%q,%q,%v)",
				c.code, rel, ver, ok, c.rel, c.ver, c.ok)
		}
	}
}

func TestIsStableVersion(t *testing.T) {
	cases := []struct {
		ver    string
		stable bool
	}{
		{"19.6.0", true},  // Rel-19 published
		{"20.1.0", true},  // Rel-20 published
		{"3.0.0", true},   // major 3 == Rel-99, published
		{"2.0.0", false},  // draft
		{"2.15.0", false}, // draft (high minor, still major 2)
		{"1.0.0", false},  // draft
		{"0.2.0", false},  // early draft
		{"", false},       // unparseable → fail-safe not-stable
		{"GSM", false},    // legacy bucket label, not a dotted version
	}
	for _, c := range cases {
		if got := IsStableVersion(c.ver); got != c.stable {
			t.Errorf("IsStableVersion(%q) = %v, want %v", c.ver, got, c.stable)
		}
	}
}

func TestEncodeRoundTrip(t *testing.T) {
	for _, ver := range []string{"19.6.0", "18.15.0", "20.1.0"} {
		code, ok := EncodeVersionCode(ver)
		if !ok {
			t.Fatalf("encode %q failed", ver)
		}
		_, got, ok := DecodeVersionCode(code)
		if !ok || got != ver {
			t.Errorf("roundtrip %q -> %q -> %q", ver, code, got)
		}
	}
}

func TestArchiveURL(t *testing.T) {
	got := ArchiveURL("33.128", "19.6.0")
	want := "https://www.3gpp.org/ftp/Specs/archive/33_series/33.128/33128-j60.zip"
	if got != want {
		t.Errorf("ArchiveURL = %q, want %q", got, want)
	}
	if SeriesOf("33.128") != "33" || WorkingGroupForSeries("33") != "SA3" {
		t.Errorf("series/WG mapping wrong: %q %q", SeriesOf("33.128"), WorkingGroupForSeries("33"))
	}
}
