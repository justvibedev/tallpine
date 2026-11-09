package sessionmanager

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func gitGuardManager(t *testing.T, exe func() (string, error)) *Manager {
	t.Helper()
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/owner/checkout", Config: testRoleAgents()}
	return New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil }, DataDir: t.TempDir(), RunFilePath: "/state/running.json", Executable: exe})
}

// B600: runtimeEnv is where every launch path gets its env; the shim must be
// first on PATH there, naming the project as what it protects, and project env
// can neither switch it off nor point it elsewhere.
func TestRuntimeEnvPutsTheGitShimFirstOnPath(t *testing.T) {
	m := gitGuardManager(t, func() (string, error) { return "/opt/tallpine bin/tallpine", nil })
	env := m.runtimeEnv("mer-1", "mer", "", map[string]string{
		agentlaunch.EnvAllowSharedGitConfig: "1",
		agentlaunch.EnvGitGuardRoot:         "/elsewhere",
	})
	if env[agentlaunch.EnvGitGuardRoot] != "/owner/checkout" {
		t.Fatalf("guard root = %q", env[agentlaunch.EnvGitGuardRoot])
	}
	if got, ok := env[agentlaunch.EnvAllowSharedGitConfig]; ok {
		t.Fatalf("the override must not come from project env: %q", got)
	}
	first, _, _ := strings.Cut(env["PATH"], string(os.PathListSeparator))
	script, err := os.ReadFile(filepath.Join(first, "git"))
	if err != nil || !strings.Contains(string(script), "git-exec") {
		t.Fatalf("PATH head %q is not the git shim: %q %v (PATH %s)", first, script, err, env["PATH"])
	}
}

// A fresh spawn adds the agent binary's directory to PATH after the guard ran.
// When that directory also holds git, a bare `git` must still be the shim.
func TestSpawnKeepsTheGitShimAheadOfALaunchBinaryDirHoldingGit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no sh shim on Windows")
	}
	agentDir := t.TempDir()
	claude := filepath.Join(agentDir, "claude")
	for _, path := range []string{claude, filepath.Join(agentDir, "git")} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o700); err != nil { //nolint:gosec // test fake must be executable
			t.Fatal(err)
		}
	}
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/owner/checkout", Config: testRoleAgents()}
	rt := &fakeRuntime{}
	m := New(Deps{Runtime: rt, Agents: singleAgent{agent: launchArgvAgent{argv: []string{claude}}}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return claude, nil }, DataDir: t.TempDir(), RunFilePath: "/state/running.json",
		Executable: func() (string, error) { return "/opt/tallpine bin/tallpine", nil }})
	if _, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, RequestedMode: domain.SessionModeTUI,
	}); err != nil {
		t.Fatal(err)
	}
	path := rt.lastCfg.Env["PATH"]
	if !strings.Contains(path, agentDir) {
		t.Fatalf("the launch binary directory was not added, so this tests nothing: %s", path)
	}
	for _, dir := range filepath.SplitList(path) {
		if _, err := os.Stat(filepath.Join(dir, "git")); err != nil {
			continue
		}
		if filepath.Base(filepath.Dir(dir)) != "git-shim" {
			t.Fatalf("bare git resolves to %s, not the shim (PATH %s)", dir, path)
		}
		return
	}
	t.Fatalf("no git on PATH %s", path)
}
