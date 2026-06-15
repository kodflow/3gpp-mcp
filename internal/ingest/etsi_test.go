package ingest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/embed"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// TestETSIIngestEndToEnd proves the ETSI path ingests through the SAME pipeline as
// 3GPP: an ETSI-provenance HTML (no 3GPP filename) lands clauses under the ETSI id.
func TestETSIIngestEndToEnd(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	convert := filepath.Join(root, "convert-etsi")
	// Glob is <ConvertDir>/*/*.html → put the file under an "ETSI" bucket dir.
	dir := filepath.Join(convert, "ETSI")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := `<!-- ETSI-SPEC: 103 221-1 | 1.21.1 -->
<html><body>
<h1>6	X1 task object</h1>
<p>The ADMF provisions interception tasks over the X1 interface.</p>
</body></html>`
	// A deliberately non-3GPP filename: ETSI mode must not rely on it.
	if err := os.WriteFile(filepath.Join(dir, "ts_10322101v012101p.html"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(root, "etsi.duckdb")
	st, err := Run(ctx, dbPath, Options{
		ConvertDir: convert,
		ETSI:       true,
		Embedder:   embed.Disabled{},
		Logf:       func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if st.Clauses == 0 {
		t.Fatalf("no clauses ingested: %+v", st)
	}

	db, err := store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var n int
	if err := db.DB().QueryRowContext(ctx,
		`SELECT count(*) FROM clauses WHERE spec_id = 'ETSI TS 103 221-1'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Error("no clauses stored under spec_id 'ETSI TS 103 221-1'")
	}
}
