package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/marcboeker/go-duckdb/v2"
)

// NOT ONE OF THESE TESTS TOUCHES THE ENVIRONMENT, and that is the point of
// PickMemoryLimit being pure. The environment is process-wide, tests run in
// parallel, and on Unix mutating it while another thread may read it is not
// permitted. Testing the policy with values costs nothing and removes the
// hazard entirely.
func TestPickMemoryLimit(t *testing.T) {
	for _, tc := range []struct {
		name, raw, want string
	}{
		{"unset falls back", "", DefaultMemoryLimit},
		{"explicit wins", "4GB", "4GB"},
		{"whitespace is not a value", "   ", DefaultMemoryLimit},
		{"tab and newline are not a value", "\t\n", DefaultMemoryLimit},
		{"surrounding space is trimmed", "  6GB  ", "6GB"},
	} {
		if got := PickMemoryLimit(tc.raw); got != tc.want {
			t.Errorf("%s: PickMemoryLimit(%q) = %q, want %q", tc.name, tc.raw, got, tc.want)
		}
	}
}

// The default must stay a literal. A cap derived from the machine's RAM would be
// the very default that killed the builds, so this pins the shape rather than
// trusting a comment.
func TestDefaultMemoryLimitIsAModestLiteral(t *testing.T) {
	if !strings.HasSuffix(DefaultMemoryLimit, "GB") {
		t.Fatalf("DefaultMemoryLimit = %q, want a plain size", DefaultMemoryLimit)
	}
	if DefaultMemoryLimit == "" || strings.ContainsAny(DefaultMemoryLimit, "%") {
		t.Fatalf("DefaultMemoryLimit = %q, want a fixed size and not a share of RAM", DefaultMemoryLimit)
	}
}

// The spill file belongs beside the corpus — the volume that must hold the
// result anyway — and a bare corpus name must still yield a usable relative path
// rather than an empty one or an absolute one.
func TestSpillDirSitsBesideTheCorpus(t *testing.T) {
	if got, want := SpillDir(filepath.Join("data", "etsi.duckdb")), filepath.Join("data", "duckdb-spill.tmp"); got != want {
		t.Fatalf("SpillDir = %q, want %q", got, want)
	}
	got := SpillDir("etsi.duckdb")
	if got == "" || filepath.IsAbs(got) {
		t.Fatalf("a bare corpus name must yield a local relative path, got %q", got)
	}
}

// The knobs are worthless if DuckDB refuses them, so open a real database and
// read the setting BACK. A renamed or removed option fails here rather than at
// 22 GB on a build machine.
func TestBoundMemoryIsAcceptedByDuckDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cap.duckdb")
	h, err := sql.Open("duckdb", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })

	if err := boundMemoryTo(h, dbPath, "3GB"); err != nil {
		t.Fatalf("DuckDB rejected the knobs: %v", err)
	}

	var got string
	if err := h.QueryRow("SELECT current_setting('memory_limit')").Scan(&got); err != nil {
		t.Fatal(err)
	}
	// DuckDB normalises the unit ("3GB" -> "2.7 GiB"), so assert the cap MOVED and
	// stayed modest rather than matching a formatted string.
	if !strings.HasPrefix(got, "2") && !strings.HasPrefix(got, "3") {
		t.Fatalf("memory_limit did not take, got %q (the default would be ~80%% of RAM)", got)
	}

	var tmp string
	if err := h.QueryRow("SELECT current_setting('temp_directory')").Scan(&tmp); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filepath.ToSlash(tmp), "duckdb-spill.tmp") {
		t.Fatalf("temp_directory did not take, got %q", tmp)
	}
}

// A single-quote in a path would end the SQL string early. Nothing in this
// repository has one today, which is exactly why it would go unnoticed.
func TestBoundMemoryEscapesQuotesInThePath(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "it's.duckdb")
	h, err := sql.Open("duckdb", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })

	if err := boundMemoryTo(h, dbPath, "3GB"); err != nil {
		t.Fatalf("a quote in the corpus path broke the statement: %v", err)
	}
}
