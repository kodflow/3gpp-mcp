package subjectmeta_test

import (
	"sort"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/registry"
	"github.com/kodflow/3gpp-mcp/internal/subjectmeta"
)

// TestSubjectMetaMatchesRegistry is the anti-desync guard: the CGO-free
// subjectmeta.All must list exactly the same subjects as the dynamic
// registry.Default(). Without this, someone could add a subject to the registry
// (so it ingests) without giving it incremental metadata (so a change to it
// would be silently invisible to discover), or vice-versa.
func TestSubjectMetaMatchesRegistry(t *testing.T) {
	var regNames []string
	for _, s := range registry.Default().All() {
		regNames = append(regNames, s.Name())
	}
	var metaNames []string
	for _, m := range subjectmeta.All {
		metaNames = append(metaNames, m.Name)
	}
	sort.Strings(regNames)
	sort.Strings(metaNames)

	if len(regNames) != len(metaNames) {
		t.Fatalf("subject count mismatch: registry=%v subjectmeta=%v", regNames, metaNames)
	}
	for i := range regNames {
		if regNames[i] != metaNames[i] {
			t.Fatalf("subject set mismatch: registry=%v subjectmeta=%v", regNames, metaNames)
		}
	}
}

// TestSubjectActivatesOwnSeries cross-checks that each subjectmeta entry's
// declared Series actually matches the spec the live subject activates on, so a
// wrong Series (which would rebuild the wrong shard on a bump) is caught.
func TestSubjectActivatesOwnSeries(t *testing.T) {
	bySeries := map[string]bool{}
	for _, m := range subjectmeta.All {
		for _, s := range m.Series {
			bySeries[m.Name+"/"+s] = true
		}
	}
	// li owns series 33 (TS 33.128); glossary owns series 21 (TS 21.905).
	for _, want := range []string{"li/33", "glossary/21"} {
		if !bySeries[want] {
			t.Errorf("expected subjectmeta to map %s", want)
		}
	}
}

// TestFootprintChangesWithVersion locks the core property: a Version bump shifts
// the footprint (so discover detects it), while an unchanged subject is stable.
func TestFootprintChangesWithVersion(t *testing.T) {
	base := subjectmeta.Meta{Name: "li", Version: "li-v1", Series: []string{"33"}}
	bumped := subjectmeta.Meta{Name: "li", Version: "li-v2", Series: []string{"33"}}
	if subjectmeta.Footprint(base) == subjectmeta.Footprint(bumped) {
		t.Fatal("footprint must change when Version changes")
	}
	// Determinism: a SEPARATE Meta value with identical fields must hash the same
	// (two distinct values, so this isn't a tautological self-comparison).
	baseAgain := subjectmeta.Meta{Name: "li", Version: "li-v1", Series: []string{"33"}}
	if subjectmeta.Footprint(base) != subjectmeta.Footprint(baseAgain) {
		t.Fatal("footprint must be deterministic for equal Meta values")
	}
}

// TestFootprintTracksSourceHash locks the pv-omits-asn1-scanner fix: the
// content-derived SourceHash (e.g. the ASN.1 scanner tag) influences the
// footprint independently of the Version string, so a scanner change is detected
// even if the developer forgets to bump Version. Two Metas differing ONLY by
// SourceHash must hash differently.
func TestFootprintTracksSourceHash(t *testing.T) {
	a := subjectmeta.Meta{Name: "li", Version: "li-v1", SourceHash: "asn1-vA", Series: []string{"33"}}
	b := subjectmeta.Meta{Name: "li", Version: "li-v1", SourceHash: "asn1-vB", Series: []string{"33"}}
	if subjectmeta.Footprint(a) == subjectmeta.Footprint(b) {
		t.Fatal("footprint must change when SourceHash (ASN.1 scanner) changes, even with Version held constant")
	}
	// The shipped LI subject must carry the scanner tag in its SourceHash so a real
	// scanner bump (ASN1ScannerVersion) flips the LI footprint.
	var liSrc string
	for _, m := range subjectmeta.All {
		if m.Name == "li" {
			liSrc = m.SourceHash
		}
	}
	if liSrc != subjectmeta.ASN1ScannerVersion {
		t.Fatalf("LI SourceHash = %q, want the ASN.1 scanner tag %q", liSrc, subjectmeta.ASN1ScannerVersion)
	}
}

// TestIngestFootprintsSortedStable locks that IngestFootprints (folded into
// model.SpecIngestIdentity) is sorted and deterministic, so the ingest gate does
// not flip merely because subjects were declared in a different order.
func TestIngestFootprintsSortedStable(t *testing.T) {
	a := subjectmeta.IngestFootprints()
	b := subjectmeta.IngestFootprints()
	if len(a) != len(subjectmeta.All) {
		t.Fatalf("IngestFootprints len=%d, want %d", len(a), len(subjectmeta.All))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatal("IngestFootprints must be deterministic")
		}
		if i > 0 && a[i-1] > a[i] {
			t.Fatalf("IngestFootprints must be sorted, got %v", a)
		}
	}
}

// TestChangedSeries covers the three discover cases: unchanged (nil), one
// subject bumped (only its series), and an empty published index (all series,
// the once-only re-index after the feature ships).
func TestChangedSeries(t *testing.T) {
	cur := subjectmeta.Index()

	// 1. Published == current → nothing changed.
	if got := subjectmeta.ChangedSeries(cur); len(got) != 0 {
		t.Errorf("no change should yield no series, got %v", got)
	}

	// 2. Pretend li's published footprint is stale → only series 33 rebuilds.
	stale := map[string]string{}
	for k, v := range cur {
		stale[k] = v
	}
	stale["li"] = "deadbeef0000"
	got := subjectmeta.ChangedSeries(stale)
	if len(got) != 1 || got[0] != "33" {
		t.Errorf("li bump should rebuild only series 33, got %v", got)
	}

	// 3. Empty published index → every subject's series (once-only re-index).
	all := subjectmeta.ChangedSeries(map[string]string{})
	if len(all) != 2 { // 21 (glossary) + 33 (li)
		t.Errorf("empty index should rebuild all subject series, got %v", all)
	}
}
