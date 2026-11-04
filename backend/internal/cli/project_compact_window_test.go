package cli

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestProjectSetConfig_OrchestratorCompactWindow(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectServer(t, http.StatusOK, `{"status":"ok","project":{"id":"demo","config":{"orchestrator":{"agent":"codex"}}}}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "set-config", "demo", "--orchestrator-compact-window", "off")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	// Alone, the flag goes through the narrow rules route, which an
	// orchestrator token may call on its own project.
	if capture.method != http.MethodPut || capture.path != "/api/v1/projects/demo/rules" {
		t.Fatalf("request = %s %s, want PUT /api/v1/projects/demo/rules", capture.method, capture.path)
	}
	var got map[string]string
	if err := json.Unmarshal(capture.body, &got); err != nil {
		t.Fatalf("decode request body: %v\nbody=%s", err, capture.body)
	}
	if len(got) != 1 || got["orchestratorAutoCompactWindow"] != "off" {
		t.Fatalf("rules body = %#v, want only orchestratorAutoCompactWindow=off", got)
	}
}

// With another config flag the window merges into orchestrator beside agent.
func TestProjectSetConfig_OrchestratorCompactWindowMerges(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectServer(t, http.StatusOK, `{"status":"ok","project":{"id":"demo","config":{"orchestrator":{"agent":"codex"}}}}`)
	writeRunFileFor(t, cfg, srv)
	if _, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"project", "set-config", "demo", "--orchestrator-compact-window", "1m", "--session-prefix", "x"); err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var got struct {
		Config struct {
			Orchestrator map[string]any `json:"orchestrator"`
		} `json:"config"`
	}
	if err := json.Unmarshal(capture.body, &got); err != nil {
		t.Fatalf("decode request body: %v\nbody=%s", err, capture.body)
	}
	if got.Config.Orchestrator["autoCompactWindow"] != "1m" || got.Config.Orchestrator["agent"] != "codex" {
		t.Fatalf("orchestrator = %#v, want autoCompactWindow 1m merged beside agent", got.Config.Orchestrator)
	}
}

func TestProjectSetConfig_OrchestratorIdleCompactUsesRulesRoute(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectServer(t, http.StatusOK, `{"status":"ok","project":{"id":"demo","config":{}}}`)
	writeRunFileFor(t, cfg, srv)
	if _, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"project", "set-config", "demo", "--orchestrator-idle-compact", "off"); err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var got map[string]string
	if err := json.Unmarshal(capture.body, &got); err != nil {
		t.Fatalf("decode request body: %v\nbody=%s", err, capture.body)
	}
	if capture.path != "/api/v1/projects/demo/rules" || len(got) != 1 || got["orchestratorIdleCompactAt"] != "off" {
		t.Fatalf("request %s body %#v, want rules route with orchestratorIdleCompactAt=off", capture.path, got)
	}
}
