package sessionmanager

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// Every sweep of an archived project's preserved worktree is refused the same
// way; that expected refusal is reported to the caller, not warned in the
// daemon log on every pass.
func TestCleanupArchivedProjectRefusalIsNotLoggedAsWarning(t *testing.T) {
	m, st, ws := newCleanupLifecycleManager(t)
	var buf bytes.Buffer
	m.logger = slog.New(slog.NewTextHandler(&buf, nil))
	seedTerminal(st.fakeStore, "mer-1", domain.SessionMetadata{WorkspacePath: t.TempDir()})
	ws.destroyErr = fmt.Errorf("project archived: %w", ErrProjectNotResolvable)
	for range 3 {
		if _, err := m.Cleanup(ctx, "mer"); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Contains(buf.String(), "workspace teardown failed") {
		t.Fatalf("archived-project refusal warned in the log:\n%s", buf.String())
	}
}

// A session of an archived project whose worktree is still on disk is refused
// before any teardown attempt, on every sweep, and nothing is recorded: the
// worktree stays for manual removal.
func TestCleanupSweepSkipsArchivedProjectWorktreeWithoutTeardownAttempt(t *testing.T) {
	m, st, ws := newCleanupLifecycleManager(t)
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", ArchivedAt: time.Now()}
	dir := t.TempDir()
	seedTerminal(st.fakeStore, "mer-1", domain.SessionMetadata{WorkspacePath: dir})
	for range 2 {
		res, err := m.Cleanup(ctx, "mer")
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Skipped) != 1 || res.Skipped[0].Reason != archivedProjectReason {
			t.Fatalf("skipped = %v, want the archived-project reason", res.Skipped)
		}
	}
	if ws.destroyed != 0 {
		t.Fatalf("teardown attempted %d time(s) for an archived project's session", ws.destroyed)
	}
	if _, ok, _ := st.GetSessionCleanupFacts(ctx, "mer-1"); ok {
		t.Fatal("a preserved worktree was recorded")
	}
	if _, err := os.Lstat(dir); err != nil {
		t.Fatalf("worktree not preserved: %v", err)
	}
}

// An accepted (pending) cleanup of an archived project's session still reaches
// teardown, so its record settles and the reconciler stops revisiting it.
func TestCleanupSettlesPendingArchivedProjectSessionWithWorktreeOnDisk(t *testing.T) {
	m, st, ws := newCleanupLifecycleManager(t)
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", ArchivedAt: time.Now()}
	seedTerminal(st.fakeStore, "mer-1", domain.SessionMetadata{WorkspacePath: t.TempDir()})
	if err := m.recordWorkspaceCleanup(ctx, st.sessions["mer-1"], domain.DispositionPending, ""); err != nil {
		t.Fatal(err)
	}
	ws.destroyErr = fmt.Errorf("project archived: %w", ErrProjectNotResolvable)
	if _, err := m.CleanupSession(ctx, "mer-1", nil); err != nil {
		t.Fatal(err)
	}
	if got := disposition(t, st, "mer-1"); got == domain.DispositionPending {
		t.Fatal("disposition still pending: the reconciler would retry forever")
	}
}

// A session with a recorded repo path does not depend on the project resolver,
// so an archived project does not stop its teardown.
func TestCleanupArchivedProjectSessionWithRecordedRepoPathReachesTeardown(t *testing.T) {
	m, st, ws := newCleanupLifecycleManager(t)
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", ArchivedAt: time.Now()}
	seedTerminal(st.fakeStore, "mer-1", domain.SessionMetadata{WorkspacePath: t.TempDir(), WorkspaceRepoPath: "/repo"})
	res, err := m.Cleanup(ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}
	if ws.destroyed != 1 || len(res.Cleaned) != 1 {
		t.Fatalf("destroyed=%d cleaned=%v, want teardown reached", ws.destroyed, res.Cleaned)
	}
}

// A settled (failed) record of an archived project's session with its worktree
// on disk is not retried by later sweeps: no teardown attempt, record unchanged.
func TestCleanupSweepDoesNotRetryFailedArchivedProjectSession(t *testing.T) {
	m, st, ws := newCleanupLifecycleManager(t)
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", ArchivedAt: time.Now()}
	seedTerminal(st.fakeStore, "mer-1", domain.SessionMetadata{WorkspacePath: t.TempDir()})
	if err := m.recordWorkspaceCleanup(ctx, st.sessions["mer-1"], domain.DispositionFailed, "WORKSPACE_CLEANUP_FAILED"); err != nil {
		t.Fatal(err)
	}
	res, err := m.Cleanup(ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Reason != archivedProjectReason {
		t.Fatalf("skipped = %v, want the archived-project reason", res.Skipped)
	}
	if ws.destroyed != 0 {
		t.Fatalf("teardown attempted %d time(s)", ws.destroyed)
	}
	if got := disposition(t, st, "mer-1"); got != domain.DispositionFailed {
		t.Fatalf("disposition = %q, want failed unchanged", got)
	}
}

// An archived scratch project's teardown never uses the project resolver, so
// its session still reaches teardown.
func TestCleanupArchivedScratchProjectSessionReachesTeardown(t *testing.T) {
	m, st, ws := newCleanupLifecycleManager(t)
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Kind: domain.ProjectKindScratch, ArchivedAt: time.Now()}
	seedTerminal(st.fakeStore, "mer-1", domain.SessionMetadata{WorkspacePath: t.TempDir()})
	res, err := m.Cleanup(ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}
	if ws.destroyed != 1 || len(res.Cleaned) != 1 {
		t.Fatalf("destroyed=%d cleaned=%v, want teardown reached", ws.destroyed, res.Cleaned)
	}
}
