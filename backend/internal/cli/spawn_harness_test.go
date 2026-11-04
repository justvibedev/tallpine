package cli

import "testing"

// TestResolveSpawnHarness_InheritsDaemonDefault: a freshly added project has no
// pinned agents; spawn must use the daemon default carried on the project (B542).
func TestResolveSpawnHarness_InheritsDaemonDefault(t *testing.T) {
	fresh := projectDetails{ID: "new", Agent: "claude-code"}
	for _, kind := range []string{"worker", "orchestrator", ""} {
		if got, err := resolveSpawnHarness("", kind, fresh); err != nil || got != "claude-code" {
			t.Fatalf("kind %q: got %q err %v, want claude-code", kind, got, err)
		}
	}
	pinned := projectDetails{ID: "p", Agent: "claude-code", Config: &projectConfig{Worker: roleOverride{Agent: "codex"}}}
	if got, _ := resolveSpawnHarness("", "worker", pinned); got != "codex" {
		t.Fatalf("pinned worker must win, got %q", got)
	}
}
