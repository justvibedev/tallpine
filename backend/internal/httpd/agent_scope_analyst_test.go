package httpd

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAnalystDeniedRefusesEveryWrite(t *testing.T) {
	for _, path := range []string{
		"/api/v1/reports",
		"/api/v1/sessions/s1/send",
		"/api/v1/sessions/s1/conversation/steer-or-send",
		"/api/v1/sessions/s1/conversation/messages",
		"/api/v1/sessions/s1/conversation/steer",
		"/api/v1/projects/p/asks",
		"/api/v1/orchestrators/delegate",
		"/api/v1/sessions",
	} {
		if !analystDenied(httptest.NewRequest(http.MethodPost, path, nil), "a1") {
			t.Errorf("POST %s must be refused for a context analyst", path)
		}
	}
	for _, m := range []string{http.MethodGet, http.MethodHead} {
		if analystDenied(httptest.NewRequest(m, "/api/v1/reports", nil), "a1") {
			t.Errorf("%s must stay allowed", m)
		}
	}
}

func TestAnalystMayPostItsOwnActivityOnly(t *testing.T) {
	if analystDenied(httptest.NewRequest(http.MethodPost, "/api/v1/sessions/a1/activity", nil), "a1") {
		t.Error("the analyst's own activity hook must be allowed")
	}
	if !analystDenied(httptest.NewRequest(http.MethodPost, "/api/v1/sessions/other/activity", nil), "a1") {
		t.Error("another session's activity must be refused")
	}
}
