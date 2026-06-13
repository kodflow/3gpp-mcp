package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/metrics"
	"github.com/kodflow/3gpp-mcp/internal/search"
)

const testTok = "TESTtoken1234567890X" // 20 chars

// loadingGetter stands in for the live getter before the corpus is ready.
func loadingGetter() (dashStatic, *search.Engine, bool) { return dashStatic{}, nil, false }

// TestDashboardProvenanceJSONContract pins the data-layer provenance keys the
// operator curls to diagnose a stale inherited data layer. These keys are the
// fix for the 23h-blind incident — they must not silently disappear.
func TestDashboardProvenanceJSONContract(t *testing.T) {
	out := dashboardData{dashStatic: dashStatic{
		HNSWState:        "frozen",
		FTSIndexPresent:  true,
		DataImageCreated: "2026-06-12T17:00:00Z",
		SourceCorpus:     "sha256:deadbeef",
		DBPath:           "/data/mcp-3gpp/3gpp.duckdb",
		DBSizeBytes:      22111 * 1024 * 1024,
		DBMTimeUnix:      1749747600,
	}}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{
		`"hnsw_state":"frozen"`,
		`"fts_index_present":true`,
		`"data_image_created":"2026-06-12T17:00:00Z"`,
		`"source_corpus":"sha256:deadbeef"`,
		`"db_path":"/data/mcp-3gpp/3gpp.duckdb"`,
		`"db_size_bytes":`,
		`"db_mtime_unix":`,
	} {
		if !strings.Contains(string(b), key) {
			t.Errorf("dashboard.json missing provenance key %s\nin: %s", key, b)
		}
	}
}

func TestDashboardPageAuthGate(t *testing.T) {
	page := dashboardPageHandler(testTok)

	// No token → 401 + the login hint, NEVER the dashboard.
	rec := httptest.NewRecorder()
	page(rec, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-token status=%d, want 401", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "rafraîchissement") {
		t.Error("dashboard body leaked without a token")
	}

	// Valid ?token → 200, real page, and a cookie so the JSON fetch authenticates.
	rec = httptest.NewRecorder()
	page(rec, httptest.NewRequest(http.MethodGet, "/dashboard?token="+testTok, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("token status=%d, want 200", rec.Code)
	}
	for _, want := range []string{"3gpp-mcp", "/dashboard.json", "/dashboard/toggle", "À quoi sert chaque option", "ONNX embedder"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("dashboard HTML missing %q", want)
		}
	}
	if !strings.Contains(rec.Header().Get("Set-Cookie"), "dash_token=") {
		t.Error("query-token page must set the dash_token cookie")
	}
}

func TestDashboardJSONAuthAndLoading(t *testing.T) {
	h := dashboardJSONHandler(loadingGetter, metrics.New(), testTok)

	// No token → 401.
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/dashboard.json", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-token status=%d, want 401", rec.Code)
	}

	// Valid token (query) but not ready → 503 loading.
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/dashboard.json?token="+testTok, nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("loading status=%d, want 503", rec.Code)
	}

	// Token via cookie also passes the gate (so the browser fetch works).
	req := httptest.NewRequest(http.MethodGet, "/dashboard.json", nil)
	req.AddCookie(&http.Cookie{Name: "dash_token", Value: testTok})
	rec = httptest.NewRecorder()
	h(rec, req)
	if rec.Code == http.StatusUnauthorized {
		t.Error("cookie auth was rejected")
	}

	// Token via Bearer also passes.
	req = httptest.NewRequest(http.MethodGet, "/dashboard.json", nil)
	req.Header.Set("Authorization", "Bearer "+testTok)
	rec = httptest.NewRecorder()
	h(rec, req)
	if rec.Code == http.StatusUnauthorized {
		t.Error("bearer auth was rejected")
	}
}

func TestToggleAuthGate(t *testing.T) {
	h := toggleHandler(loadingGetter, testTok)

	// No token → 401 (a mutating endpoint MUST be protected).
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/dashboard/toggle?name=vector&on=false", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-token status=%d, want 401", rec.Code)
	}
	// Wrong token → 401.
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/dashboard/toggle?token=WRONGWRONGWRONGWRONG0&name=vector&on=false", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong-token status=%d, want 401", rec.Code)
	}
	// GET with a VALID token → 405 (mutating endpoint is POST-only; a GET must not
	// flip a toggle even with the token — CSRF-like / accidental pre-fetch).
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/dashboard/toggle?token="+testTok+"&name=vector&on=false", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET status=%d, want 405", rec.Code)
	}
}

