package sessionmanager

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// fakeDaemonExe returns a daemon binary path under dir whose directory holds
// the tp alias and tallpine transition name as non-executable siblings, so PATH
// pinning uses the directory itself rather than a generated shim directory.
func fakeDaemonExe(t *testing.T, dir ...string) string {
	t.Helper()
	d := filepath.Join(append([]string{t.TempDir()}, dir...)...)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{brand.CLIAlias} {
		if err := os.WriteFile(filepath.Join(d, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(d, brand.CLIName)
}

// The packaged app keeps no tp/vo next to the daemon binary, so a spawned
// session pins a shim directory resolving every CLI name to the daemon.
func TestSpawn_PinsShimDirWhenDaemonDirHasNoSiblings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shims")
	}
	t.Setenv("PATH", "/usr/bin:/bin")
	daemonExe := filepath.Join(t.TempDir(), "tallpine")
	if err := os.WriteFile(daemonExe, []byte("#!/bin/sh\necho DAEMON\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rt := &fakeRuntime{}
	m := New(Deps{
		Runtime: rt, Agents: fakeAgents{}, Workspace: &fakeWorkspace{}, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, DataDir: dataDir,
		LookPath:   func(string) (string, error) { return "/bin/true", nil },
		Executable: func() (string, error) { return daemonExe, nil },
	})
	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err != nil {
		t.Fatal(err)
	}
	path := rt.lastCfg.Env["PATH"]
	parts := strings.Split(path, string(os.PathListSeparator))
	if len(parts) < 3 || parts[1] != "/usr/bin" || parts[2] != "/bin" || !strings.HasPrefix(parts[0], filepath.Join(dataDir, "runtime")) {
		t.Fatalf("runtime env PATH = %q, want the shim dir under the data dir, then the inherited PATH", path)
	}
	for _, name := range []string{brand.CLIName, brand.CLIAlias} {
		cmd := exec.CommandContext(context.Background(), "/bin/sh", "-c", name)
		cmd.Env = []string{"PATH=" + path}
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "DAEMON") {
			t.Fatalf("%s: %v: %s", name, err, out)
		}
	}
}
