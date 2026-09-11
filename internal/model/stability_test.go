package model

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE DEFECT, as served on 2026-09-11: ETSI TS 103 221-1 V1.23.1 — a published
// TS — was answered "returned version 1.23.1 is a DRAFT", because the 3GPP rule
// (major < 3 is a draft) was applied to an ETSI edition number. Every version the
// ETSI half holds comes from the /deliver publication archive (milestone 60),
// including the 14 whose major is 0.
//
// Falsified: with IsStableSpecVersion returning IsStableVersion for every id,
// the ETSI rows below come back false.
func TestAnEtsiPublicationIsStableWhateverItsMajor(t *testing.T) {
	for _, c := range []struct {
		spec, ver string
		want      bool
	}{
		{"ETSI TS 103 221-1", "1.23.1", true},
		{"ETSI EN 300 497-1", "0.3.2", true},
		{"ETSI TS 102 221", "18.4.0", true},
		{"etsi ts 102 232-1", "2.1.1", true}, // same normalised predicate as the MCP router
		// An empty version is a deliverable the corpus does not hold: not a
		// version, so nothing to call stable.
		{"ETSI TS 103 280", "", false},
		// A shape the /deliver crawl cannot produce is vouched for by nothing
		// (Qodo, #338): not a folder "VV.VV.VV_60", so not called stable.
		{"ETSI TS 103 221-1", "1.23", false},
		{"ETSI TS 103 221-1", "1.x.1", false},
		{"ETSI TS 103 221-1", "draft", false},
	} {
		if got := IsStableSpecVersion(c.spec, c.ver); got != c.want {
			t.Errorf("IsStableSpecVersion(%q, %q) = %v, want %v", c.spec, c.ver, got, c.want)
		}
	}
}

// THE 3GPP ANSWER DOES NOT MOVE. 570 of the 20 163 3GPP versions served on
// 2026-09-11 are genuine drafts (major 0/1/2); the half-aware rule must agree
// with the old one on every 3GPP version, drafts included.
//
// Falsified: with IsStableSpecVersion returning true for every id, the draft
// rows below disagree.
func TestThe3GPPRuleIsUnchanged(t *testing.T) {
	for _, spec := range []string{"23.501", "33.128", "38.300", "0408"} {
		for _, ver := range []string{"0.1.0", "1.0.0", "2.0.0", "2.9.9", "3.0.0", "8.9.0", "15.0.0", "18.6.0", "19.6.0", "20.2.0", ""} {
			if got, want := IsStableSpecVersion(spec, ver), IsStableVersion(ver); got != want {
				t.Errorf("IsStableSpecVersion(%q, %q) = %v, but the 3GPP rule says %v", spec, ver, got, want)
			}
		}
	}
	if IsStableSpecVersion("23.501", "2.0.0") {
		t.Error("a 3GPP 2.0.0 is a draft and must stay one")
	}
}

// The citation every search hit and get_spec clause carries goes through
// Clause.Cite, so the rule has to reach it.
func TestAnEtsiClauseIsCitedStable(t *testing.T) {
	etsi := Clause{SpecID: "ETSI TS 103 221-1", Release: "ETSI", Version: "1.23.1", ClausePath: "5.1"}
	if !etsi.Cite().Stable {
		t.Errorf("ETSI clause cited stable:false: %+v", etsi.Cite())
	}
	draft := Clause{SpecID: "23.501", Release: "Rel-20", Version: "2.0.0", ClausePath: "5.1"}
	if draft.Cite().Stable {
		t.Errorf("3GPP draft clause cited stable:true: %+v", draft.Cite())
	}
}

// NOTHING OUTSIDE internal/model MAY ASK THE HALF-BLIND QUESTION AGAIN. Every
// caller of the stability rule knows which spec it is looking at; the version
// alone cannot say which half's rule applies, and that is how the defect above
// was written in six places at once.
func TestNoProductionCallerUsesTheHalfBlindRule(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	var offenders []string
	scanned := 0
	for _, dir := range []string{"internal", "cmd"} {
		_ = filepath.Walk(filepath.Join(root, dir), func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			rel := filepath.ToSlash(strings.TrimPrefix(p, root+string(filepath.Separator)))
			if strings.HasPrefix(rel, "internal/model/") {
				return nil
			}
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return nil
			}
			scanned++
			if strings.Contains(string(b), "IsStableVersion(") {
				offenders = append(offenders, rel)
			}
			return nil
		})
	}
	if scanned == 0 {
		t.Fatal("no Go file scanned: the walk root is wrong and this test checks nothing")
	}
	if len(offenders) > 0 {
		t.Errorf("model.IsStableVersion (the 3GPP-only rule) is called from %v; use IsStableSpecVersion(specID, version)", offenders)
	}
}