// TestOptionRows pins the explicit per-option state matrix: each pill must
// carry an explicit state + reason so the dashboard never shows a bare on/off.
func TestOptionRows(t *testing.T) {
	get := func(rows []capOption, key string) capOption {
		t.Helper()
		for _, o := range rows {
			if o.Key == key {
				return o
			}
		}
		t.Fatalf("option %q missing", key)
		return capOption{}
	}

	// Prod-today shape: embedder + vectors + FTS, but NO frozen HNSW, no reranker
	// → Semantic must say "degraded/exact-scan", HNSW "unavailable/non gelé".
	noHNSW := search.State{
		EmbedderEnabled: true, FTSEnabled: true, HNSWFrozen: false, RerankerEnabled: false,
		LexicalOn: true, VectorOn: true, HNSWOn: true, EmbedderModelID: "m1",
	}
	rows := optionRows(noHNSW, "m1")
	if len(rows) != 5 {
		t.Fatalf("len(rows)=%d, want 5", len(rows))
	}
	for _, want := range []struct{ key, state, badge string }{
		{"embedder", "on", "on"},
		{"vector", "degraded", "exact-scan"},
		{"hnsw", "unavailable", "non gelé"},
		{"lexical", "on", "on"},
		{"rerank", "unavailable", "n/a"},
	} {
		o := get(rows, want.key)
		if o.State != want.state || o.Badge != want.badge {
			t.Errorf("%s = (%s,%s), want (%s,%s)", want.key, o.State, o.Badge, want.state, want.badge)
		}
		if o.Reason == "" {
			t.Errorf("%s has no reason — every option must be explicit", want.key)
		}
		if o.State != "on" && o.Fix == "" {
			t.Errorf("%s is %s but has no fix hint", want.key, o.State)
		}
	}

	// Everything baked + on → Semantic on (HNSW), reranker available on demand.
	full := search.State{
		EmbedderEnabled: true, FTSEnabled: true, HNSWFrozen: true, RerankerEnabled: true,
		LexicalOn: true, VectorOn: true, HNSWOn: true, RerankOn: false, EmbedderModelID: "m1",
	}
	rows = optionRows(full, "m1")
	if o := get(rows, "vector"); o.State != "on" || o.Badge != "on (HNSW)" {
		t.Errorf("vector = (%s,%s), want (on,on (HNSW))", o.State, o.Badge)
	}
	if o := get(rows, "hnsw"); o.State != "on" {
		t.Errorf("hnsw state = %s, want on", o.State)
	}
	if o := get(rows, "rerank"); o.State != "degraded" || o.Badge != "à la demande" {
		t.Errorf("rerank = (%s,%s), want (degraded,à la demande)", o.State, o.Badge)
	}

	// Model mismatch severs the vector arm with an explicit, named reason.
	rows = optionRows(full, "OTHER")
	o := get(rows, "vector")
	if o.State != "unavailable" {
		t.Errorf("mismatch vector state = %s, want unavailable", o.State)
	}
	if !strings.Contains(o.Reason, "OTHER") || !strings.Contains(o.Reason, "m1") {
		t.Errorf("mismatch reason must name both models, got %q", o.Reason)
	}

	// Toggled-off arms report "off" + how to re-enable (not "unavailable").
	off := full
	off.VectorOn, off.HNSWOn, off.LexicalOn = false, false, false
	rows = optionRows(off, "m1")
	for _, key := range []string{"vector", "hnsw", "lexical"} {
		if o := get(rows, key); o.State != "off" {
			t.Errorf("%s state = %s, want off", key, o.State)
		}
	}
}

func TestEmptyTokenRejectsEverything(t *testing.T) {
	// crypto/rand failure path returns "" → the gate must reject all (fail-closed).
	page := dashboardPageHandler("")
	rec := httptest.NewRecorder()
	page(rec, httptest.NewRequest(http.MethodGet, "/dashboard?token=", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("empty-token page status=%d, want 401 (fail-closed)", rec.Code)
	}
}
