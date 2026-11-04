package tmux

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestLaunchCommandWithEnvFileCarriesNoEnvValues(t *testing.T) {
	const secret = "s3cr3t-token-value"
	for name, env := range map[string]map[string]string{
		"plain":    {"GH_TOKEN": secret, "PATH": "/usr/bin"},
		"scrubbed": {"GH_TOKEN": secret, "PATH": "/usr/bin", agentlaunch.EnvScrub: "1"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := ports.RuntimeConfig{SessionID: "s", WorkspacePath: t.TempDir(), Argv: []string{"agent"}, Env: env}
			file, err := writeLaunchEnvFile(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(file)
			cmd := buildLaunchCommandWithEnvFile(cfg, file)
			if strings.Contains(cmd, secret) {
				t.Fatalf("launch command exposes env value: %s", cmd)
			}
			if !strings.Contains(cmd, "rm -f") {
				t.Fatalf("launch command does not delete the env file: %s", cmd)
			}
			if got, _ := os.ReadFile(file); !strings.Contains(string(got), secret) {
				t.Fatalf("env file lacks the value: %q", got)
			}
			if fi, _ := os.Stat(file); fi.Mode().Perm() != 0o600 {
				t.Fatalf("env file mode = %v", fi.Mode().Perm())
			}
			if di, _ := os.Stat(file[:strings.LastIndex(file, "/")]); di.Mode().Perm() != 0o700 {
				t.Fatalf("env dir mode = %v", di.Mode().Perm())
			}
		})
	}
}

func TestCreateAndRestartKeepEnvValuesOutOfArgv(t *testing.T) {
	const secret = "s3cr3t-token-value"
	cfg := ports.RuntimeConfig{
		SessionID: "sess-1", WorkspacePath: "/tmp/ws", Argv: []string{"agent"},
		Env: map[string]string{"TALLPINE_SESSION_TOKEN": secret, "PATH": "/usr/bin"},
	}
	r, fr := newTestRuntime(0)
	fr.outputs = [][]byte{nil, []byte("/tmp/ws\n"), nil, nil, nil, nil, nil}
	if _, err := r.Create(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	r2, fr2 := newTestRuntime(0)
	fr2.outputs = [][]byte{nil, nil, nil, nil}
	if _, err := r2.Restart(context.Background(), ports.RuntimeHandle{ID: "sess-1"}, cfg); err != nil {
		t.Logf("restart: %v", err)
	}
	if len(fr2.calls) == 0 || fr2.calls[0].args[0] != "respawn-pane" {
		t.Fatalf("restart never respawned: %v", fr2.calls)
	}
	for _, calls := range [][]runnerCall{fr.calls, fr2.calls} {
		for _, c := range calls {
			if strings.Contains(strings.Join(c.args, " "), secret) {
				t.Fatalf("tmux argv exposes env value: %v", c.args)
			}
		}
	}
}

func TestEnvFileRemovedWhenWorkspaceMissing(t *testing.T) {
	for name, env := range map[string]map[string]string{"plain": {"A": "1"}, "scrubbed": {"A": "1", agentlaunch.EnvScrub: "1"}} {
		t.Run(name, func(t *testing.T) {
			cfg := ports.RuntimeConfig{WorkspacePath: "/nonexistent/ws", Argv: []string{"true"}, Env: env}
			file, err := writeLaunchEnvFile(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(file)
			out, _ := exec.Command("/bin/sh", "-c", buildLaunchCommandWithEnvFile(cfg, file)).CombinedOutput()
			if _, err := os.Stat(file); err == nil {
				t.Fatalf("env file left behind: %s", out)
			}
		})
	}
}
