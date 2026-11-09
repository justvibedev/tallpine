//go:build e2e

package cli_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeACPRig is the scratch world the daemon-restart e2e tests share: a git
// repo, a scripted ACP agent standing in for Claude, and the daemon's environ.
type fakeACPRig struct {
	t       *testing.T
	e       env
	ctl     string
	fakeACP string
	repo    string
	environ []string
}

func newFakeACPRig(t *testing.T, e env) *fakeACPRig {
	t.Helper()
	root := t.TempDir()
	ctl := filepath.Join(root, "ctl")
	bin := filepath.Join(root, "bin")
	home := filepath.Join(root, "home")
	for _, d := range []string{ctl, bin, home} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fakeACP := filepath.Join(bin, "fakeacp")
	if out, err := exec.Command("go", "build", "-o", fakeACP, "./testdata/fakeacp").CombinedOutput(); err != nil {
		t.Fatalf("build fakeacp: %v\n%s", err, out)
	}
	// Registered before any daemon, so it runs after their cleanups have killed
	// the hosts: an agent still running then has nothing left to stop it (B605).
	t.Cleanup(func() {
		running := func() string {
			out, _ := exec.Command("pgrep", "-f", fakeACP).Output()
			return strings.TrimSpace(string(out))
		}
		for deadline := time.Now().Add(10 * time.Second); running() != "" && time.Now().Before(deadline); {
			time.Sleep(100 * time.Millisecond)
		}
		if pids := running(); pids != "" {
			t.Errorf("fakeacp still running after the test: %s", pids)
		}
		killLeftovers(fakeACP)
	})
	// A stand-in `claude` so binary resolution succeeds; its probes just fail,
	// which the chat launch treats as inconclusive.
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	environ := append(e.environ(""),
		"HOME="+home,
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"TALLPINE_CLAUDE_ACP_COMMAND="+fakeACP,
		"TALLPINE_FAKEACP_DIR="+ctl,
		"TALLPINE_TURN_FALLBACK_DELAY=2s", "TALLPINE_QUIET_REPORT_DELAY=2s", "TALLPINE_ACP_HANDSHAKE_TIMEOUT=3s", // honoured only by the -tags e2e binary
	)
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(root, "origin.git")
	if out, err := exec.Command("git", "init", "--bare", "-b", "main", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	for _, args := range [][]string{
		{"init", "-b", "main"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "t"},
		{"commit", "--allow-empty", "-m", "init"},
		{"remote", "add", "origin", bare}, {"push", "origin", "main"}, {"remote", "set-head", "origin", "main"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return &fakeACPRig{t: t, e: e, ctl: ctl, fakeACP: fakeACP, repo: repo, environ: environ}
}

// tallpine runs the CLI in the rig's environ and fails the test on a non-zero exit.
func (r *fakeACPRig) tallpine(extra []string, args ...string) string {
	r.t.Helper()
	out, code := r.e.runEnv(r.t, append(append([]string{}, r.environ...), extra...), args...)
	if code != 0 {
		r.t.Fatalf("tallpine %s: exit %d\n%s", strings.Join(args, " "), code, out)
	}
	return out
}
