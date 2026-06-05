package ingest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/store"
)

// chHTML builds a minimal LibreOffice-shaped spec with a Change-history annex
// carrying a single CR row at the given version.
func chHTML(cr, version, subject string) string {
	return "<html><body>" +
		"<h1>1\tScope</h1><p>scope text for this spec version.</p>" +
		"<h1>Change history</h1>" +
		"<table><tr><th>Date</th><th>Meeting</th><th>CR</th><th>Cat</th><th>New version</th><th>Subject</th></tr>" +
		"<tr><td>2024-06-01</td><td>SA2#100</td><td>" + cr + "</td><td>F</td><td>" + version + "</td><td>" + subject + "</td></tr>" +
		"</table></body></html>"
}

// writeConvert lays a file at <dir>/<rel>/<base>.html (the …/convert/<Rel>/ shape
// htmlparse attributes the release from).
func writeConvert(t *testing.T, dir, rel, base, html string) {
	t.Helper()
	d := filepath.Join(dir, rel)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, base+".html"), []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}
}

func changeCount(t *testing.T, dbPath string) int {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	var n int
	if err := st.DB().QueryRow(`SELECT count(*) FROM changes`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestChangeHistoryLotModeSuppressed pins B6: a release-scoped run (opt.Releases
// set) must NOT write the change-history annex, because its latest-in-run version
// may be older than the global latest and ReplaceChanges (DELETE+INSERT) would
// truncate a fuller cumulative annex. A full run (opt.Releases empty) still writes.
func TestChangeHistoryLotModeSuppressed(t *testing.T) {
	ctx := context.Background()
	convert := t.TempDir()
	// 23.501 at two versions; codes carry the release (h=17, j=19).
	writeConvert(t, convert, "Rel-17", "23501-h60", chHTML("0177", "17.6.0", "old fix"))
	writeConvert(t, convert, "Rel-19", "23501-j60", chHTML("0199", "19.6.0", "new fix"))

	// Full run: latest is 19.6.0, lotMode off → the Rel-19 annex is written.
	fullDB := filepath.Join(t.TempDir(), "full.duckdb")
	full, err := Run(ctx, fullDB, Options{ConvertDir: convert})
	if err != nil {
		t.Fatal(err)
	}
	if full.Changes != 1 {
		t.Fatalf("full run: stats.Changes = %d, want 1", full.Changes)
	}
	if n := changeCount(t, fullDB); n != 1 {
		t.Fatalf("full run: changes rows = %d, want 1", n)
	}

	// Lot run (Releases=[Rel-17]): only the Rel-17 file is a job and lotMode is on
	// → change writes are suppressed, so the table stays empty.
	lotDB := filepath.Join(t.TempDir(), "lot.duckdb")
	lot, err := Run(ctx, lotDB, Options{ConvertDir: convert, Releases: []string{"Rel-17"}})
	if err != nil {
		t.Fatal(err)
	}
	if lot.Changes != 0 {
		t.Fatalf("lot run: stats.Changes = %d, want 0 (suppressed)", lot.Changes)
	}
	if n := changeCount(t, lotDB); n != 0 {
		t.Fatalf("lot run: changes rows = %d, want 0 (a release subset must not truncate the annex)", n)
	}
}
