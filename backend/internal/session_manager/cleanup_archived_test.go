package sessionmanager

import (
	"fmt"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// A background pass over a session whose project can no longer resolve its
// repo (archived) records the failure once, so the reconciler's candidate scan
// (which skips failed dispositions) stops revisiting and warning every tick.
func TestReconcileStopsRetryingArchivedProjectSession(t *testing.T) {
	m, st, ws := newCleanupLifecycleManager(t)
	seedTerminal(st.fakeStore, "mer-1", domain.SessionMetadata{WorkspacePath: filepath.Join(t.TempDir(), "gone")})
	ws.destroyErr = fmt.Errorf("project archived: %w", ErrProjectNotResolvable)
	st.candidates = []domain.SessionID{"mer-1"}
	if err := m.ReconcileWorkspaceCleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if got := disposition(t, st, "mer-1"); got != domain.DispositionFailed {
		t.Fatalf("disposition = %q, want failed so the session stops being a candidate", got)
	}
}

// Other teardown failures stay unrecorded (retried), unchanged.
func TestReconcileKeepsRetryingOtherTeardownFailures(t *testing.T) {
	m, st, ws := newCleanupLifecycleManager(t)
	seedTerminal(st.fakeStore, "mer-1", domain.SessionMetadata{WorkspacePath: filepath.Join(t.TempDir(), "gone")})
	ws.destroyErr = fmt.Errorf("disk on fire")
	st.candidates = []domain.SessionID{"mer-1"}
	if err := m.ReconcileWorkspaceCleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.GetSessionCleanupFacts(ctx, "mer-1"); ok {
		t.Fatal("a transient failure was recorded as settled")
	}
}

// A recorded repo path that was deleted (the resolver is never consulted)
// fails with ErrWorkspaceRepoUnavailable; once the worktree is gone too it is
// recorded so the reconciler stops retrying. A worktree still on disk is not.
func TestReconcileStopsRetryingMissingRepoWhenWorktreeGone(t *testing.T) {
	m, st, ws := newCleanupLifecycleManager(t)
	seedTerminal(st.fakeStore, "mer-1", domain.SessionMetadata{WorkspacePath: filepath.Join(t.TempDir(), "gone")})
	ws.destroyErr = fmt.Errorf("repo deleted: %w", ports.ErrWorkspaceRepoUnavailable)
	st.candidates = []domain.SessionID{"mer-1"}
	if err := m.ReconcileWorkspaceCleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if got := disposition(t, st, "mer-1"); got != domain.DispositionFailed {
		t.Fatalf("disposition = %q, want failed", got)
	}
}

func TestCleanupMissingRepoWithWorktreeOnDiskIsNotRecorded(t *testing.T) {
	m, st, ws := newCleanupLifecycleManager(t)
	seedTerminal(st.fakeStore, "mer-1", domain.SessionMetadata{WorkspacePath: t.TempDir()})
	ws.destroyErr = fmt.Errorf("repo deleted: %w", ports.ErrWorkspaceRepoUnavailable)
	if _, err := m.CleanupSession(ctx, "mer-1", nil); err != nil {
		t.Fatal(err)
	}
	if facts, ok, _ := st.GetSessionCleanupFacts(ctx, "mer-1"); ok && facts.WorkspaceDisposition == domain.DispositionFailed {
		t.Fatal("a live project's on-disk worktree was recorded as a failure")
	}
}

// An archived project's worktree still on disk (a deliberately preserved one)
// is not recorded as a failure by a manual cleanup sweep.
func TestCleanupArchivedProjectWithWorktreeOnDiskIsNotRecorded(t *testing.T) {
	m, st, ws := newCleanupLifecycleManager(t)
	seedTerminal(st.fakeStore, "mer-1", domain.SessionMetadata{WorkspacePath: t.TempDir()})
	ws.destroyErr = fmt.Errorf("project archived: %w", ErrProjectNotResolvable)
	if _, err := m.Cleanup(ctx, "mer"); err != nil {
		t.Fatal(err)
	}
	if facts, ok, _ := st.GetSessionCleanupFacts(ctx, "mer-1"); ok && facts.WorkspaceDisposition == domain.DispositionFailed {
		t.Fatal("a preserved worktree was recorded as a failure")
	}
}
