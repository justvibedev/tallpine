package httpd

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMobileFeatureGate(t *testing.T) {
	on := false
	h := mobileFeatureGate(func() bool { return on })(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	cases := []struct {
		method, path string
		offWant      int
	}{
		{http.MethodGet, "/api/v1/mobile/status", http.StatusNoContent},
		{http.MethodPost, "/api/v1/mobile/feature", http.StatusNoContent},
		{http.MethodPost, "/api/v1/mobile/enable", http.StatusNotFound},
		{http.MethodGet, "/api/v1/mobile/devices", http.StatusNotFound},
		{http.MethodPost, "/api/v1/push/devices", http.StatusNotFound},
		{http.MethodGet, "/api/v1/endpoints", http.StatusNotFound},
		{http.MethodGet, "/api/v1/tunnel", http.StatusNotFound},
		{http.MethodGet, "/api/v1/mobileapp", http.StatusNoContent},
		{http.MethodGet, "/api/v1/sessions", http.StatusNoContent},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != c.offWant {
			t.Errorf("off %s %s = %d, want %d", c.method, c.path, rec.Code, c.offWant)
		}
	}
	on = true
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/mobile/enable", nil))
	if rec.Code != http.StatusNoContent {
		t.Errorf("on enable = %d, want 204", rec.Code)
	}
}

func TestMobileFeatureGateNilIsPassthrough(t *testing.T) {
	h := mobileFeatureGate(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/mobile/enable", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d", rec.Code)
	}
}
