package goal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestDecodeVerCodeInvertsTheEncoder walks the exact table rust/discover pins for
// encode_ver_code. The two must stay mutual inverses: a version this decodes
// differently from what the status report wrote is a version cmp_ver reads as
// newer, and the key comes straight back as drift — the ledger would record
// entries that silence nothing.
func TestDecodeVerCodeInvertsTheEncoder(t *testing.T) {
	for code, want := range map[string]string{
		"100":    "1.0.0",
		"070":    "0.7.0",
		"111":    "1.1.1",
		"h60":    "17.6.0",
		"a50":    "10.5.0",
		"zzz":    "35.35.35",
		"300":    "3.0.0",
		"j00":    "19.0.0",
		"083700": "8.37.0",
		"016200": "1.62.0",
		"360000": "36.0.0",
		"999999": "99.99.99",
	} {
		got, ok := decodeVerCode(code)
		if !ok || got != want {
			t.Errorf("decodeVerCode(%q) = %q,%v — want %q", code, got, ok, want)
		}
	}
}

func TestDecodeVerCodeRejectsWhatIsNotAVersion(t *testing.T) {
	for _, code := range []string{"", "1", "12", "1234", "12345", "1234567", "A50", "a5!", "0a0000"} {
		if got, ok := decodeVerCode(code); ok {
			t.Errorf("decodeVerCode(%q) accepted %q — a wrong version silences the wrong spec", code, got)
		}
	}
}

const testWorklist = `Rel-4 https://www.3gpp.org/ftp/Specs/archive/21_series/21.100/21100-100.zip 21100-100.zip
Rel-10 https://www.3gpp.org/ftp/Specs/archive/25_series/25.914/25914-a20.zip 25914-a20.zip
Rel-11 https://www.3gpp.org/ftp/Specs/archive/30_series/30.531/30531-016400.zip 30531-016400.zip
Rel-15 https://www.3gpp.org/ftp/Specs/archive/36_series/36.571-3/36571-3-100.zip 36571-3-100.zip
Rel-16 https://www.3gpp.org/ftp/Specs/archive/23_series/23.501/23501-h60.zip 23501-h60.zip
`

// TestUpstreamRefusalsBecomeLedgerEntries. FAILDL is outright absence; FALLBACK
// is the same absence found by taking a LOWER version instead. Both mean the
// requested version does not exist, and both must be remembered or `fetch` runs
// for ever on a corpus nothing can add to.
func TestUpstreamRefusalsBecomeLedgerEntries(t *testing.T) {
	log := `2026-09-03T12:50:52+02:00 FAILDL https://www.3gpp.org/ftp/Specs/archive/21_series/21.100/21100-100.zip
2026-09-03T12:50:15+02:00 FALLBACK https://www.3gpp.org/ftp/Specs/archive/25_series/25.914/25914-a20.zip -> 25914-a10.zip
2026-09-03T12:50:53+02:00 FAILDL https://www.3gpp.org/ftp/Specs/archive/36_series/36.571-3/36571-3-100.zip
2026-09-03T12:51:46+02:00 FAILDL https://www.3gpp.org/ftp/Specs/archive/30_series/30.531/30531-016400.zip
`
	got := absentFromLog(testWorklist, log)
	want := map[string]string{
		"21.100|Rel-4":    "1.0.0",
		"25.914|Rel-10":   "10.2.0",
		"36.571-3|Rel-15": "1.0.0", // the spec DIRECTORY is the authority on where an id ends
		"30.531|Rel-11":   "1.64.0",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d ledger entries, want %d: %v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("ledger[%q] = %q, want %q", k, got[k], v)
		}
	}
}

