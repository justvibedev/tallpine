package httpd

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNoStoreAPIMiddleware(t *testing.T) {
	h := noStoreAPIMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/blob" {
			w.Header().Set("Cache-Control", "private, no-cache")
		}
		w.WriteHeader(http.StatusOK)
	}))
	cases := []struct{ path, want string }{
		{"/api/v1/sessions", "no-store"},
		{"/api/v1/blob", "private, no-cache"},
		{"/health", ""},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if got := rec.Header().Get("Cache-Control"); got != tc.want {
			t.Errorf("%s: Cache-Control = %q, want %q", tc.path, got, tc.want)
		}
	}
}
