package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/daemontoken"
	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

type notifyCapture struct {
	posts  []string // "<path> <body>"
	tokens []string
}

func notifySession(id, project, kind, created string, terminated bool) string {
	return fmt.Sprintf(`{"id":%q,"projectId":%q,"kind":%q,"harness":"codex","isTerminated":%t,"createdAt":%q,"status":"working"}`,
		id, project, kind, terminated, created)
}

func notifyServer(t *testing.T, sessions ...string) (*httptest.Server, *notifyCapture) {
	t.Helper()
	got := &notifyCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/orchestrators":
			_, _ = io.WriteString(w, `{"sessions":[`+strings.Join(sessions, ",")+`]}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/send"):
			b, _ := io.ReadAll(r.Body)
			got.posts = append(got.posts, r.URL.Path+" "+string(b))
			got.tokens = append(got.tokens, r.Header.Get(daemontoken.Header))
			_, _ = io.WriteString(w, `{"ok":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func runNotify(t *testing.T, srv *httptest.Server, args ...string) (string, error) {
	t.Helper()
	cfg := setConfigEnv(t)
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		append([]string{"orchestrator", "notify", "--project", "demo", "--message", "hi"}, args...)...)
	return out, err
}

func TestOrchestratorNotify_PicksLiveTarget(t *testing.T) {
	srv, got := notifyServer(t,
		notifySession("old", "demo", "orchestrator", "2026-01-01T00:00:00Z", false),
		notifySession("want", "demo", "orchestrator", "2026-03-01T00:00:00Z", false))
	if _, err := runNotify(t, srv, "--target", "want"); err != nil {
		t.Fatal(err)
	}
	if len(got.posts) != 1 || !strings.HasPrefix(got.posts[0], "/api/v1/sessions/want/send ") {
		t.Fatalf("posts = %v, want one send to want", got.posts)
	}
}

func TestOrchestratorNotify_FallsBackToOldestLive(t *testing.T) {
	srv, got := notifyServer(t,
		notifySession("dead-oldest", "demo", "orchestrator", "2025-01-01T00:00:00Z", true),
		notifySession("other-proj", "other", "orchestrator", "2025-02-01T00:00:00Z", false),
		notifySession("worker", "demo", "worker", "2025-03-01T00:00:00Z", false),
		notifySession("newer", "demo", "orchestrator", "2026-03-01T00:00:00Z", false),
		notifySession("oldest-live", "demo", "orchestrator", "2026-01-01T00:00:00Z", false))
	// The target is terminated, so it must not be picked.
	if _, err := runNotify(t, srv, "--target", "dead-oldest"); err != nil {
		t.Fatal(err)
	}
	if len(got.posts) != 1 || !strings.HasPrefix(got.posts[0], "/api/v1/sessions/oldest-live/send ") {
		t.Fatalf("posts = %v, want one send to oldest-live", got.posts)
	}
}

func TestOrchestratorNotify_ScrubsSessionIdentity(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "worker-9")
	t.Setenv("TALLPINE_SESSION_TOKEN", "secret-token")
	t.Setenv("TALLPINE_PROJECT_ID", "someone-else")
	srv, got := notifyServer(t, notifySession("orch", "demo", "orchestrator", "2026-01-01T00:00:00Z", false))
	if _, err := runNotify(t, srv); err != nil {
		t.Fatal(err)
	}
	if len(got.posts) != 1 {
		t.Fatalf("posts = %v", got.posts)
	}
	body := got.posts[0][strings.Index(got.posts[0], " ")+1:]
	var req apitypes.SendSessionMessageRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	if req.Message != "hi" || req.SenderSessionID != "" || !req.UserAuthored {
		t.Fatalf("request = %+v, want bare message from the machine", req)
	}
	if strings.Contains(strings.Join(got.tokens, ""), "secret-token") {
		t.Fatalf("session token leaked into the request: %v", got.tokens)
	}
	for _, name := range []string{"TALLPINE_SESSION_ID", "TALLPINE_SESSION_TOKEN", "TALLPINE_PROJECT_ID"} {
		if v := os.Getenv(name); v != "" {
			t.Errorf("%s still set to %q", name, v)
		}
	}
}

func TestOrchestratorNotify_NoLiveOrchestratorFails(t *testing.T) {
	srv, got := notifyServer(t,
		notifySession("dead", "demo", "orchestrator", "2026-01-01T00:00:00Z", true),
		notifySession("elsewhere", "other", "orchestrator", "2026-01-01T00:00:00Z", false))
	_, err := runNotify(t, srv)
	if err == nil || !strings.Contains(err.Error(), "no live orchestrator") {
		t.Fatalf("err = %v, want no live orchestrator", err)
	}
	if len(got.posts) != 0 {
		t.Fatalf("sent anyway: %v", got.posts)
	}
}
