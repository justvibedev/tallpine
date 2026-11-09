package codexappserver

import (
	"encoding/json"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
)

// B600: a chat session runs the same shell commands as a terminal one, so a
// guarded launch must deliver the git guard on every thread request.
func TestThreadStartParamsCarryTheGitGuardForGuardedSessions(t *testing.T) {
	params := threadStartParams(threadSettings{
		WorkspacePath: "/work",
		Env:           map[string]string{agentlaunch.EnvGitGuardRoot: "/owner"},
	})
	config, ok := params["config"].(map[string]any)
	if !ok {
		t.Fatalf("no config in params: %#v", params)
	}
	if config["bypass_hook_trust"] != true {
		t.Fatalf("bypass_hook_trust = %#v; the app-server leaves the hook untrusted without it", config["bypass_hook_trust"])
	}
	encoded, err := json.Marshal(config["hooks"])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"PreToolUse"`) || !strings.Contains(string(encoded), "hooks codex git-guard") {
		t.Fatalf("hooks = %s", encoded)
	}
}

// Sessions without a guard root (no project, Windows) must not get a trust bypass.
func TestThreadStartParamsOmitTheGitGuardWhenNotGuarded(t *testing.T) {
	params := threadStartParams(threadSettings{WorkspacePath: "/work", Env: map[string]string{}})
	if _, ok := params["config"]; ok {
		t.Fatalf("unguarded session got config: %#v", params["config"])
	}
}
