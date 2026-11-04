package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

func TestSpawnClaimPRWiring(t *testing.T) {
	cfg := setConfigEnv(t)
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appendPrimaryRequest(&requests, r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo","repo":"https://github.com/justvibedev/tallpine-dev","defaultBranch":"main"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/agents/readiness/ensure":
			_, _ = io.WriteString(w, authorizedAgentsJSON("codex"))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			_, _ = io.WriteString(w, `{"session":{"id":"demo-9","status":"idle"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-9/pr/claim":
			var req apitypes.ClaimPRRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.PR != "https://github.com/justvibedev/tallpine-dev/pull/142" || req.AllowTakeover == nil || *req.AllowTakeover {
				t.Fatalf("claim request = %#v", req)
			}
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-9","prs":[{"url":"https://github.com/justvibedev/tallpine-dev/pull/142","number":142,"state":"open","ci":"passing","review":"review_required","mergeability":"mergeable","reviewComments":false,"updatedAt":"2026-06-04T12:00:00Z"}],"branchChanged":false,"takenOverFrom":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "spawn", "--project", "demo", "--agent", "codex", "--name", "worker", "--claim-pr", "142", "--no-takeover")
	if err != nil {
		t.Fatalf("spawn claim-pr failed: %v stderr=%s", err, errOut)
	}
	if !strings.Contains(out, "claimed https://github.com/justvibedev/tallpine-dev/pull/142") {
		t.Fatalf("output missing claimed label: %s", out)
	}
	want := []string{"GET /api/v1/projects/demo", "POST /api/v1/agents/readiness/ensure", "POST /api/v1/sessions", "POST /api/v1/sessions/demo-9/pr/claim"}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests=%#v want %#v", requests, want)
	}
}

// TestSpawnClaimPR_Draft covers #4171: `tallpine spawn --claim-pr` on a draft PR must
// keep the spawned session instead of rolling it back with PR_NOT_OPEN.
func TestSpawnClaimPR_Draft(t *testing.T) {
	cfg := setConfigEnv(t)
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appendPrimaryRequest(&requests, r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo","repo":"https://github.com/justvibedev/tallpine-dev","defaultBranch":"main"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/agents/readiness/ensure":
			_, _ = io.WriteString(w, authorizedAgentsJSON("codex"))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			_, _ = io.WriteString(w, `{"session":{"id":"demo-9","status":"idle"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-9/pr/claim":
			var req apitypes.ClaimPRRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.PR != "https://github.com/justvibedev/tallpine-dev/pull/4168" {
				t.Fatalf("claim request = %#v", req)
			}
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-9","prs":[{"url":"https://github.com/justvibedev/tallpine-dev/pull/4168","number":4168,"state":"draft","ci":"pending","review":"none","mergeability":"unknown","reviewComments":false,"updatedAt":"2026-08-20T12:00:00Z"}],"branchChanged":false,"takenOverFrom":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "spawn", "--project", "demo", "--agent", "codex", "--name", "worker", "--claim-pr", "4168")
	if err != nil {
		t.Fatalf("spawn claim-pr draft failed: %v stderr=%s", err, errOut)
	}
	if !strings.Contains(out, "claimed https://github.com/justvibedev/tallpine-dev/pull/4168") {
		t.Fatalf("output missing claimed label: %s", out)
	}
	// No rollback: the draft claim succeeded.
	want := []string{"GET /api/v1/projects/demo", "POST /api/v1/agents/readiness/ensure", "POST /api/v1/sessions", "POST /api/v1/sessions/demo-9/pr/claim"}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests=%#v want %#v", requests, want)
	}
}

func TestSpawnClaimPR_GitLab(t *testing.T) {
	cfg := setConfigEnv(t)
	var capturedReq apitypes.ClaimPRRequest
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appendPrimaryRequest(&requests, r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo","repo":"https://gitlab.com/castai/ctxd","defaultBranch":"main"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/agents/readiness/ensure":
			_, _ = io.WriteString(w, authorizedAgentsJSON("codex"))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			_, _ = io.WriteString(w, `{"session":{"id":"demo-9","status":"idle"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-9/pr/claim":
			_ = json.NewDecoder(r.Body).Decode(&capturedReq)
			if capturedReq.PR != "https://gitlab.com/castai/ctxd/-/merge_requests/9" || capturedReq.AllowTakeover == nil || *capturedReq.AllowTakeover {
				t.Fatalf("claim request = %#v", capturedReq)
			}
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-9","prs":[{"url":"https://gitlab.com/castai/ctxd/-/merge_requests/9","number":9,"state":"open","ci":"passing","review":"review_required","mergeability":"mergeable","reviewComments":false,"updatedAt":"2026-06-04T12:00:00Z"}],"branchChanged":false,"takenOverFrom":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "spawn", "--project", "demo", "--agent", "codex", "--name", "worker", "--claim-pr", "https://gitlab.com/castai/ctxd/-/merge_requests/9", "--no-takeover")
	if err != nil {
		t.Fatalf("spawn claim-pr gitlab failed: %v stderr=%s", err, errOut)
	}
	if !strings.Contains(out, "claimed https://gitlab.com/castai/ctxd/-/merge_requests/9") {
		t.Fatalf("output missing claimed label: %s", out)
	}
	want := []string{"GET /api/v1/projects/demo", "POST /api/v1/agents/readiness/ensure", "POST /api/v1/sessions", "POST /api/v1/sessions/demo-9/pr/claim"}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests=%#v want %#v", requests, want)
	}
}

func TestSpawnClaimPRFailureRollsBackSession(t *testing.T) {
	cfg := setConfigEnv(t)
	var requests []string
	sessions := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appendPrimaryRequest(&requests, r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo","repo":"https://github.com/justvibedev/tallpine-dev","defaultBranch":"main"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/agents/readiness/ensure":
			_, _ = io.WriteString(w, authorizedAgentsJSON("codex"))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			sessions["demo-10"] = true
			_, _ = io.WriteString(w, `{"session":{"id":"demo-10","status":"idle"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-10/pr/claim":
			if !sessions["demo-10"] {
				t.Fatal("claim called before session existed")
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"not_found","code":"PR_NOT_FOUND","message":"PR not found"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-10/rollback":
			delete(sessions, "demo-10")
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-10","deleted":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "spawn", "--project", "demo", "--agent", "codex", "--name", "worker", "--claim-pr", "142")
	if err == nil {
		t.Fatal("expected spawn claim failure")
	}
	msg := err.Error()
	if !strings.Contains(msg, "failed to claim PR 142") || !strings.Contains(msg, "rolled back session demo-10") {
		t.Fatalf("error = %v", err)
	}
	if sessions["demo-10"] {
		t.Fatalf("spawned session still present after claim rollback: %#v", sessions)
	}
	want := []string{"GET /api/v1/projects/demo", "POST /api/v1/agents/readiness/ensure", "POST /api/v1/sessions", "POST /api/v1/sessions/demo-10/pr/claim", "POST /api/v1/sessions/demo-10/rollback"}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests=%#v want %#v", requests, want)
	}
}