// The negative control that matters most: a spec upstream SERVED must never be
// recorded absent. Doing so would freeze it at whatever version the corpus holds
// and no later run would ever ask for it again — a silent, permanent gap, which
// is strictly worse than the re-fetching this ledger exists to stop.
func TestASpecThatWasServedIsNotRecordedAbsent(t *testing.T) {
	log := `2026-09-03T12:50:52+02:00 FAILDL https://www.3gpp.org/ftp/Specs/archive/21_series/21.100/21100-100.zip
2026-09-03T12:50:59+02:00 [corpus] 23501-h60.zip -> HTML ok
`
	got := absentFromLog(testWorklist, log)
	if _, bad := got["23.501|Rel-16"]; bad {
		t.Fatal("a spec that downloaded and converted was recorded as absent upstream")
	}
	if len(got) != 1 {
		t.Fatalf("only the FAILDL belongs in the ledger, got %v", got)
	}
}

// A URL the work list never asked for carries no release, and a ledger key
// without the right release names a different (spec, release) than the corpus
// index does — silencing a spec nobody proved absent.
func TestAnUnrequestedURLIsIgnored(t *testing.T) {
	log := `2026-09-03T12:50:52+02:00 FAILDL https://www.3gpp.org/ftp/Specs/archive/99_series/99.999/99999-100.zip`
	if got := absentFromLog(testWorklist, log); len(got) != 0 {
		t.Fatalf("a URL outside the work list produced ledger entries: %v", got)
	}
}

// TestTheLedgerOnlyEverRises. A transient network failure must not be able to
// un-accept a key a previous run proved absent, and a key must never be dropped:
// either would reopen the loop this closes.
func TestTheLedgerOnlyEverRises(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "absent-index.json")

	if _, err := mergeAbsentIndex(p, map[string]string{"21.100|Rel-4": "1.0.0", "25.914|Rel-10": "10.2.0"}); err != nil {
		t.Fatal(err)
	}
	// A later run that saw a LOWER version, and one key it did not see at all.
	added, err := mergeAbsentIndex(p, map[string]string{"21.100|Rel-4": "0.9.0"})
	if err != nil {
		t.Fatal(err)
	}
	if added != 0 {
		t.Errorf("a lower version counted as a new acceptance (added=%d)", added)
	}

	var ledger map[string]string
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &ledger); err != nil {
		t.Fatal(err)
	}
	if ledger["21.100|Rel-4"] != "1.0.0" {
		t.Errorf("the ledger was lowered to %q — a proven absence was forgotten", ledger["21.100|Rel-4"])
	}
	if _, ok := ledger["25.914|Rel-10"]; !ok {
		t.Error("a key absent from the newer run was dropped from the ledger")
	}
}

// The negative control for the ledger: a genuinely NEWER version upstream must
// still get through. The ledger accepts one precise version as absent, never the
// key — otherwise a spec that 3GPP later publishes would never be acquired.
func TestAHigherUpstreamVersionOutranksTheLedger(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "absent-index.json")
	if _, err := mergeAbsentIndex(p, map[string]string{"25.914|Rel-10": "10.2.0"}); err != nil {
		t.Fatal(err)
	}
	added, err := mergeAbsentIndex(p, map[string]string{"25.914|Rel-10": "10.3.0"})
	if err != nil {
		t.Fatal(err)
	}
	if added != 0 {
		t.Errorf("raising an existing key counted as a new acceptance (added=%d)", added)
	}
	if cmpVerTriple("10.3.0", "10.2.0") <= 0 {
		t.Fatal("cmpVerTriple disagrees with cmp_ver about which version is newer, so the ledger and the delta cannot agree either")
	}
}

// TestCmpVerTripleMatchesTheRustOrdering. The ledger and rust/discover's delta
// must order versions identically, including the short forms the status report
// writes ("19.0" for 19.0.0).
func TestCmpVerTripleMatchesTheRustOrdering(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"19.0", "19.0.0", 0},
		{"10.5.0", "10.5.0", 0},
		{"10.5.1", "10.5.0", 1},
		{"1.64.0", "1.62.0", 1},
		{"", "0.0.0", 0},
		{"2.0.0", "10.0.0", -1},
	} {
		if got := cmpVerTriple(tc.a, tc.b); got != tc.want {
			t.Errorf("cmpVerTriple(%q,%q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
