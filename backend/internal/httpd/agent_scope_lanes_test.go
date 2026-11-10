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

type laneScopeStore struct {
	*fakeAgentScopeStore
	lanes map[domain.SessionID]string
}

func (s laneScopeStore) SessionLane(_ context.Context, id domain.SessionID) (domain.SessionLane, error) {
	return domain.SessionLane{LaneItemID: s.lanes[id]}, nil
}

func laneRequest(t *testing.T, h http.Handler, token, path, body string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set(daemontoken.Header, token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// The main orchestrator may start and end lanes only with the orchestratorLanes
// policy on; lane orchestrators, workers and other projects never may.
func TestAgentScopeLanePolicy(t *testing.T) {
	base, tokens := newAgentScopeFixture()
	laneOrch := scopeSession("app-lane", "app", domain.KindOrchestrator, "v-lane")
	base.sessions[laneOrch.ID] = laneOrch
	store := laneScopeStore{base, map[domain.SessionID]string{"app-lane": "B1"}}
	on := true
	scopes := newAgentScopes(tokens, store, nil)
	scopes.lanesEnabled = func(context.Context, domain.ProjectID) bool { return on }
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := daemonAuthMiddleware(testDaemonToken, "", nil, scopes, nil)(inner)

	main, lane, worker := base.tokenFor(tokens, "app-orch"), base.tokenFor(tokens, "app-lane"), base.tokenFor(tokens, "app-1")
	start := `{"projectId":"app","lane":"B1","prompt":"brief"}`
	end := `{"projectId":"app","lane":"B1"}`
	for _, tc := range []struct {
		name, token, path, body string
		want                    int
	}{
		{"main starts lane", main, "/api/v1/orchestrators", start, 200},
		{"main ends lane", main, "/api/v1/orchestrators/end", end, 200},
		{"end by session", main, "/api/v1/orchestrators/end", `{"sessionId":"app-lane"}`, 200},
		{"lane cannot start", lane, "/api/v1/orchestrators", start, 403},
		{"lane cannot end", lane, "/api/v1/orchestrators/end", end, 403},
		{"worker cannot start", worker, "/api/v1/orchestrators", start, 403},
		{"no brief", main, "/api/v1/orchestrators", `{"projectId":"app","lane":"B1"}`, 403},
		{"blank brief", main, "/api/v1/orchestrators", `{"projectId":"app","lane":"B1","prompt":"   "}`, 403},
		{"clean refused", main, "/api/v1/orchestrators", `{"projectId":"app","lane":"B1","prompt":"x","clean":true}`, 403},
		{"other project", main, "/api/v1/orchestrators", `{"projectId":"web","lane":"B1","prompt":"x"}`, 403},
		{"end other project", main, "/api/v1/orchestrators/end", `{"projectId":"web","lane":"B1"}`, 403},
	} {
		if got := laneRequest(t, h, tc.token, tc.path, tc.body); got != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, got, tc.want)
		}
	}
	on = false
	if got := laneRequest(t, h, main, "/api/v1/orchestrators", start); got != 403 {
		t.Errorf("policy off: start = %d, want 403", got)
	}
	if got := laneRequest(t, h, main, "/api/v1/orchestrators/end", end); got != 403 {
		t.Errorf("policy off: end = %d, want 403", got)
	}
}
