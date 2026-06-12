package main

import (
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
