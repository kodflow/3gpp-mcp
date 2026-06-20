package store

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestRustIngestLiE2E proves the LI subject (Phase 8b): the Rust `ingest-li` binary parses
// a TS 33.128 ASN.1 module into the LI event registry + asn1 type catalogue via store-rs,
// and the Go serve side reads the li_events / li_event_fields / asn1_types back.
//
// Skipped unless RUST_INGEST_LI points at the built binary.
func TestRustIngestLiE2E(t *testing.T) {
	bin := os.Getenv("RUST_INGEST_LI")
	if bin == "" {
		t.Skip("RUST_INGEST_LI not set — build rust/ingest ingest-li first")
	}
	ctx := context.Background()
	dir := t.TempDir()

	// Real-shape module: opening brace on its own line (TS 33.128 style).
	asn := filepath.Join(dir, "TS33128Payloads.asn")
	const mod = `TS33128Payloads {itu-t(0) identified-organization(4) etsi(0) mobileDomain(0)
  threeGPPSA3LI(33128) ts33128(19) r18(18) version14(14)}
DEFINITIONS AUTOMATIC TAGS ::= BEGIN

XIRIEvent ::= CHOICE
{
  -- AMF events, see clause 6.2.2
  registration [1] AMFRegistration,
  deregistration [2] AMFDeregistration
}

AMFRegistration ::= SEQUENCE
{
  registrationType [1] RegistrationType,
  supi [2] SUPI OPTIONAL
}

RegistrationType ::= ENUMERATED
{
  initial(1),
  mobility(2)
}
END
`
	if err := os.WriteFile(asn, []byte(mod), 0o644); err != nil {
		t.Fatal(err)
	}

	db := filepath.Join(dir, "li.duckdb")
	out, err := exec.Command(bin, "--asn", asn, "--db", db).CombinedOutput()
	if err != nil {
		t.Fatalf("rust ingest-li: %v\n%s", err, out)
	}
	t.Logf("ingest-li: %s", out)

	s, err := OpenReadOnly(db)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = s.Close() }()

	var events, fields, types int
	if err := s.DB().QueryRowContext(ctx, "SELECT count(*) FROM li_events").Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if err := s.DB().QueryRowContext(ctx, "SELECT count(*) FROM li_event_fields WHERE event_name='AMFRegistration'").Scan(&fields); err != nil {
		t.Fatalf("count fields: %v", err)
	}
	if err := s.DB().QueryRowContext(ctx, "SELECT count(*) FROM asn1_types").Scan(&types); err != nil {
		t.Fatalf("count types: %v", err)
	}
	if events != 2 || fields != 2 || types < 1 {
		t.Fatalf("li registry mismatch: events=%d fields=%d types=%d (want 2/2/>=1)", events, fields, types)
	}

	var nf, domain, release string
	if err := s.DB().QueryRowContext(ctx, "SELECT originating_nf, domain, release FROM li_events WHERE event_name='registration'").Scan(&nf, &domain, &release); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if nf != "AMF" || domain != "5GC" || release != "Rel-18" {
		t.Fatalf("event attrs: nf=%q domain=%q release=%q (want AMF/5GC/Rel-18)", nf, domain, release)
	}
	t.Logf("Rust LI registry → Go read OK: %d events, %d fields, %d types", events, fields, types)
}
