package gitdefault

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/testenv"
)

// The default runner must not let an inherited GIT_DIR send RecordInitialBranch
// into another repository.
func TestDefaultRunnerIgnoresInheritedGitDir(t *testing.T) {
	repo := testenv.GitRepo(t)
	other := testenv.GitRepo(t)
	t.Setenv("GIT_DIR", other+"/.git")
	t.Setenv("GIT_WORK_TREE", other)
	if err := New("git", nil).RecordInitialBranch(context.Background(), repo); err != nil {
		t.Fatalf("RecordInitialBranch: %v", err)
	}
	read := func(dir string) string {
		cmd := exec.Command("git", "-C", dir, "config", "--local", "--get", ManagedDefaultConfigKey) //nolint:forbidigo // test reads back config
		cmd.Env = ports.GitEnv(os.Environ())
		out, _ := cmd.Output()
		return strings.TrimSpace(string(out))
	}
	if got := read(repo); got != "main" {
		t.Fatalf("repo config = %q, want main", got)
	}
	if got := read(other); got != "" {
		t.Fatalf("other repo was written: %q", got)
	}
}
