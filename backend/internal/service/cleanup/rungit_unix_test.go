//go:build !windows

package cleanup

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/processalive"
)

// A timed-out git call stops what git started too: an ssh stuck on a prompt
// must not be orphaned on every pass.
func TestRunGitStopsGitsChildrenOnTimeout(t *testing.T) {
	g := newGitFixture(t)
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "ssh.pid")
	script := filepath.Join(dir, "ssh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho $$ > "+pidFile+"\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_SSH_COMMAND", script)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	if _, err := RunGit(ctx, g.repo, "ls-remote", "ssh://example.invalid/repo.git"); err == nil {
		t.Fatal("ls-remote through a stuck ssh succeeded")
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("RunGit took %s after its context ended", took)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the ssh command never ran: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for processalive.Alive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("git's ssh child %d survived the timeout", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
