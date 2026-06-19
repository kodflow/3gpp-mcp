package store

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// TestRustIngestCatalogE2E proves the Rust DynaReport overlay (Phase 7): Go ingest creates
// a spec with no title/WG; the Rust `ingest-catalog` binary parses status-report.htm +
// Releases.aspx and overlays authoritative title/TS-or-TR/WG onto the spec and freeze_date
// onto its version; Go reads the enriched rows back. Additive + cite-or-silent.
//
// Skipped unless RUST_INGEST_CATALOG points at the built binary.
func TestRustIngestCatalogE2E(t *testing.T) {
	bin := os.Getenv("RUST_INGEST_CATALOG")
	if bin == "" {
		t.Skip("RUST_INGEST_CATALOG not set — build rust/ingest ingest-catalog first")
	}
	ctx := context.Background()
	dir := t.TempDir()
	db := filepath.Join(dir, "c.duckdb")

	// 1. Content ingest created the spec + version (no title/WG yet).
	s, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertSpec(model.Spec{SpecID: "23.501", Series: "23", DocType: "TS"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertVersion(model.SpecVersion{SpecID: "23.501", Release: "Rel-19", Version: "19.5.0"}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	statusReport := filepath.Join(dir, "status-report.htm")
	if err := os.WriteFile(statusReport, []byte(`<html><body>
<a name="activeRel-19"></a>
<table>
  <tr><th>Type</th><th>Spec num</th><th>Title</th><th>Vers</th><th>WG</th></tr>
  <tr><td>TS</td><td>23.501</td><td>System architecture for the 5G System</td><td>19.5.0</td><td>S2</td></tr>
</table></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	releases := filepath.Join(dir, "releases.htm")
	if err := os.WriteFile(releases, []byte(`<html><body><table>
  <tr><td>Rel-19</td><td>Release 19</td><td>Open</td><td>2023-03-24</td><td>2025-06-20 (SA#108)</td><td></td></tr>
</table></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(bin, "--db", db, "--status-report", statusReport, "--releases", releases).CombinedOutput()
	if err != nil {
		t.Fatalf("rust ingest-catalog: %v\n%s", err, out)
	}
	t.Logf("ingest-catalog: %s", out)

	s2, err := OpenReadOnly(db)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = s2.Close() }()

	var title, wg, source, freeze string
	if err := s2.DB().QueryRowContext(ctx, "SELECT title, working_group FROM specs WHERE spec_id='23.501'").Scan(&title, &wg); err != nil {
		t.Fatalf("read spec: %v", err)
	}
	if err := s2.DB().QueryRowContext(ctx, "SELECT COALESCE(CAST(freeze_date AS VARCHAR),''), COALESCE(metadata_source,'') FROM spec_versions WHERE spec_id='23.501'").Scan(&freeze, &source); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if title != "System architecture for the 5G System" || wg != "S2" {
		t.Fatalf("spec overlay mismatch: title=%q wg=%q", title, wg)
	}
	if freeze != "2025-06-20" || source != "dynareport" {
		t.Fatalf("version overlay mismatch: freeze=%q source=%q (want 2025-06-20/dynareport)", freeze, source)
	}
	t.Logf("Rust catalog overlay → Go read OK: title+WG+freeze_date applied")
}
