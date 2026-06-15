package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSparseNeeded(t *testing.T) {
	cases := []struct {
		name, expected, published string
		want                      bool
	}{
		{"not-capable", "", "", false}, // dense-only build: never needed
		{"not-capable-ignores-published", "", "x", false},
		{"missing-published", "abc123", "", true},     // capable + nothing published
		{"stale-published", "abc123", "old999", true}, // capable + different layer
		{"converged", "abc123", "abc123", false},      // capable + already the expected layer
	}
	for _, c := range cases {
		if got := sparseNeeded(c.expected, c.published); got != c.want {
			t.Errorf("%s: sparseNeeded(%q,%q)=%v, want %v", c.name, c.expected, c.published, got, c.want)
		}
	}
}

func TestLoadSparseModelID(t *testing.T) {
	if got := loadSparseModelID(""); got != "" {
		t.Errorf("empty path => %q, want \"\"", got)
	}
	if got := loadSparseModelID(filepath.Join(t.TempDir(), "nope.json")); got != "" {
		t.Errorf("missing file => %q, want \"\"", got)
	}
	dir := t.TempDir()
	good := filepath.Join(dir, "sparse-index.json")
	if err := os.WriteFile(good, []byte(`{"sparse_model":"deadbeef12"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadSparseModelID(good); got != "deadbeef12" {
		t.Errorf("good file => %q, want deadbeef12", got)
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadSparseModelID(bad); got != "" {
		t.Errorf("malformed file => %q, want \"\" (degrade to absent)", got)
	}
}
