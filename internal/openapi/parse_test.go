package openapi

import (
	"path/filepath"
	"testing"
)

func TestParseFixture(t *testing.T) {
	path := filepath.Join("testdata", "TS29518_Namf_Communication.yaml")
	res, err := ParseFile(path, "Rel-18", "DEADBEEFCAFE")
	if err != nil {
		t.Fatal(err)
	}

	// externalDocs is authoritative for spec_id + version.
	if res.SpecID != "29.518" {
		t.Errorf("spec_id = %q, want 29.518", res.SpecID)
	}
	if res.Version != "18.13.0" {
		t.Errorf("version = %q, want 18.13.0", res.Version)
	}

	// Two HTTP methods on one path => two operations.
	if len(res.Operations) != 2 {
		t.Fatalf("operations = %d, want 2", len(res.Operations))
	}
	for _, o := range res.Operations {
		if o.Method == "PUT" {
			if o.OperationID != "CreateUEContext" {
				t.Errorf("PUT operationId = %q, want CreateUEContext", o.OperationID)
			}
			if o.RequestSchema != "UeContextCreateData" {
				t.Errorf("request_schema = %q, want UeContextCreateData", o.RequestSchema)
			}
			if o.APIRoot != "namf-comm/v1" {
				t.Errorf("api_root = %q, want namf-comm/v1", o.APIRoot)
			}
			if o.ServiceFamily != "Namf" {
				t.Errorf("service_family = %q, want Namf", o.ServiceFamily)
			}
			if len(o.ResponseCodes) != 3 { // 201, 400, 403
				t.Errorf("response_codes = %v, want 3", o.ResponseCodes)
			}
			if o.Locator() != "API namf-comm/v1 PUT /ue-contexts/{ueContextId} (CreateUEContext)" {
				t.Errorf("locator = %q", o.Locator())
			}
			if o.ForgeURL == "" || o.Cite().URL != o.ForgeURL {
				t.Errorf("citation url missing/mismatched: %q vs %q", o.Cite().URL, o.ForgeURL)
			}
		}
	}

	// Schemas: object props, cross-file refs, and an anyOf enum.
	byName := map[string]int{}
	for i, s := range res.Schemas {
		byName[s.SchemaName] = i
	}
	for _, want := range []string{"UeContextCreateData", "UeContext", "AccessType"} {
		if _, ok := byName[want]; !ok {
			t.Errorf("missing schema %q", want)
		}
	}
	ucd := res.Schemas[byName["UeContextCreateData"]]
	if ucd.Kind != "object" {
		t.Errorf("UeContextCreateData kind = %q, want object", ucd.Kind)
	}
	if !contains(ucd.RefsOut, "29.518:UeContext") || !contains(ucd.RefsOut, "29.571:NgRanTargetId") {
		t.Errorf("UeContextCreateData refs_out = %v, want same-file + cross-file edges", ucd.RefsOut)
	}
	at := res.Schemas[byName["AccessType"]]
	if at.Kind != "enum" {
		t.Errorf("AccessType kind = %q, want enum", at.Kind)
	}
	if !contains(at.EnumValues, "3GPP_ACCESS") || !contains(at.EnumValues, "NON_3GPP_ACCESS") {
		t.Errorf("AccessType enum = %v", at.EnumValues)
	}
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
