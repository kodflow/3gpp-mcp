package store

import (
	"context"
	"strings"
	"testing"
)

// AN EMPTY LIST COLUMN MUST NOT BREAK search_api.
//
// DuckDB's array_to_string returns NULL for an empty list rather than the empty
// string, and database/sql refuses to scan NULL into a Go string. The scan dies
// on the FIRST offending row, so a single schema with no enum values turned the
// whole call into:
//
//	search_api failed: sql: Scan error on column index 9,
//	name "array_to_string(enum_values, '\x1f')": converting NULL to string is unsupported
//
// Measured on the published corpus: 23 391 of 27 889 api_schemas rows are in that
// state. search_api was broken for essentially every query.
//
// WHY NO EXISTING TEST SAW IT. Every fixture goes through InsertAPISchemas, and
// `strings.Join(nil, sep)` is "" — which `string_split(”, sep)` turns into a
// list of ONE empty string, not an empty list. The Go writer is structurally
// incapable of producing the row that breaks the reader. The Rust ingest writes
// genuine empty lists, so only the real corpus had them.
//
// So this fixture writes the row the way PRODUCTION does: an empty list literal.
func TestSearchAPISurvivesEmptyListColumns(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// [] and not string_split('', sep): that is the difference that matters.
	if _, err := st.DB().ExecContext(ctx, `
		INSERT INTO api_schemas
		  (schema_id, spec_id, release, version, service, schema_name, kind, description,
		   properties, enum_values, refs_out, yaml_file, forge_sha, forge_url)
		VALUES (1, '29.571', 'Rel-18', '18.11.0', 'CommonData', 'Guami', 'object',
		        'globally unique AMF identifier',
		        []::VARCHAR[], []::VARCHAR[], []::VARCHAR[],
		        'TS29571_CommonData.yaml', 'deadbeef', 'https://example.invalid/x')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, `
		INSERT INTO api_operations
		  (op_id, spec_id, release, version, api_doc_version, service, service_family,
		   api_root, path, method, operation_id, summary, tags, request_schema,
		   response_codes, yaml_file, forge_sha, forge_url)
		VALUES (1, '29.518', 'Rel-18', '18.13.0', '1.0.0', 'Namf_Communication', 'Namf',
		        'namf-comm/v1', '/ue-contexts/{id}', 'PUT', 'CreateUEContext',
		        'create the UE context', []::VARCHAR[], '', []::VARCHAR[],
		        'TS29518_Namf_Communication.yaml', 'deadbeef', 'https://example.invalid/y')`); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ name, kind, query string }{
		{"schema", "schema", "Guami"},
		{"operation", "operation", "create UE context"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hits, err := st.SearchAPI(ctx, APISearchQuery{Text: tc.query, Kind: tc.kind, TopK: 5})
			if err != nil {
				if strings.Contains(err.Error(), "converting NULL to string") {
					t.Fatalf("an empty list column broke search_api entirely: %v", err)
				}
				t.Fatal(err)
			}
			if len(hits) == 0 {
				t.Fatalf("search_api(%s) found nothing — the row is there", tc.kind)
			}
		})
	}
}

// The empty list must read back as NO values, not as one empty string. Coalescing
// to "" and splitting on the separator gives nil only because splitArr special-cases
// the empty string; pin it, because a caller that renders `[""]` as an enum value
// publishes a value the specification does not contain.
func TestEmptyListReadsBackAsNoValues(t *testing.T) {
	if got := splitArr(""); got != nil {
		t.Fatalf("splitArr(%q) = %#v, want nil — an empty list is not one empty value", "", got)
	}
}
