package httpd

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"tallpine.eu/tallpine/backend/internal/config"
)

func TestNewRouterAllowsNilLogger(t *testing.T) {
	router := newTestRouter(config.Config{}, nil, nil)
	rec := httptest.NewRecorder()
	req := loopbackRequest(http.MethodGet, "/healthz", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/healthz status = %d, want 200", rec.Code)
	}
}
