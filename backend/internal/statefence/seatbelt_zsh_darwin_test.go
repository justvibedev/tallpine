//go:build darwin

package statefence

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/sessionscratch"
)

// TestSeatbeltZshHeredocUsesSessionTempPrefix: zsh writes here-documents at
// $TMPPREFIX (default /tmp/zsh), which the fence denies; the TMPPREFIX the
// session env carries keeps them in the session temp dir (B521).
func TestSeatbeltZshHeredocUsesSessionTempPrefix(t *testing.T) {
	if !Available("darwin") {
		t.Skip("sandbox-exec not present")
	}
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not present")
	}
	f := newFenceEnv(t)
	profile := mustProfile(t, f.env, protectOn(t, nil), t.TempDir(), HarnessPaths{})
	heredoc := func(prefix string) (string, error) {
		argv := Wrap([]string{zsh, "-fc", "cat <<X\nok\nX"}, profile)
		c := exec.Command(argv[0], argv[1:]...)
		c.Env = append(os.Environ(), "TMPDIR="+f.tmp, "TMPPREFIX="+prefix)
		out, err := c.CombinedOutput()
		return string(out), err
	}
	if out, err := heredoc(sessionscratch.TempPrefix(f.tmp)); err != nil || strings.TrimSpace(out) != "ok" {
		t.Fatalf("zsh heredoc with the session TMPPREFIX: %v\n%s", err, out)
	}
	if out, err := heredoc("/tmp/zsh"); err == nil {
		t.Fatalf("zsh heredoc in /tmp must stay denied by the fence, got %q", out)
	}
}
