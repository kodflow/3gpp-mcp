package main

import (
	"reflect"
	"sort"
	"testing"
)

// statusHTML mirrors the layout of internal/catalog/testdata/status-report.htm:
// one section per release (anchored by <a name="activeRel-NN">), each spec
// recurring across the sections it lives in. 23.501 appears in Rel-19, Rel-18 and
// Rel-16 so we can prove a LOWER-release bump is seen while a HIGHER release of
// the SAME spec stays put.
const statusHTML = `<!DOCTYPE html><html><body>
<a name="activeRel-19"></a>
<table>
  <tr><th>type</th><th>spec&nbsp;num</th><th>title</th><th>vers</th><th>WG</th></tr>
  <tr><td>TS</td><td><a href="/x">23.501</a></td><td>5GS arch</td><td>19.7.0</td><td>S2</td></tr>
</table>
<a name="activeRel-18"></a>
<table>
  <tr><th>type</th><th>spec&nbsp;num</th><th>title</th><th>vers</th><th>WG</th></tr>
  <tr><td>TS</td><td><a href="/x">23.501</a></td><td>5GS arch</td><td>18.13.0</td><td>S2</td></tr>
</table>
<a name="deadRel-16"></a>
<table>
  <tr><th>type</th><th>spec&nbsp;num</th><th>title</th><th>vers</th><th>WG</th></tr>
  <tr><td>TS</td><td><a href="/x">23.501</a></td><td>5GS arch</td><td>16.16.0</td><td>S2</td></tr>
</table>
</body></html>`

// TestParseStatusPerSpecRelease asserts the status report collapses to one
// version PER (spec, release), not one scalar per spec — the data-shape change
// that makes a lower-release bump observable at all.
func TestParseStatusPerSpecRelease(t *testing.T) {
	site, err := parseStatus([]byte(statusHTML))
	if err != nil {
		t.Fatalf("parseStatus: %v", err)
	}
	want := map[string]string{
		"23.501|Rel-19": "19.7.0",
		"23.501|Rel-18": "18.13.0",
		"23.501|Rel-16": "16.16.0",
	}
	if !reflect.DeepEqual(site, want) {
		t.Fatalf("parseStatus per-(spec,release) mismatch:\n got %v\nwant %v", site, want)
	}
}

// TestDeltaLowerReleaseBumpDetected is the PR-5 regression lock: a Rel-16
// maintenance bump (16.15.0 -> 16.16.0) must put series "23" in the matrix EVEN
// THOUGH Rel-19 of the same spec is higher and unchanged. The pre-PR-5 scalar
// collapse compared the site's single highest (19.7.0) against the indexed
// highest (19.7.0), saw equality, and missed the Rel-16 update entirely.
func TestDeltaLowerReleaseBumpDetected(t *testing.T) {
	site, err := parseStatus([]byte(statusHTML))
	if err != nil {
		t.Fatalf("parseStatus: %v", err)
	}
	idx := map[string]string{
		"23.501|Rel-19": "19.7.0",  // unchanged
		"23.501|Rel-18": "18.13.0", // unchanged
		"23.501|Rel-16": "16.15.0", // site is 16.16.0 -> changed
	}
	got := seriesList(deltaSeries(site, idx, major("Rel-4"), false))
	if want := []string{"23"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("lower-release bump not detected: got %v want %v", got, want)
	}
}

// TestDeltaNothingChangedEmpty: when every (spec,release) site version equals the
// indexed version, the matrix is empty (no spurious rebuild).
func TestDeltaNothingChangedEmpty(t *testing.T) {
	site, err := parseStatus([]byte(statusHTML))
	if err != nil {
		t.Fatalf("parseStatus: %v", err)
	}
	idx := map[string]string{
		"23.501|Rel-19": "19.7.0",
		"23.501|Rel-18": "18.13.0",
		"23.501|Rel-16": "16.16.0",
	}
	if got := seriesList(deltaSeries(site, idx, major("Rel-4"), false)); len(got) != 0 {
		t.Fatalf("expected empty matrix, got %v", got)
	}
}

// TestDeltaNewReleaseDetected: a release present on the site but absent from the
// index (idx[key]=="") is treated as changed, so a brand-new release of an
// already-indexed spec rebuilds its series.
func TestDeltaNewReleaseDetected(t *testing.T) {
	site, err := parseStatus([]byte(statusHTML))
	if err != nil {
		t.Fatalf("parseStatus: %v", err)
	}
	idx := map[string]string{
		"23.501|Rel-19": "19.7.0",
		"23.501|Rel-18": "18.13.0",
		// Rel-16 missing from the index entirely => new release => changed.
	}
	got := seriesList(deltaSeries(site, idx, major("Rel-4"), false))
	if want := []string{"23"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("new release not detected: got %v want %v", got, want)
	}
}

// TestDeltaFloorPerRelease: the floor filters per (spec,release), not after a
// max-version collapse. A Rel-16 bump below a Rel-17 floor must NOT trigger,
// while the unchanged-but-above-floor releases stay quiet too.
func TestDeltaFloorPerRelease(t *testing.T) {
	site, err := parseStatus([]byte(statusHTML))
	if err != nil {
		t.Fatalf("parseStatus: %v", err)
	}
	idx := map[string]string{
		"23.501|Rel-19": "19.7.0",
		"23.501|Rel-18": "18.13.0",
		"23.501|Rel-16": "16.15.0", // site bumped, but below a Rel-17 floor
	}
	if got := seriesList(deltaSeries(site, idx, major("Rel-17"), false)); len(got) != 0 {
		t.Fatalf("Rel-16 bump leaked past a Rel-17 floor: got %v", got)
	}
}

func seriesList(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// TestLegacyGSMSeries pins the legacy series range used by --include-legacy-gsm:
// all 2-digit, all below the modern series floor (21), and distinct — so adding
// them can never collide with a modern series the status report already surfaces.
func TestLegacyGSMSeries(t *testing.T) {
	if len(legacyGSMSeries) == 0 {
		t.Fatal("legacyGSMSeries is empty")
	}
	seen := map[string]bool{}
	for _, s := range legacyGSMSeries {
		if len(s) != 2 {
			t.Errorf("legacy series %q is not 2 digits", s)
		}
		if s >= "21" {
			t.Errorf("legacy series %q is in the modern range (>=21) — would double-count", s)
		}
		if seen[s] {
			t.Errorf("duplicate legacy series %q", s)
		}
		seen[s] = true
	}
}

// TestSeriesInIndex locks the #129 convergence gate: --include-legacy-gsm must add
// a legacy series ONLY while it is absent from the index. Once its 4-digit specs
// are ingested (the parser fix), the series is present and must NOT be re-added —
// otherwise discover re-selects it forever and the daily delta never reaches 0.
func TestSeriesInIndex(t *testing.T) {
	idx := map[string]string{
		"23.501|Rel-18": "18.5.0",
		"03.88|GSM":     "5.0.0", // legacy GSM now indexed
	}
	if !seriesInIndex(idx, "03") {
		t.Error("series 03 is indexed (03.88|GSM) but seriesInIndex says no — would re-select forever")
	}
	if !seriesInIndex(idx, "23") {
		t.Error("series 23 is indexed but seriesInIndex says no")
	}
	if seriesInIndex(idx, "04") {
		t.Error("series 04 is NOT indexed but seriesInIndex says yes — would skip a needed build")
	}
	if seriesInIndex(map[string]string{}, "03") {
		t.Error("empty index must report series 03 absent (so a fresh build still picks it up)")
	}
}
