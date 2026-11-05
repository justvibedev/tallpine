package sessionmanager

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// lifecycleStore adds the optional candidate scan and record delete to the
// focused fake store.
type lifecycleStore struct {
	*fakeStore
	candidates []domain.SessionID
	deleted    []domain.SessionID
}

func (s *lifecycleStore) ListTerminalCleanupCandidates(context.Context, time.Time) ([]domain.SessionID, error) {
	return s.candidates, nil
}

func (s *lifecycleStore) DeleteTerminatedSession(_ context.Context, id domain.SessionID) (bool, error) {
	rec, ok := s.sessions[id]
	if !ok || !rec.IsTerminated {
		return false, nil
	}
	delete(s.sessions, id)
	s.deleted = append(s.deleted, id)
	return true, nil
}

func newCleanupLifecycleManager(t *testing.T) (*Manager, *lifecycleStore, *fakeWorkspace) {
	t.Helper()
	m, st, _, ws := newManager()
	ls := &lifecycleStore{fakeStore: st}
	m.store = ls
	return m, ls, ws
}

func disposition(t *testing.T, st *lifecycleStore, id domain.SessionID) domain.WorkspaceDisposition {
	t.Helper()
	facts, ok, err := st.GetSessionCleanupFacts(ctx, id)
	if err != nil || !ok {
		t.Fatalf("cleanup facts for %s: ok=%v err=%v", id, ok, err)
	}
	return facts.WorkspaceDisposition
}

