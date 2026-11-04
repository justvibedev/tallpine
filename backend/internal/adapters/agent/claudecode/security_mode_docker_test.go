package claudecode

import (
	"context"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
)

// Security mode keeps the Docker socket out of the fence's reachable sockets
// unless the project opted in with securityMode.allowDocker.
func TestSecurityModeDockerSocketOnlyWhenAllowed(t *testing.T) {
	home := t.TempDir()
	env := credEnv(home)
	_, _, closed := sandboxOf(t, ProtectStateSettingsFor(env, policytest.Decision(t, map[string]map[string]any{"securityMode": {"enabled": true}})))
	if hasPath(closed, "/var/run/docker.sock") || hasPath(closed, filepath.Join(home, ".docker/run/docker.sock")) {
		t.Fatalf("docker socket reachable without allowDocker: %v", closed)
	}
	engine, store := policytest.Engine(t, nil)
	store.Data = []byte(`{"version":2,"rules":{"securityMode":{"enabled":true}},"projects":{"p":{"securityMode":{"allowDocker":true}}}}`)
	_, _, open := sandboxOf(t, ProtectStateSettingsFor(env, engine.Decide(context.Background(), agentpolicy.SessionRef{Project: "p"})))
	if !hasPath(open, "/var/run/docker.sock") {
		t.Fatalf("allowDocker did not open the socket: %v", open)
	}
}
