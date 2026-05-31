package ingest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/embed"
	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
	"github.com/kodflow/3gpp-mcp/internal/subject"
)

// fakeSubject is a test-only Subject that activates on one spec id and lets the
// test decide whether Ingest fails. When purger is true it also records the
// (specID, release) it was asked to Purge so the resume fan-out can be asserted.
type fakeSubject struct {
	spec      string
	ingestErr error
	purger    bool
	purged    *[]string // appended "<specID>/<release>" on Purge
}

func (f *fakeSubject) Name() string             { return "fake" }
func (f *fakeSubject) Activates(id string) bool { return id == f.spec }
func (f *fakeSubject) Ingest(context.Context, *store.Store, subject.IngestContext) (int, error) {
	if f.ingestErr != nil {
		return 0, f.ingestErr
	}
	return 0, nil
}
func (f *fakeSubject) Tools(*store.Store, string) []subject.ToolRegistration { return nil }
func (f *fakeSubject) Purge(_ context.Context, _ *store.Store, sid, release, _ string) error {
	if f.purger && f.purged != nil {
		*f.purged = append(*f.purged, sid+"/"+release)
	}
	return nil
}

// writeSpecHTML drops a minimal LibreOffice-shaped 3GPP HTML so Run's globber +
// htmlparse pick it up. The filename encodes spec 33.128, Rel-19, v19.6.0.
func writeSpecHTML(t *testing.T, convertDir string) {
	t.Helper()
	relDir := filepath.Join(convertDir, "Rel-19")
	if err := os.MkdirAll(relDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const html = "<html><head><title>x</title></head><body>" +
		"<h1>1\tScope</h1><p>The present document specifies lawful interception.</p>" +
		"<h2>6.2.2.2\tGeneration of xIRI over LI_X2</h2><p>intro</p>" +
		"</body></html>"
	if err := os.WriteFile(filepath.Join(relDir, "33128-j60.html"), []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSubjectErrorFailsScope locks the strict-resume error contract: a subject
// Ingest WRITE error must fail the whole (spec, version) scope so the ingest_log
// row is left 'started' (never flipped to 'done'). Before the fix the error was
// logged-and-continued and MarkIngestDone ran anyway, leaving a half-written
// subject marked done and skipped forever by --resume.
func TestSubjectErrorFailsScope(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	convertDir := filepath.Join(dir, "convert")
	writeSpecHTML(t, convertDir)
	dbPath := filepath.Join(dir, "shard.duckdb")

	wantErr := errors.New("transient write failure")
	reg := subject.New(&fakeSubject{spec: "33.128", ingestErr: wantErr})

	_, err := Run(ctx, dbPath, Options{
		ConvertDir: convertDir,
		Embedder:   embed.Disabled{},
		Registry:   reg,
	})
	if err == nil || !errors.Is(err, wantErr) {
		t.Fatalf("Run should propagate the subject error, got %v", err)
	}

	// The spec must NOT be marked done — the row stays 'started' so a --resume
	// run purges + redoes it instead of skipping it forever.
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	pv := pipelineVersionForTest()
	done, err := st.IsIngestDone(ctx, "33.128", "19.6.0", pv)
	if err != nil {
		t.Fatal(err)
	}
	if done {
		t.Fatal("spec marked done despite a subject write error (resume would skip it forever)")
	}
}

// TestResumeFansOutSubjectPurge locks the purge contract: on a --resume redo of a
// 'started' (spec, version), filterResumeJobs must call the subject's Purge for
// that scope alongside the core PurgeSpecScope. Before the fix PurgeSpecScope only
// touched core tables and subject rows survived the "clean" redo.
func TestResumeFansOutSubjectPurge(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "shard.duckdb")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if err := st.Reset(ctx); err != nil {
		t.Fatal(err)
	}

	pv := "v1"
	// Leave 33.128 'started' (half-ingested) so the resume redo path fires.
	if err := st.MarkIngestStarted(ctx, "33.128", "19.6.0", pv); err != nil {
		t.Fatal(err)
	}

	var purged []string
	reg := subject.New(&fakeSubject{spec: "33.128", purger: true, purged: &purged})
	jobs := []ingestJob{{path: "x", specID: "33.128", release: "Rel-19", version: "19.6.0"}}

	out, skipped, err := filterResumeJobs(ctx, st, reg, jobs, true, pv, func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 0 || len(out) != 1 {
		t.Fatalf("started job should be re-queued: out=%d skipped=%d", len(out), skipped)
	}
	if len(purged) != 1 || purged[0] != "33.128/Rel-19" {
		t.Fatalf("subject Purge not fanned out for the redone scope, got %v", purged)
	}
}

// pipelineVersionForTest mirrors Run's pipeline-version derivation for the
// Disabled embedder so the test can query IsIngestDone with the same key.
func pipelineVersionForTest() string {
	return model.PipelineVersion(embed.Disabled{}.ModelID())
}
