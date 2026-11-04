package sessionmanager

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// branchRefs is a repo with a session's local branch and its remote-tracking
// copy, which a branch cleanup deletes.
type branchRefs struct {
	t    *testing.T
	repo string
}

var branchCleanupRefs = []string{"refs/heads/vo/mer-1/root", "refs/remotes/origin/vo/mer-1/root"}

func newBranchRefs(t *testing.T) branchRefs {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	b := branchRefs{t: t, repo: filepath.Join(t.TempDir(), "repo")}
	b.git("init", "-q", "-b", "main", b.repo)
	b.git("-C", b.repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	for _, ref := range branchCleanupRefs {
		b.git("-C", b.repo, "update-ref", ref, "HEAD")
	}
	return b
}

func (b branchRefs) git(args ...string) {
	b.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Env = append(cmd.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		b.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// deleteFn is the deletion a branch cleanup runs under the guard.
func (b branchRefs) deleteFn(ran chan<- struct{}) func(context.Context) {
	return func(context.Context) {
		for _, ref := range branchCleanupRefs {
			_ = exec.Command("git", "-C", b.repo, "update-ref", "-d", ref).Run()
		}
		close(ran)
	}
}

func (b branchRefs) requireRefs() {
	b.t.Helper()
	for _, ref := range branchCleanupRefs {
		if err := exec.Command("git", "-C", b.repo, "rev-parse", "--verify", "--quiet", ref).Run(); err != nil {
			b.t.Fatalf("%s deleted while a restore owns the guards", ref)
		}
	}
}

// pauseRestore starts restoring id and returns once the restore is inside the
// workspace adapter, before its worktree is attached; the returned func lets
// it continue and waits for it.
func pauseRestore(t *testing.T, m *Manager, ws *fakeWorkspace, id domain.SessionID) func() {
	t.Helper()
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	ws.restoreHook = func() {
		close(entered)
		<-release
	}
	go func() {
		defer close(done)
		_, _ = m.RestoreWithMode(context.Background(), id)
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("restore did not reach the workspace")
	}
	return func() {
		close(release)
		<-done
	}
}

// A restore of the session itself holds its agent-operation guard: the branch
// cleanup is refused right away, and both refs survive.
func TestGuardBranchCleanupRefusesWhileTheSessionIsRestoring(t *testing.T) {
	m, st, _, ws := newManager()
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "vo/mer-1/root", AgentSessionID: "agent-x"})
	refs := newBranchRefs(t)
	finish := pauseRestore(t, m, ws, "mer-1")
	defer finish()

	ran := make(chan struct{})
	reason, err := m.GuardBranchCleanup(context.Background(), "mer-1", refs.deleteFn(ran))
	if err != nil || reason == "" {
		t.Fatalf("reason=%q err=%v, want a refusal", reason, err)
	}
	select {
	case <-ran:
		t.Fatal("the deletion ran while the session is restoring")
	default:
	}
	refs.requireRefs()
}

// A restore of another session of the project holds the workspace gate: the
// branch cleanup waits instead of deleting, and a cancelled pass gives up
// without deleting.
func TestGuardBranchCleanupWaitsWhileAnotherSessionRestores(t *testing.T) {
	m, st, _, ws := newManager()
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "vo/mer-1/root"})
	seedTerminal(st, "mer-2", domain.SessionMetadata{WorkspacePath: "/ws/mer-2", Branch: "vo/mer-1/root", AgentSessionID: "agent-x"})
	refs := newBranchRefs(t)
	finish := pauseRestore(t, m, ws, "mer-2")
	defer finish()

	ctx, cancel := context.WithCancel(context.Background())
	ran := make(chan struct{})
	type result struct {
		reason string
		err    error
	}
	done := make(chan result, 1)
	go func() {
		reason, err := m.GuardBranchCleanup(ctx, "mer-1", refs.deleteFn(ran))
		done <- result{reason, err}
	}()
	select {
	case <-ran:
		t.Fatal("the deletion ran while another session's restore holds the workspace gate")
	case res := <-done:
		t.Fatalf("the guard returned while the restore holds the gate: %+v", res)
	case <-time.After(300 * time.Millisecond):
	}
	refs.requireRefs()
	cancel()
	select {
	case res := <-done:
		if !errors.Is(res.err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", res.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled branch cleanup kept waiting for the gate")
	}
	select {
	case <-ran:
		t.Fatal("a cancelled branch cleanup deleted")
	default:
	}
	refs.requireRefs()
}

// With nothing restoring, the deletion runs under the guard.
func TestGuardBranchCleanupRunsWhenNothingRestores(t *testing.T) {
	m, st, _, _ := newManager()
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "vo/mer-1/root"})
	refs := newBranchRefs(t)
	ran := make(chan struct{})
	if reason, err := m.GuardBranchCleanup(context.Background(), "mer-1", refs.deleteFn(ran)); err != nil || reason != "" {
		t.Fatalf("reason=%q err=%v", reason, err)
	}
	<-ran
	// A live session is refused.
	st.sessions["mer-3"] = mkLive("mer-3")
	if reason, _ := m.GuardBranchCleanup(context.Background(), "mer-3", func(context.Context) { t.Fatal("ran for a live session") }); reason == "" {
		t.Fatal("a live session's branch cleanup was not refused")
	}
}

// A restore of another session of the project that has its agent-operation
// guard but has not reached the workspace gate yet would run right after the
// cleanup and may need the branch: the cleanup is refused.
func TestGuardBranchCleanupRefusesWhileAnotherSessionIsAboutToRestore(t *testing.T) {
	m, st, _, _ := newManager()
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "vo/mer-1/root"})
	seedTerminal(st, "mer-2", domain.SessionMetadata{WorkspacePath: "/ws/mer-2", Branch: "vo/mer-1/root"})
	refs := newBranchRefs(t)
	if err := m.beginAgentOperation(context.Background(), "mer-2", agentOperationRestore); err != nil {
		t.Fatal(err)
	}
	ran := make(chan struct{})
	reason, err := m.GuardBranchCleanup(context.Background(), "mer-1", refs.deleteFn(ran))
	if err != nil || !strings.Contains(reason, "being restored") {
		t.Fatalf("reason=%q err=%v, want a refusal for the restore in flight", reason, err)
	}
	refs.requireRefs()

	// Once that restore is done, the cleanup runs; a restore in another
	// project never holds it back.
	m.endAgentOperation("mer-2", agentOperationRestore)
	st.sessions["oth-1"] = domain.SessionRecord{ID: "oth-1", ProjectID: "other", IsTerminated: true}
	if err := m.beginAgentOperation(context.Background(), "oth-1", agentOperationRestore); err != nil {
		t.Fatal(err)
	}
	defer m.endAgentOperation("oth-1", agentOperationRestore)
	if reason, err := m.GuardBranchCleanup(context.Background(), "mer-1", refs.deleteFn(ran)); err != nil || reason != "" {
		t.Fatalf("reason=%q err=%v, want the cleanup to run", reason, err)
	}
	<-ran
}
