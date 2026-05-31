package e2e

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/embed"
	"github.com/kodflow/3gpp-mcp/internal/ingest"
	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// writeHTML drops an HTML spec file under <convert>/<release>/<name>, creating
// the release dir. The ingest glob is <convert>/*/*.html, so each spec lives in
// a per-release subdir exactly like the real corpus.sh output.
func writeHTML(t *testing.T, convert, release, name, body string) {
	t.Helper()
	dir := filepath.Join(convert, release)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// specHTML is a minimal but valid LibreOffice-style spec body: a numbered
// heading + prose so htmlparse yields at least one clause. The tab between the
// clause number and title is what the heading parser splits on.
func specHTML(title string) string {
	return "<html><body>\n" +
		"<h1>1\tScope</h1><p>The present document specifies " + title + ".</p>\n" +
		"<h2>4.1\tGeneral</h2><p>General behaviour of " + title + ".</p>\n" +
		"</body></html>"
}

func clauseCount(t *testing.T, dbPath, specID string) int {
	t.Helper()
	st, err := store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	var n int
	if err := st.DB().QueryRowContext(context.Background(),
		`SELECT count(*) FROM clauses WHERE spec_id=?`, specID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func metaVal(t *testing.T, dbPath, key string) string {
	t.Helper()
	st, err := store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	return st.GetMeta(context.Background(), key)
}

// TestIngestResumeAppends is the executable proof of the two properties the
// corpus build depends on at the ingest level:
//
//   - RESUME skips work already 'done' (so a runner re-run after a 6h cap, or a
//     cross-run cache hit, makes the second attempt nearly free instead of
//     redoing the series).
//   - RESUME is APPEND, not rebuild: keeping the existing DB and adding a new
//     spec must leave every prior spec's data intact (no Reset wipe).
//
// It also asserts the output carries pipeline_version — the exact meta key
// cmd/merge reads to decide base-vs-shard compatibility on a delta merge.
func TestIngestResumeAppends(t *testing.T) {
	ctx := context.Background()
	convert := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "corpus.duckdb")
	quiet := func(string, ...any) {}

	// Two specs in two series, both Rel-18 (code i40 → 18.4.0).
	writeHTML(t, convert, "Rel-18", "23501-i40.html", specHTML("system architecture"))
	writeHTML(t, convert, "Rel-18", "24501-i40.html", specHTML("NAS protocol"))

	// ---- Run 1: full ingest (no resume). ----
	st1, err := ingest.Run(ctx, dbPath, ingest.Options{
		ConvertDir: convert, EnableFTS: false, Embedder: embed.Disabled{}, Logf: quiet,
	})
	if err != nil {
		t.Fatalf("run1: %v", err)
	}
	if st1.Specs != 2 {
		t.Fatalf("run1 specs = %d, want 2", st1.Specs)
	}
	base23 := clauseCount(t, dbPath, "23.501")
	base24 := clauseCount(t, dbPath, "24.501")
	if base23 == 0 || base24 == 0 {
		t.Fatalf("run1 clause counts: 23=%d 24=%d (both must be >0)", base23, base24)
	}
	// The link merge relies on: ingest must stamp pipeline_version.
	wantPV := model.PipelineVersion("") // lexical (Disabled embedder)
	if got := metaVal(t, dbPath, "pipeline_version"); got != wantPV {
		t.Fatalf("run1 pipeline_version = %q, want %q", got, wantPV)
	}

	// ---- Run 2: resume on the SAME corpus → everything already 'done'. ----
	st2, err := ingest.Run(ctx, dbPath, ingest.Options{
		ConvertDir: convert, EnableFTS: false, Embedder: embed.Disabled{}, Logf: quiet, Resume: true,
	})
	if err != nil {
		t.Fatalf("run2 (resume, no change): %v", err)
	}
	if st2.Clauses != 0 {
		t.Errorf("run2 re-ingested %d clauses; resume must skip already-done specs (want 0)", st2.Clauses)
	}
	if st2.Specs != 0 {
		t.Errorf("run2 processed %d specs; resume must skip all done (want 0)", st2.Specs)
	}
	if c := clauseCount(t, dbPath, "23.501"); c != base23 {
		t.Errorf("23.501 clause count drifted on no-op resume: %d != %d", c, base23)
	}

	// ---- Run 3: add a NEW spec, resume → only the new spec is ingested,
	//      prior specs untouched (append, not rebuild). ----
	writeHTML(t, convert, "Rel-18", "29502-i40.html", specHTML("SMF services"))
	st3, err := ingest.Run(ctx, dbPath, ingest.Options{
		ConvertDir: convert, EnableFTS: false, Embedder: embed.Disabled{}, Logf: quiet, Resume: true,
	})
	if err != nil {
		t.Fatalf("run3 (resume, +1 spec): %v", err)
	}
	if st3.Specs != 1 {
		t.Errorf("run3 processed %d specs; only the new one should run (want 1)", st3.Specs)
	}
	if c := clauseCount(t, dbPath, "29.502"); c == 0 {
		t.Error("new spec 29.502 was not ingested on resume")
	}
	// Prior specs must be byte-for-byte intact (Reset would have wiped them).
	if c := clauseCount(t, dbPath, "23.501"); c != base23 {
		t.Errorf("23.501 lost/changed on append resume: %d != %d (Reset leaked?)", c, base23)
	}
	if c := clauseCount(t, dbPath, "24.501"); c != base24 {
		t.Errorf("24.501 lost/changed on append resume: %d != %d", c, base24)
	}
}
