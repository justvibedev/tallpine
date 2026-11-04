package agentlaunch

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/brand"
)

func TestProjectEnvMergeAndRedaction(t *testing.T) {
	project := map[string]string{"PROJECT_TOKEN": "long-secret", "SHARED": "project", "SHORT": "long"}
	merged := MergeEnv(project, map[string]string{"SHARED": "child"})
	if merged["PROJECT_TOKEN"] != "long-secret" || merged["SHARED"] != "child" {
		t.Fatalf("merged project and child environment = %#v", merged)
	}
	merged["PROJECT_TOKEN"] = "changed"
	if project["PROJECT_TOKEN"] != "long-secret" {
		t.Fatal("launch mutated saved project environment")
	}
	if got := RedactValues("long-secret", project); got != "[REDACTED]" {
		t.Fatalf("redacted overlapping values = %q", got)
	}
	cause := errors.New("failed with long-secret")
	redacted := RedactError(cause, project)
	if redacted.Error() != "failed with [REDACTED]" || errors.Is(redacted, cause) {
		t.Fatalf("redacted error = %v, want hidden value without the original cause", redacted)
	}
}

func TestMergeEnvReservesTallpineNamespace(t *testing.T) {
	merged := MergeEnv(
		map[string]string{"TALLPINE_SESSION_ID": "spoof", "tallpine_worktree_path": "spoof", "PROJECT_TOKEN": "safe"},
		map[string]string{"TALLPINE_SESSION_ID": "session-1"},
	)
	if merged["TALLPINE_SESSION_ID"] != "session-1" || merged["tallpine_worktree_path"] != "" {
		t.Fatalf("project Tallpine markers survived merge: %#v", merged)
	}
	if merged["PROJECT_TOKEN"] != "safe" {
		t.Fatalf("project variable = %q, want safe", merged["PROJECT_TOKEN"])
	}
}

func TestSharedInstallKeepsAgentNodeAndCanonicalTallpine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shebang selection; Windows has a separate command test")
	}
	stateDir := t.TempDir()
	t.Setenv("TALLPINE_DATA_DIR", "data")
	shared, agent := filepath.Join(t.TempDir(), "shared install's bin"), t.TempDir()
	if err := os.MkdirAll(shared, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	tallpine := filepath.Join(shared, "tallpine")
	write(tallpine, "#!/bin/sh\necho CANONICAL\n")
	write(filepath.Join(shared, "node"), "#!/bin/sh\necho OLD_NODE\n")
	write(filepath.Join(agent, "node"), "#!/bin/sh\necho AGENT_NODE\n")
	write(filepath.Join(agent, "tallpine"), "#!/bin/sh\necho FOREIGN\n")
	launcher := filepath.Join(agent, "agent")
	write(launcher, "#!/usr/bin/env node\n")
	executable := func() (string, error) { return tallpine, nil }
	base := agent + string(os.PathListSeparator) + shared + string(os.PathListSeparator) + "/usr/bin:/bin"
	worktree := t.TempDir()
	t.Chdir(worktree)
	path, err := PinnedPATH(executable, os.Getenv, map[string]string{"PATH": base}, stateDir)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"PATH": path}
	AugmentRuntimePATHForLaunchBinary(context.Background(), env, []string{launcher}, func(string) (string, error) { return filepath.Join(agent, "node"), nil }, PinnedDir(executable, stateDir))
	cmd := exec.CommandContext(context.Background(), "/bin/sh", "-c", "tallpine; exec \"$1\"", "sh", launcher)
	cmd.Env = []string{"PATH=" + env["PATH"]}
	output, err := cmd.CombinedOutput()
	if err != nil || string(output) != "CANONICAL\nAGENT_NODE\n" {
		t.Fatalf("executable selection: %v: %s", err, output)
	}
	if got := strings.Split(env["PATH"], string(os.PathListSeparator))[0]; got == shared {
		t.Fatal("shared install directory still takes priority")
	} else if !filepath.IsAbs(got) || !strings.HasPrefix(got, stateDir+string(os.PathSeparator)) {
		t.Fatalf("shim directory = %q, want absolute path below resolved data directory %q", got, stateDir)
	}
}

