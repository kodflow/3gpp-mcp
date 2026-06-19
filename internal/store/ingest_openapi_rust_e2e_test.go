package store

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestRustIngestOpenapiE2E proves the Rust OpenAPI overlay (Phase 6): the Rust
// `ingest-openapi` binary parses a 5GC OpenAPI YAML corpus (parse3gpp::openapi) and writes
// the api_* tables via store-rs, then the Go serve side reads the operations + schemas back
// read-only. Additive — clauses are untouched.
//
// Skipped unless RUST_INGEST_OPENAPI points at the built binary.
func TestRustIngestOpenapiE2E(t *testing.T) {
	bin := os.Getenv("RUST_INGEST_OPENAPI")
	if bin == "" {
		t.Skip("RUST_INGEST_OPENAPI not set — build rust/ingest ingest-openapi first")
	}
	ctx := context.Background()
	dir := t.TempDir()

	// Corpus layout <src>/<Rel>/<sha>/<file>.yaml.
	yDir := filepath.Join(dir, "5g-apis", "Rel-18", "abc123")
	if err := os.MkdirAll(yDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const yaml = `openapi: 3.0.0
info:
  version: 1.2.3
externalDocs:
  description: "3GPP TS 29.518 V18.13.0; 5G System; Access and Mobility Management Services"
servers:
  - url: "{apiRoot}/namf-comm/v1"
paths:
  /ue-contexts/{ueContextId}:
    put:
      operationId: CreateUEContext
      summary: Create a UE context
      tags: [ Individual UE Context ]
      responses:
        "201": {}
components:
  schemas:
    AccessType:
      anyOf:
        - type: string
          enum: [ "3GPP_ACCESS", "NON_3GPP_ACCESS" ]
    UeContextCreateData:
      type: object
      properties:
        ueContext:
          $ref: "#/components/schemas/UeContext"
`
	if err := os.WriteFile(filepath.Join(yDir, "TS29518_Namf_Communication.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	db := filepath.Join(dir, "api.duckdb")
	out, err := exec.Command(bin, "--src", filepath.Join(dir, "5g-apis"), "--db", db).CombinedOutput()
	if err != nil {
		t.Fatalf("rust ingest-openapi: %v\n%s", err, out)
	}
	t.Logf("ingest-openapi: %s", out)

	s, err := OpenReadOnly(db)
	if err != nil {
		t.Fatalf("open Rust-ingested API DB: %v", err)
	}
	defer func() { _ = s.Close() }()

	var ops, schemas int
	var specID, method, release string
	if err := s.DB().QueryRowContext(ctx, "SELECT count(*) FROM api_operations").Scan(&ops); err != nil {
		t.Fatalf("count ops: %v", err)
	}
	if err := s.DB().QueryRowContext(ctx, "SELECT count(*) FROM api_schemas").Scan(&schemas); err != nil {
		t.Fatalf("count schemas: %v", err)
	}
	if err := s.DB().QueryRowContext(ctx, "SELECT spec_id, method, release FROM api_operations LIMIT 1").Scan(&specID, &method, &release); err != nil {
		t.Fatalf("read op: %v", err)
	}
	if ops != 1 || schemas != 2 || specID != "29.518" || method != "PUT" || release != "Rel-18" {
		t.Fatalf("api overlay mismatch: ops=%d schemas=%d spec=%s method=%s rel=%s (want 1/2/29.518/PUT/Rel-18)", ops, schemas, specID, method, release)
	}

	// the enum schema kept its values list.
	var enumKind string
	if err := s.DB().QueryRowContext(ctx, "SELECT kind FROM api_schemas WHERE schema_name='AccessType'").Scan(&enumKind); err != nil {
		t.Fatalf("read AccessType: %v", err)
	}
	if enumKind != "enum" {
		t.Fatalf("AccessType kind = %q, want enum", enumKind)
	}
	t.Logf("Rust OpenAPI overlay → Go read OK: %d ops, %d schemas", ops, schemas)
}
