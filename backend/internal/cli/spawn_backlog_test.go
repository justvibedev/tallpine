package cli

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSpawnBacklogSendsRefForceAndPrintsWarnings(t *testing.T) {
	cfg := setConfigEnv(t)
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo","defaultBranch":"main"}}`)
		case r.URL.Path == "/api/v1/agents/readiness/ensure":
			_, _ = io.WriteString(w, authorizedAgentsJSON("codex"))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			_ = json.NewDecoder(r.Body).Decode(&body)
			_, _ = io.WriteString(w, `{"session":{"id":"demo-9","status":"idle"},"warnings":["B12 is blocked by B7"]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"spawn", "--project", "demo", "--agent", "codex", "--name", "leak", "--backlog", " B12 ", "--force", "--allow-duplicate", "--plan", "in-place")
	if err != nil {
		t.Fatalf("spawn --backlog: %v stderr=%s", err, errOut)
	}
	if body["backlogRef"] != "B12" || body["backlogForce"] != true || body["backlogAllowDuplicate"] != true || body["backlogPlan"] != "in-place" {
		t.Fatalf("request body = %v", body)
	}
	if !strings.Contains(out, "spawned session demo-9") || !strings.Contains(out, "warning: B12 is blocked by B7\n") {
		t.Fatalf("output = %q", out)
	}
	// Warnings come first so `tail -1` shows the result line.
	if strings.Index(out, "warning:") > strings.Index(out, "spawned session") {
		t.Fatalf("warning printed after the result line: %q", out)
	}
	if _, has := body["issueId"]; has {
		t.Fatalf("--backlog must not set an issue: %v", body)
	}
}

func TestSpawnBacklogUsageErrors(t *testing.T) {
	setConfigEnv(t)
	for name, args := range map[string][]string{
		"allow-duplicate alone": {"spawn", "--project", "demo", "--agent", "codex", "--name", "x", "--allow-duplicate"},
		"plan alone":            {"spawn", "--project", "demo", "--agent", "codex", "--name", "x", "--plan", "in-place"},
		"plan junk":             {"spawn", "--project", "demo", "--agent", "codex", "--name", "x", "--backlog", "B1", "--plan", "maybe"},
		"force alone":           {"spawn", "--project", "demo", "--agent", "codex", "--name", "x", "--force"},
		"orchestrator":          {"spawn", "--project", "demo", "--kind", "orchestrator", "--agent", "codex", "--name", "x", "--backlog", "B1"},
		"standalone":            {"spawn", "--standalone", "--agent", "codex", "--name", "x", "--backlog", "B1"},
	} {
		_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, args...)
		var usage usageError
		if !errors.As(err, &usage) {
			t.Errorf("%s: err = %v, want a usage error", name, err)
		}
	}
}