func TestSharedInstallRequiresResolvedAbsoluteDataDir(t *testing.T) {
	shared := t.TempDir()
	names := []string{"tallpine", "node"}
	if runtime.GOOS == "windows" {
		names = []string{"tallpine.exe", "node.exe"}
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(shared, name), []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	executable := func() (string, error) { return filepath.Join(shared, names[0]), nil }
	if _, err := PinnedPATH(executable, os.Getenv, nil, "data"); err == nil || !strings.Contains(err.Error(), "must be absolute") {
		t.Fatalf("PinnedPATH error = %v, want absolute data-directory error", err)
	}
}

func TestConfiguredPATHWindowsUsesExactProtectedSpelling(t *testing.T) {
	configured := map[string]string{"Path": "project", "PATH": "tallpine-pinned"}
	for range 1000 {
		if got := configuredPATH(configured, true); got != "tallpine-pinned" {
			t.Fatalf("configured PATH = %q, want exact protected value", got)
		}
	}
	if got := configuredPATH(map[string]string{"Path": "project"}, true); got != "project" {
		t.Fatalf("case-insensitive configured PATH = %q, want project", got)
	}
}

func TestPinnedPATHResolvesAllCLINames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shims")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "bin", "tallpine")
	if err := os.MkdirAll(filepath.Dir(exe), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("#!/bin/sh\necho CANONICAL\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	path, err := PinnedPATH(func() (string, error) { return exe, nil }, os.Getenv, map[string]string{"PATH": "/usr/bin:/bin"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(strings.Split(path, string(os.PathListSeparator))[0], brand.LegacyCLIName)); err == nil {
		t.Fatal("the retired vo shim must not be created on PATH")
	}
	for _, name := range []string{brand.CLIName, brand.CLIAlias} {
		cmd := exec.CommandContext(context.Background(), "/bin/sh", "-c", name)
		cmd.Env = []string{"PATH=" + path}
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "CANONICAL") {
			t.Fatalf("%s: %v: %s", name, err, out)
		}
	}
}

func TestUnwritableShimDirPinsOnlyDaemonOnlyDirs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes")
	}
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	unwritable := filepath.Join(blocker, "data")

	alone := filepath.Join(t.TempDir(), "tallpine")
	path, err := PinnedPATH(func() (string, error) { return alone, nil }, os.Getenv, map[string]string{"PATH": "/usr/bin"}, unwritable)
	if err != nil || path != filepath.Dir(alone)+string(os.PathListSeparator)+"/usr/bin" {
		t.Fatalf("daemon-only dir: PATH = %q, err = %v, want the daemon dir pinned", path, err)
	}

	shared := t.TempDir()
	for _, name := range []string{"tallpine", "node"} {
		if err := os.WriteFile(filepath.Join(shared, name), []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if path, err := PinnedPATH(func() (string, error) { return filepath.Join(shared, "tallpine"), nil }, os.Getenv, map[string]string{"PATH": "/usr/bin"}, unwritable); err == nil {
		t.Fatalf("shared dir with node: PATH = %q, want an error (unpinned), never the shared dir first", path)
	}
}

func TestLaunchBinary(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		argv   []string
		want   string
		wantOK bool
	}{
		{"empty", nil, "", false},
		{"plain", []string{"/bin/claude", "--x"}, "/bin/claude", true},
		{"env prefix skips assignments", []string{"env", "A=1", "B=2", "/bin/codex", "-c", "k=v"}, "/bin/codex", true},
		{"absolute env", []string{"/usr/bin/env", "A=1", "agent"}, "agent", true},
		{"env with only assignments", []string{"env", "A=1"}, "", false},
	} {
		got, ok := LaunchBinary(tc.argv)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("%s: LaunchBinary = (%q, %v), want (%q, %v)", tc.name, got, ok, tc.want, tc.wantOK)
		}
	}
}
