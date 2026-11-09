package workspacewatch

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// changeDeadline bounds waiting for a change that must arrive; it only costs
// time when the change never comes.
const changeDeadline = 30 * time.Second

func waitForChange(t *testing.T, changes <-chan struct{}) {
	t.Helper()
	select {
	case _, ok := <-changes:
		if !ok {
			t.Fatal("watch channel closed before change")
		}
	case <-time.After(changeDeadline):
		t.Fatal("timed out waiting for workspace change")
	}
}

func drainChanges(changes <-chan struct{}) {
	for {
		select {
		case _, ok := <-changes:
			if !ok {
				return
			}
		default:
			return
		}
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
