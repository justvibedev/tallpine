package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A running Claude holds <config>.lock while it re-reads and rewrites the
// config. The trust write must wait for it and then merge into what Claude
// wrote, instead of racing it and losing one of the two updates.
func TestEnsureWorkspaceTrustedWaitsForClaudeConfigLock(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".claude.json")
	if err := os.WriteFile(cfgPath, []byte(`{"projects":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	lockPath := cfgPath + ".lock"
	if err := os.Mkdir(lockPath, 0o755); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- ensureWorkspaceTrusted(cfgPath, "/w/vo-worktree") }()

	select {
	case err := <-done:
		t.Fatalf("trust write finished while Claude held the config lock (err=%v)", err)
	case <-time.After(150 * time.Millisecond):
	}

	// The lock holder (Claude) writes its own update, then releases.
	if err := os.WriteFile(cfgPath, []byte(`{"projects":{"/other/claude-project":{"lastCost":2}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ensureWorkspaceTrusted: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("trust write did not proceed after the lock was released")
	}

	projects := readJSON(t, cfgPath)["projects"].(map[string]any)
	if entry, _ := projects["/w/vo-worktree"].(map[string]any); entry["hasTrustDialogAccepted"] != true {
		t.Fatalf("trust entry missing after locked write: %#v", projects)
	}
	if _, ok := projects["/other/claude-project"]; !ok {
		t.Fatalf("the lock holder's update was overwritten: %#v", projects)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("config lock not released after the write: %v", err)
	}
}

func TestEnsureWorkspaceTrustedReclaimsStaleClaudeConfigLock(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".claude.json")
	lockPath := cfgPath + ".lock"
	if err := os.Mkdir(lockPath, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatal(err)
	}

	if err := ensureWorkspaceTrusted(cfgPath, "/w"); err != nil {
		t.Fatalf("ensureWorkspaceTrusted with a stale lock: %v", err)
	}
	projects := readJSON(t, cfgPath)["projects"].(map[string]any)
	if entry, _ := projects["/w"].(map[string]any); entry["hasTrustDialogAccepted"] != true {
		t.Fatalf("trust entry missing: %#v", projects)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("reclaimed lock not released: %v", err)
	}
}

func TestEnsureWorkspaceTrustedGivesUpOnALiveClaudeConfigLock(t *testing.T) {
	timeout := claudeConfigLockTimeout
	claudeConfigLockTimeout = 100 * time.Millisecond
	t.Cleanup(func() { claudeConfigLockTimeout = timeout })

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".claude.json")
	seed := `{"projects":{}}`
	if err := os.WriteFile(cfgPath, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(cfgPath+".lock", 0o755); err != nil {
		t.Fatal(err)
	}

	err := ensureWorkspaceTrusted(cfgPath, "/w")
	if err == nil || !strings.Contains(err.Error(), ".claude.json.lock") {
		t.Fatalf("ensureWorkspaceTrusted error = %v, want a held-lock error naming the lock", err)
	}
	if data, _ := os.ReadFile(cfgPath); string(data) != seed {
		t.Fatalf("config written without the lock: %s", data)
	}
	if _, statErr := os.Stat(cfgPath + ".lock"); statErr != nil {
		t.Fatalf("someone else's live lock was removed: %v", statErr)
	}
}

func TestEnsureWorkspaceTrustedSkipsTheLockWhenAlreadyTrusted(t *testing.T) {
	timeout := claudeConfigLockTimeout
	claudeConfigLockTimeout = 50 * time.Millisecond
	t.Cleanup(func() { claudeConfigLockTimeout = timeout })

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".claude.json")
	if err := os.WriteFile(cfgPath, []byte(`{"autoModeEnvSetup":{"dismissed":true},"projects":{"/w":{"hasTrustDialogAccepted":true}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(cfgPath+".lock", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensureWorkspaceTrusted(cfgPath, "/w"); err != nil {
		t.Fatalf("already-trusted workspace waited on the lock: %v", err)
	}
}
