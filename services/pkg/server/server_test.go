package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	NewRouter("api").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"service":"api"`) {
		t.Fatalf("unexpected body %s", rec.Body.String())
	}
}

func TestMetrics(t *testing.T) {
	rec := httptest.NewRecorder()
	NewRouter("sip").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rec.Body.String(), `eximphone_up{service="sip"} 1`) {
		t.Fatalf("unexpected body %s", rec.Body.String())
	}
}
