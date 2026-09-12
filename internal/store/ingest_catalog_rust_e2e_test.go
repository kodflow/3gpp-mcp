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
// a spec with no title/WG; the Rust `ingest-catalog` binary reads the catalogue projection
// `discover --emit-catalog` derives from status-report.htm, plus Releases.aspx, and overlays
// authoritative title/TS-or-TR/WG onto the spec and freeze_date onto its version; Go reads
// the enriched rows back. Additive + cite-or-silent.
//
// IT FEEDS THE PROJECTION, not the report. The overlay took `--status-report` until
// 2026-09-12, and the reason it does not any more is that the report cannot be equal to
// itself twice (a fresh CSP nonce, CSRF token, GDPR session id and Cloudflare e-mail
// obfuscation on every response), which made `enrich` dirty on a 6 h clock and replayed
// the whole 3GPP write side behind it. The four fields below are the whole of what this
// overlay consumes — that is why projecting onto them is safe.
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

	// The projection, in the shape `discover --emit-catalog` writes it: spec_id,
	// doc_type, working_group, title — tab separated, one line per spec.
	catalog := filepath.Join(dir, "catalog-specs.tsv")
	if err := os.WriteFile(catalog, []byte("23.501\tTS\tS2\tSystem architecture for the 5G System\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	releases := filepath.Join(dir, "releases.htm")
	if err := os.WriteFile(releases, []byte(`<html><body><table>
  <tr><td>Rel-19</td><td>Release 19</td><td>Open</td><td>2023-03-24</td><td>2025-06-20 (SA#108)</td><td></td></tr>
</table></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(bin, "--db", db, "--catalog", catalog, "--releases", releases).CombinedOutput()
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
