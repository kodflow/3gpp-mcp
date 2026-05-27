package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseStatusReport(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "status-report.htm"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	specs, vers, err := ParseStatusReport(f)
	if err != nil {
		t.Fatal(err)
	}

	byID := map[string]SpecMeta{}
	for _, s := range specs {
		byID[s.SpecID] = s
	}
	// 23.501 recurs in two sections — deduped to one SpecMeta.
	if len(specs) != 3 {
		t.Errorf("specs = %d, want 3 (23.501, 33.128, 21.905)", len(specs))
	}
	if got := byID["33.128"]; got.WorkingGroup != "S3" || got.DocType != "TS" ||
		got.Series != "33" || got.Title == "" {
		t.Errorf("33.128 meta = %+v", got)
	}
	if got := byID["23.501"]; got.WorkingGroup != "S2" {
		t.Errorf("23.501 WG = %q, want S2", got.WorkingGroup)
	}
	if got := byID["21.905"]; got.DocType != "TR" {
		t.Errorf("21.905 doc_type = %q, want TR", got.DocType)
	}

	// Per-release latest versions: 23.501 has Rel-19 (19.7.0) and Rel-18 (18.12.0).
	want := map[string]string{"Rel-19": "19.7.0", "Rel-18": "18.12.0"}
	for _, v := range vers {
		if v.SpecID != "23.501" {
			continue
		}
		if want[v.Release] != v.Version {
			t.Errorf("23.501 %s = %q, want %q", v.Release, v.Version, want[v.Release])
		}
		delete(want, v.Release)
	}
	if len(want) != 0 {
		t.Errorf("missing 23.501 versions: %v", want)
	}
}

func TestParseReleases(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "releases.htm"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	rels, err := ParseReleases(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 3 {
		t.Fatalf("releases = %d, want 3", len(rels))
	}
	byCode := map[string]ReleaseMeta{}
	for _, r := range rels {
		byCode[r.Code] = r
	}

	r18 := byCode["Rel-18"]
	if r18.Status != "Frozen" || r18.Name != "Release 18" {
		t.Errorf("Rel-18 = %+v", r18)
	}
	if r18.FreezeDate == nil || r18.FreezeDate.Format("2006-01-02") != "2024-06-21" {
		t.Errorf("Rel-18 freeze = %v, want 2024-06-21", r18.FreezeDate)
	}
	if r18.FreezeMeeting != "SA#104" {
		t.Errorf("Rel-18 meeting = %q, want SA#104", r18.FreezeMeeting)
	}
	// Open release still carries a planned freeze date + Open status.
	if r20 := byCode["Rel-20"]; r20.Status != "Open" || r20.FreezeDate == nil {
		t.Errorf("Rel-20 = %+v, want Open with planned freeze", r20)
	}
}
