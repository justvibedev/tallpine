package tmux

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestLaunchCommandUnsetsWithheldVariablesBeforeTheSessionsExports(t *testing.T) {
	got := buildLaunchCommand(ports.RuntimeConfig{WorkspacePath: "/ws", Argv: []string{"a"}, Env: map[string]string{
		agentlaunch.EnvUnset: "GH_TOKEN,GITHUB_TOKEN", "GH_TOKEN": "project", "A": "1",
	}})
	unset, export := strings.Index(got, "unset GH_TOKEN GITHUB_TOKEN;"), strings.Index(got, "export GH_TOKEN='project'")
	if unset < 0 || export < 0 || unset > export {
		t.Fatalf("unset must precede the session's own export: %s", got)
	}
	if strings.Contains(got, agentlaunch.EnvUnset) {
		t.Fatalf("the instruction key must not be exported: %s", got)
	}
	if plain := buildLaunchCommand(ports.RuntimeConfig{WorkspacePath: "/ws", Argv: []string{"a"}, Env: map[string]string{"A": "1"}}); strings.Contains(plain, "unset GH_TOKEN") {
		t.Fatalf("no instruction, no unset: %s", plain)
	}
}

// Run the real script with the daemon's tokens in the pane's environment, as a
// tmux server that inherited them from the daemon would hold.
func TestLaunchCommandRealShellDropsInheritedTokensButKeepsTheProjectsOwn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh-only")
	}
	envBin, err := exec.LookPath("env")
	if err != nil {
		t.Skip("no env binary")
	}
	run := func(env map[string]string) string {
		cfg := ports.RuntimeConfig{WorkspacePath: t.TempDir(), Argv: []string{envBin}, ExitOnCommandCompletion: true, Env: env}
		cmd := exec.Command("/bin/sh", "-c", buildLaunchCommand(cfg))
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GH_TOKEN=daemon", "GITHUB_TOKEN=daemon", "KEEP=yes"}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return string(out)
	}
	out := run(map[string]string{agentlaunch.EnvUnset: "GH_TOKEN,GITHUB_TOKEN"})
	if strings.Contains(out, "daemon") || !strings.Contains(out, "KEEP=yes") || strings.Contains(out, "TALLPINE_UNSET_ENV") {
		t.Fatalf("daemon tokens must be gone, the rest kept:\n%s", out)
	}
	if out := run(map[string]string{agentlaunch.EnvUnset: "GH_TOKEN,GITHUB_TOKEN", "GH_TOKEN": "project"}); !strings.Contains(out, "GH_TOKEN=project") || strings.Contains(out, "daemon") {
		t.Fatalf("the project's own token must win over the daemon's:\n%s", out)
	}
}