// A second cleanup run must not revisit a workspace the first one released.
func TestCleanupSkipsReleasedWorkspaceOnRerun(t *testing.T) {
	m, st, ws := newCleanupLifecycleManager(t)
	seedTerminal(st.fakeStore, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1"})
	seedTerminal(st.fakeStore, "mer-2", domain.SessionMetadata{})
	first, err := m.Cleanup(ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Cleaned) != 1 || ws.destroyed != 1 {
		t.Fatalf("first run = %+v destroyed=%d, want mer-1 cleaned", first, ws.destroyed)
	}
	if got := disposition(t, st, "mer-1"); got != domain.DispositionRemoved {
		t.Fatalf("mer-1 disposition = %q, want removed", got)
	}
	if got := disposition(t, st, "mer-2"); got != domain.DispositionNotApplicable {
		t.Fatalf("mer-2 disposition = %q, want not_applicable", got)
	}
	second, err := m.Cleanup(ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Cleaned)+len(second.AlreadyGone)+len(second.Skipped) != 0 || ws.destroyed != 1 {
		t.Fatalf("second run = %+v destroyed=%d, want nothing revisited", second, ws.destroyed)
	}
}

// A restore bumps the cleanup generation, so the next archive's workspace is
// cleaned again.
func TestCleanupRevisitsAfterANewGeneration(t *testing.T) {
	m, st, ws := newCleanupLifecycleManager(t)
	seedTerminal(st.fakeStore, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1"})
	if _, err := m.Cleanup(ctx, "mer"); err != nil {
		t.Fatal(err)
	}
	rec := st.sessions["mer-1"]
	rec.CleanupGeneration++
	st.sessions["mer-1"] = rec
	if _, err := m.Cleanup(ctx, "mer"); err != nil {
		t.Fatal(err)
	}
	if ws.destroyed != 2 {
		t.Fatalf("destroyed = %d, want the new generation cleaned", ws.destroyed)
	}
}

// The background pass finishes a cleanup that was accepted (recorded
// pending) but interrupted.
func TestReconcileFinishesAcceptedCleanup(t *testing.T) {
	m, st, ws := newCleanupLifecycleManager(t)
	seedTerminal(st.fakeStore, "mer-1", domain.SessionMetadata{WorkspacePath: t.TempDir()})
	if err := st.UpsertSessionCleanupFacts(ctx, domain.SessionCleanupRecord{SessionID: "mer-1", WorkspaceDisposition: domain.DispositionPending}); err != nil {
		t.Fatal(err)
	}
	st.candidates = []domain.SessionID{"mer-1"}
	if err := m.ReconcileWorkspaceCleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if ws.destroyed != 1 {
		t.Fatalf("destroyed = %d, want the accepted cleanup finished", ws.destroyed)
	}
	if got := disposition(t, st, "mer-1"); got != domain.DispositionRemoved {
		t.Fatalf("disposition = %q, want removed", got)
	}
}

// The background pass never removes a worktree still on disk that nobody
// asked to clean up.
func TestReconcileLeavesAnUnacceptedWorktreeOnDisk(t *testing.T) {
	m, st, ws := newCleanupLifecycleManager(t)
	seedTerminal(st.fakeStore, "mer-1", domain.SessionMetadata{WorkspacePath: t.TempDir()})
	st.candidates = []domain.SessionID{"mer-1"}
	if err := m.ReconcileWorkspaceCleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if ws.destroyed != 0 {
		t.Fatalf("destroyed = %d, want the worktree left for the user", ws.destroyed)
	}
	if _, ok, _ := st.GetSessionCleanupFacts(ctx, "mer-1"); ok {
		t.Fatal("cleanup recorded for a worktree the pass left alone")
	}
}

// A session whose worktree directory is already gone is pruned and recorded,
// so it stops being a candidate.
func TestReconcileRecordsAMissingWorktree(t *testing.T) {
	m, st, ws := newCleanupLifecycleManager(t)
	seedTerminal(st.fakeStore, "mer-1", domain.SessionMetadata{WorkspacePath: filepath.Join(t.TempDir(), "gone")})
	seedTerminal(st.fakeStore, "mer-2", domain.SessionMetadata{})
	st.candidates = []domain.SessionID{"mer-1", "mer-2"}
	if err := m.ReconcileWorkspaceCleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if ws.destroyed != 1 {
		t.Fatalf("destroyed = %d, want the stale registration pruned", ws.destroyed)
	}
	if got := disposition(t, st, "mer-1"); !got.Settled() {
		t.Fatalf("mer-1 disposition = %q, want released", got)
	}
	if got := disposition(t, st, "mer-2"); got != domain.DispositionNotApplicable {
		t.Fatalf("mer-2 disposition = %q, want not_applicable", got)
	}
}

func TestDeleteSessionRefusesLiveAndUnreleased(t *testing.T) {
	m, st, ws := newCleanupLifecycleManager(t)
	st.sessions["mer-1"] = mkLive("mer-1")
	if err := m.DeleteSession(ctx, "mer-1", false); !errors.Is(err, ErrSessionNotTerminated) {
		t.Fatalf("live delete err = %v, want ErrSessionNotTerminated", err)
	}
	seedTerminal(st.fakeStore, "mer-2", domain.SessionMetadata{WorkspacePath: "/ws/mer-2"})
	if err := m.DeleteSession(ctx, "mer-2", false); !errors.Is(err, ErrWorkspaceNotReleased) {
		t.Fatalf("unreleased delete err = %v, want ErrWorkspaceNotReleased", err)
	}
	if err := m.DeleteSession(ctx, "missing", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown delete err = %v, want ErrNotFound", err)
	}
	if len(st.deleted) != 0 || ws.destroyed != 0 {
		t.Fatalf("deleted=%v destroyed=%d, want nothing touched", st.deleted, ws.destroyed)
	}
}

func TestDeleteSessionAfterReleaseOrWithCleanup(t *testing.T) {
	m, st, ws := newCleanupLifecycleManager(t)
	seedTerminal(st.fakeStore, "mer-1", domain.SessionMetadata{})
	if err := m.DeleteSession(ctx, "mer-1", false); err != nil {
		t.Fatalf("delete without a workspace: %v", err)
	}
	seedTerminal(st.fakeStore, "mer-2", domain.SessionMetadata{WorkspacePath: "/ws/mer-2"})
	if err := m.DeleteSession(ctx, "mer-2", true); err != nil {
		t.Fatalf("delete with cleanup: %v", err)
	}
	if ws.destroyed != 1 || len(st.deleted) != 2 {
		t.Fatalf("destroyed=%d deleted=%v, want mer-2 cleaned and both deleted", ws.destroyed, st.deleted)
	}
}

func TestDeleteSessionKeepsADirtyWorktree(t *testing.T) {
	m, st, ws := newCleanupLifecycleManager(t)
	seedTerminal(st.fakeStore, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1"})
	ws.destroyErr = errDirtyForTest
	err := m.DeleteSession(ctx, "mer-1", true)
	var refused *CleanupRefusedError
	if !errors.As(err, &refused) || refused.Reason != "workspace has uncommitted changes" {
		t.Fatalf("err = %v, want the dirty refusal", err)
	}
	if len(st.deleted) != 0 {
		t.Fatalf("deleted = %v, want the session kept", st.deleted)
	}
}

var errDirtyForTest = errors.Join(errors.New("refusing"), ports.ErrWorkspaceDirty)

// A kill that frees the worktree records the release, so cleanup and its
// dry-run do not offer the session again.
func TestKillRecordsReleasedWorkspace(t *testing.T) {
	m, st, ws := newCleanupLifecycleManager(t)
	st.sessions["mer-1"] = mkLive("mer-1")
	freed, err := m.Kill(ctx, "mer-1")
	if err != nil || !freed {
		t.Fatalf("freed=%v err=%v", freed, err)
	}
	if got := disposition(t, st, "mer-1"); got != domain.DispositionRemoved {
		t.Fatalf("disposition = %q, want removed", got)
	}
	if res, err := m.Cleanup(ctx, "mer"); err != nil || len(res.Cleaned)+len(res.AlreadyGone) != 0 || ws.destroyed != 1 {
		t.Fatalf("cleanup after kill = %+v err=%v destroyed=%d, want nothing revisited", res, err, ws.destroyed)
	}
}
