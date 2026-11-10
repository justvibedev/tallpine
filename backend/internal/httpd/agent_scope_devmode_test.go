package httpd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/daemontoken"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// Dev mode lifts orchestrator limits only while the owner-set policy is on;
// workers stay scoped and no agent token can change Dev mode.
func TestAgentScopeDevMode(t *testing.T) {
	base, tokens := newAgentScopeFixture()
	on := false
	scopes := newAgentScopes(tokens, base, nil)
	scopes.devMode = func(context.Context, domain.ProjectID) bool { return on }
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := daemonAuthMiddleware(testDaemonToken, "", nil, scopes, nil)(inner)
	orch, worker := base.tokenFor(tokens, "app-orch"), base.tokenFor(tokens, "app-1")

	orchNew := `{"projectId":"web","clean":true}`
	protect := `{"changes":[{"project":"app","key":"policy.protectState.mode","value":"off"}]}`
	type tc struct {
		name, token, method, path, body string
		want                            int
	}
	run := func(cases []tc) {
		for _, c := range cases {
			req := laneReq(c.method, c.path, c.body, c.token)
			rec := serve(h, req)
			if rec != c.want {
				t.Errorf("%s = %d, want %d", c.name, rec, c.want)
			}
		}
	}
	run([]tc{
		{"off: cross-project orchestrator new", orch, "POST", "/api/v1/orchestrators", orchNew, 403},
		{"off: protectState", orch, "PUT", "/api/v1/projects/web/config", `{}`, 403},
	})
	on = true
	run([]tc{
		{"on: cross-project orchestrator new", orch, "POST", "/api/v1/orchestrators", orchNew, 200},
		{"on: other project config", orch, "PUT", "/api/v1/projects/web/config", `{}`, 200},
		{"on: settings write", orch, "PATCH", "/api/v1/settings/values", protect, 200},
		{"on: mobile status", orch, "GET", "/api/v1/mobile/status", "", 403},
		{"on: push devices", orch, "POST", "/api/v1/push/devices", "{}", 403},
		{"on: mobile regenerate", orch, "POST", "/api/v1/mobile/regenerate", "", 403},
		{"on: worker stays scoped", worker, "POST", "/api/v1/orchestrators", orchNew, 403},
		{"on: worker config", worker, "PUT", "/api/v1/projects/web/config", `{}`, 403},
	})
}

func laneReq(method, path, body, token string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set(daemontoken.Header, token)
	return req
}

func serve(h http.Handler, req *http.Request) int {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}
