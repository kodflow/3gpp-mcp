package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kodflow/3gpp-mcp/internal/metrics"
)

func TestDashboardPageServes(t *testing.T) {
	rec := httptest.NewRecorder()
	dashboardPageHandler(rec, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"3gpp-mcp", "/dashboard.json", "ONNX embedder", "Requests / minute"} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard HTML missing %q", want)
		}
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type=%q, want text/html", ct)
	}
}

func TestDashboardJSONLoadingThenReady(t *testing.T) {
	coll := metrics.New()

	// Loading: getStatic reports not-ready → 503 {"status":"loading"}.
	loading := dashboardJSONHandler(func() (dashStatic, bool) { return dashStatic{}, false }, coll)
	rec := httptest.NewRecorder()
	loading(rec, httptest.NewRequest(http.MethodGet, "/dashboard.json", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("loading status=%d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "loading") {
		t.Errorf("loading body=%q, want status loading", rec.Body.String())
	}

	// Ready: fill a snapshot + record a couple of requests, expect a full payload.
	coll.Observe(5 * time.Millisecond)
	coll.Observe(15 * time.Millisecond)
	static := dashStatic{
		Version: "test", Baseline: "latest", OnnxEnabled: true, Semantic: true,
		Hnsw: true, Fts: true, EmbeddingModel: "265de25f90b8",
		EmbeddedClauses: 2_855_221, TotalClauses: 2_855_221, Specs: 150,
		StartedUnix: time.Now().Add(-90 * time.Second).Unix(),
	}
	ready := dashboardJSONHandler(func() (dashStatic, bool) { return static, true }, coll)
	rec = httptest.NewRecorder()
	ready(rec, httptest.NewRequest(http.MethodGet, "/dashboard.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("ready status=%d, want 200", rec.Code)
	}
	var got dashboardData
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if !got.OnnxEnabled || !got.Semantic {
		t.Errorf("flags lost: onnx=%v semantic=%v", got.OnnxEnabled, got.Semantic)
	}
	if got.EmbeddedClauses != 2_855_221 || got.Specs != 150 {
		t.Errorf("static facts lost: emb=%d specs=%d", got.EmbeddedClauses, got.Specs)
	}
	if got.Metrics.Total != 2 {
		t.Errorf("metrics.total=%d, want 2", got.Metrics.Total)
	}
	if got.UptimeSec < 80 {
		t.Errorf("uptime=%d, want ~90", got.UptimeSec)
	}
}
