package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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
	if len(rows) != 6 {
		t.Fatalf("len(rows)=%d, want 6", len(rows))
	}
	for _, want := range []struct{ key, state, badge string }{
		{"embedder", "on", "on"},
		{"vector", "degraded", "exact-scan"},
		{"sparse", "unavailable", "absent"},
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

func TestParseRPCBrief(t *testing.T) {
	cases := []struct {
		name, body, rpc, tool, query string
	}{
		{"tools/call", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_spec","arguments":{"query":"AMF registration","top_k":10}}}`, "tools/call", "search_spec", "AMF registration"},
		{"initialize", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2.0"}}`, "initialize", "", ""},
		{"no-query-arg", `{"method":"tools/call","params":{"name":"list_specs","arguments":{"series":"23"}}}`, "tools/call", "list_specs", ""},
		{"garbage", `not json`, "", "", ""},
		{"empty", ``, "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rpc, tool, query := parseRPCBrief([]byte(c.body))
			if rpc != c.rpc || tool != c.tool || query != c.query {
				t.Errorf("parseRPCBrief = (%q,%q,%q), want (%q,%q,%q)", rpc, tool, query, c.rpc, c.tool, c.query)
			}
		})
	}
}

func TestRequestsJSONAuthAndShape(t *testing.T) {
	c := metrics.New()
	for i := 0; i < 5; i++ {
		c.Record(metrics.ReqLog{Method: "POST", RPC: "tools/call", Tool: "search_spec", Query: "q", DurMs: float64(i), Status: 200})
	}
	h := requestsJSONHandler(c, testTok)

	// No token → 401.
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/dashboard/requests.json", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-token status=%d, want 401", rec.Code)
	}

	// Valid token + limit honoured; newest-first; shape carries the feed keys.
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/dashboard/requests.json?token="+testTok+"&limit=3", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rec.Code)
	}
	var out struct {
		Requests []metrics.ReqLog `json:"requests"`
		NowUnix  int64            `json:"now_unix"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, rec.Body.String())
	}
	if len(out.Requests) != 3 {
		t.Fatalf("limit not honoured: got %d, want 3", len(out.Requests))
	}
	if out.Requests[0].DurMs != 4 { // newest first
		t.Errorf("not newest-first: first DurMs=%.0f, want 4", out.Requests[0].DurMs)
	}
	for _, key := range []string{`"method":"POST"`, `"tool":"search_spec"`, `"dur_ms":`, `"status":200`, `"now_unix":`} {
		if !strings.Contains(rec.Body.String(), key) {
			t.Errorf("requests.json missing key %s", key)
		}
	}
}

// TestMetricsMiddlewareObservesPostOnly: a GET (the long-lived SSE stream) is
// Recorded into the feed but NOT folded into the latency percentiles, while a
// POST is both Observed and Recorded.
func TestMetricsMiddlewareObservesPostOnly(t *testing.T) {
	c := metrics.New()
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	mw := metricsMiddleware(c, next)

	// A GET (SSE pull) — recorded, not observed.
	mw.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/mcp", nil))
	if s := c.Snapshot(); s.Total != 0 {
		t.Errorf("GET observed into percentiles: total=%d, want 0", s.Total)
	}

	// A POST tools/call — observed AND recorded with the parsed tool+query.
	body := `{"method":"tools/call","params":{"name":"search_spec","arguments":{"query":"hello"}}}`
	mw.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body)))
	if s := c.Snapshot(); s.Total != 1 {
		t.Errorf("POST not observed: total=%d, want 1", s.Total)
	}
	feed := c.Recent(10)
	if len(feed) != 2 {
		t.Fatalf("feed len=%d, want 2 (GET + POST)", len(feed))
	}
	if feed[0].Tool != "search_spec" || feed[0].Query != "hello" || feed[0].Method != "POST" {
		t.Errorf("POST feed entry = %+v, want tool=search_spec query=hello method=POST", feed[0])
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

func TestResolveDashToken(t *testing.T) {
	cases := []struct {
		env             string
		set             bool
		wantAuthOff     bool
		wantFixed       bool
		wantTokenIsEnv  bool // token must equal env verbatim (fixed mode)
		wantTokenRandom bool // token must be a fresh random (unset mode)
	}{
		{set: false, wantTokenRandom: true},
		{env: "", set: true, wantTokenRandom: true},
		{env: "off", set: true, wantAuthOff: true},
		{env: "OFF", set: true, wantAuthOff: true},
		{env: "none", set: true, wantAuthOff: true},
		{env: "disabled", set: true, wantAuthOff: true},
		{env: "0", set: true, wantAuthOff: true},
		{env: "my-stable-token", set: true, wantFixed: true, wantTokenIsEnv: true},
	}
	for _, c := range cases {
		if c.set {
			t.Setenv("DASHBOARD_TOKEN", c.env)
		} else {
			_ = os.Unsetenv("DASHBOARD_TOKEN")
		}
		tok, authOff, fixed := resolveDashToken()
		if authOff != c.wantAuthOff || fixed != c.wantFixed {
			t.Errorf("resolveDashToken(%q) authOff=%v fixed=%v, want authOff=%v fixed=%v", c.env, authOff, fixed, c.wantAuthOff, c.wantFixed)
		}
		if c.wantAuthOff && tok != dashAuthOpen {
			t.Errorf("resolveDashToken(%q) token=%q, want dashAuthOpen sentinel", c.env, tok)
		}
		if c.wantTokenIsEnv && tok != c.env {
			t.Errorf("resolveDashToken(%q) token=%q, want verbatim env", c.env, tok)
		}
		if c.wantTokenRandom && len(tok) != 20 {
			t.Errorf("resolveDashToken(%q) token len=%d, want 20 (random)", c.env, len(tok))
		}
	}
}

func TestAuthDisabledOpensDashboard(t *testing.T) {
	// DASHBOARD_TOKEN=off → resolveDashToken yields the open sentinel → every endpoint
	// passes WITHOUT a token (the operator's "stop re-fetching the token" knob).
	t.Setenv("DASHBOARD_TOKEN", "off")
	tok, authOff, _ := resolveDashToken()
	if !authOff {
		t.Fatal("expected authOff=true for DASHBOARD_TOKEN=off")
	}
	page := dashboardPageHandler(tok)
	rec := httptest.NewRecorder()
	page(rec, httptest.NewRequest(http.MethodGet, "/dashboard", nil)) // no ?token=
	if rec.Code != http.StatusOK {
		t.Errorf("auth-off page status=%d, want 200 (open, no token)", rec.Code)
	}
}
