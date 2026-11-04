package tmux

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/envfilter"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestBuildLaunchCommand_ScrubbedFormIsEnvMinusIAndNeverExportsTheControlKeys(t *testing.T) {
	cfg := ports.RuntimeConfig{WorkspacePath: "/ws", Argv: []string{"agent", "--x"}, Env: map[string]string{
		agentlaunch.EnvScrub: "1", agentlaunch.EnvUnset: "GH_TOKEN", "A": "it's", "PATH": "/bin",
	}}
	got := buildLaunchCommand(cfg)
	want := `cd '/ws' || exit; env -i TERM="$TERM" COLORTERM='truecolor' A='it'\''s' PATH='/bin' 'agent' '--x'; ` +
		`exec env -i TERM="$TERM" COLORTERM='truecolor' A='it'\''s' PATH='/bin' "${SHELL:-/bin/sh}" -i`
	if got != want {
		t.Fatalf("scrubbed launch command:\n got: %s\nwant: %s", got, want)
	}
	if strings.Contains(got, agentlaunch.EnvScrub) || strings.Contains(got, agentlaunch.EnvUnset) {
		t.Fatalf("control keys must not be passed on: %s", got)
	}
}

func TestBuildLaunchCommand_DefaultIsUnchanged(t *testing.T) {
	cfg := ports.RuntimeConfig{WorkspacePath: "/ws", Argv: []string{"agent", "--x"}, Env: map[string]string{"A": "1"}}
	got := buildLaunchCommand(cfg)
	want := `cd '/ws' || exit; unset NO_COLOR; export A='1'; export COLORTERM='truecolor'; ` +
		`export PATH='` + getenv("PATH") + `'; 'agent' '--x'; exec "${SHELL:-/bin/sh}" -i`
	if getenv("PATH") == "" {
		want = strings.Replace(want, `export PATH=''; `, "", 1)
	}
	if got != want {
		t.Fatalf("default launch command changed:\n got: %s\nwant: %s", got, want)
	}
}

// runScrubbed executes the real launch script under sh with a poisoned
// environment, standing in for a tmux pane whose server holds daemon secrets.
func runScrubbed(t *testing.T, cfg ports.RuntimeConfig, poisoned []string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("sh-only")
	}
	cfg.WorkspacePath = t.TempDir()
	cmd := exec.Command("/bin/sh", "-c", buildLaunchCommand(cfg))
	cmd.Env = append([]string{"TERM=tmux-256color", "SHELL=/bin/sh"}, poisoned...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("launch failed: %v\n%s", err, out)
	}
	return string(out)
}

func TestScrubbedLaunch_AgentSeesOnlyTheSessionsEnvironment(t *testing.T) {
	envBin, err := exec.LookPath("env")
	if err != nil {
		t.Skip("no env binary")
	}
	cfg := ports.RuntimeConfig{
		Argv: []string{envBin}, ExitOnCommandCompletion: true,
		Env: map[string]string{agentlaunch.EnvScrub: "1", "PATH": os.Getenv("PATH"), "HOME": "/home/agent", "TALLPINE_SESSION_ID": "s-1", "SPACED": "a b'c"},
	}
	// The pane (tmux server) environment carries daemon secrets, even when the
	// server was started before the policy was turned on.
	out := runScrubbed(t, cfg, []string{"AWS_SECRET_ACCESS_KEY=leak", "GH_TOKEN=leak", "DATABASE_URL=leak", "HOME=/home/daemon"})
	for _, leak := range []string{"leak", "DATABASE_URL", "/home/daemon", "AWS_SECRET", agentlaunch.EnvScrub} {
		if strings.Contains(out, leak) {
			t.Fatalf("scrubbed agent still sees %q:\n%s", leak, out)
		}
	}
	for _, want := range []string{"HOME=/home/agent", "TALLPINE_SESSION_ID=s-1", "SPACED=a b'c", "TERM=tmux-256color", "COLORTERM=truecolor", "PATH="} {
		if !strings.Contains(out, want) {
			t.Fatalf("scrubbed agent lacks %q:\n%s", want, out)
		}
	}
}

// The allowlist must not break the tools an agent shell lives on. This applies
// the real allowlist to this process's environment, runs the real scrubbed
// launch script and calls each tool that is installed and works on the machine.
func TestScrubbedLaunch_CommonToolsStillRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh-only")
	}
	out := envfilter.Of(policytest.Decision(t, map[string]map[string]any{envfilter.Name: {envfilter.FieldMode: "allowlist"}}))
	env := map[string]string{agentlaunch.EnvScrub: "1"}
	for _, entry := range out.Filter(os.Environ(), false, nil).Keep {
		if key, value, ok := strings.Cut(entry, "="); ok {
			env[key] = value
		}
	}
	// The variables a shell session depends on survive when the daemon has them.
	for _, name := range []string{"PATH", "HOME", "USER", "SHELL", "LANG", "SSH_AUTH_SOCK"} {
		if os.Getenv(name) == "" {
			continue
		}
		runScrubbed(t, ports.RuntimeConfig{Argv: []string{"/bin/sh", "-c", `test -n "$` + name + `"`}, ExitOnCommandCompletion: true, Env: env}, nil)
	}
	ran := 0
	for _, tool := range [][]string{{"git", "--version"}, {"ssh", "-V"}, {"node", "--version"}, {"go", "version"}, {"gh", "--version"}} {
		// Only tools that work here at all: a broken install (a missing dylib, say)
		// would fail for reasons that have nothing to do with the environment.
		if exec.Command(tool[0], tool[1]).Run() != nil {
			continue
		}
		script := tool[0] + " " + tool[1] + " >/dev/null 2>&1 || exit 7"
		runScrubbed(t, ports.RuntimeConfig{Argv: []string{"/bin/sh", "-c", script}, ExitOnCommandCompletion: true, Env: env}, []string{"DATABASE_URL=leak"})
		ran++
	}
	if ran == 0 {
		t.Skip("none of git, ssh, node, go, gh is installed")
	}
}
