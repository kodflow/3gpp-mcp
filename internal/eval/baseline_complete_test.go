package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadBaselineRefusesAnIncompleteEntry pins the review finding on #324 that
// CodeRabbit and Qodo raised independently. Metrics is a struct of float64s, so an
// entry with a metric missing decodes it as 0 — and a baseline of zeros is a bar no
// ranking can fall below. The files are written as RAW JSON on purpose:
// WriteBaseline always emits every field, so a test built on it could never produce
// the incomplete file this guards against.
func TestLoadBaselineRefusesAnIncompleteEntry(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	full := `"ndcg@10":0.07,"recall@10":0.16,"mrr@10":0.04,"success@1":0`

	for name, body := range map[string]string{
		"empty entry":       `{"lexical":{}}`,
		"one missing":       `{"lexical":{"ndcg@10":0.07,"recall@10":0.16,"mrr@10":0.04}}`,
		"null metric":       `{"lexical":{"ndcg@10":null,"recall@10":0.16,"mrr@10":0.04,"success@1":0}}`,
		"second incomplete": `{"lexical":{` + full + `},"hybrid":{"ndcg@10":0.1}}`,
	} {
		if _, err := LoadBaseline(write(strings.ReplaceAll(name, " ", "_")+".json", body)); err == nil ||
			!strings.Contains(err.Error(), "incomplete") {
			t.Errorf("%s: %s was accepted — a missing metric is a zero bar; got err=%v", name, body, err)
		}
	}

	// An explicit 0 is a real value and must survive: success@1 is 0 on the
	// committed baseline, and refusing it would fail every build.
	bl, err := LoadBaseline(write("complete.json", `{"lexical":{`+full+`}}`))
	if err != nil {
		t.Fatalf("a complete baseline with an explicit 0 was refused: %v", err)
	}
	if bl["lexical"].NDCG10 != 0.07 {
		t.Errorf("the complete baseline decoded wrong: %+v", bl["lexical"])
	}
}

// TestTheCommittedBaselineIsComplete: the file the pipeline actually gates against
// must pass the same rule, or the gate refuses every build the moment this lands.
func TestTheCommittedBaselineIsComplete(t *testing.T) {
	if _, err := LoadBaseline(filepath.Join("..", "..", "docs", "inputs", "eval", "baseline.json")); err != nil {
		t.Fatalf("the committed baseline fails its own loader: %v", err)
	}
}
