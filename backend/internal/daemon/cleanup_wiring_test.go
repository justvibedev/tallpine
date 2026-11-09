package daemon

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	cleanupsvc "tallpine.eu/tallpine/backend/internal/service/cleanup"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
)

type fakeSessionCleaner struct{ res sessionmanager.CleanupResult }

func (f fakeSessionCleaner) CleanupSession(context.Context, domain.SessionID, sessionmanager.CleanupCheck) (sessionmanager.CleanupResult, error) {
	return f.res, nil
}

func (f fakeSessionCleaner) SessionWorktreePaths(context.Context, domain.SessionID) ([]string, error) {
	return nil, nil
}

func TestWorktreeRemoverMapsCleanupResults(t *testing.T) {
	cases := map[string]struct {
		res  sessionmanager.CleanupResult
		want cleanupsvc.WorktreeOutcome
	}{
		"removed": {sessionmanager.CleanupResult{Cleaned: []domain.SessionID{"s"}}, cleanupsvc.WorktreeOutcome{Removed: true}},
		"kept": {sessionmanager.CleanupResult{Skipped: []sessionmanager.CleanupSkip{{SessionID: "s", Reason: "workspace has uncommitted changes"}}},
			cleanupsvc.WorktreeOutcome{Kept: "workspace has uncommitted changes"}},
		"gone":    {sessionmanager.CleanupResult{AlreadyGone: []domain.SessionID{"s"}}, cleanupsvc.WorktreeOutcome{AlreadyGone: true}},
		"nothing": {sessionmanager.CleanupResult{}, cleanupsvc.WorktreeOutcome{AlreadyGone: true}},
	}
	for name, tc := range cases {
		got, err := worktreeRemover{mgr: fakeSessionCleaner{res: tc.res}}.CleanupSession(context.Background(), "s", nil)
		if err != nil || got != tc.want {
			t.Errorf("%s: got %+v, %v; want %+v", name, got, err, tc.want)
		}
	}
}

type checkedSessionManager struct{ check sessionmanager.CleanupCheck }

func (c *checkedSessionManager) SetCleanupCheck(check sessionmanager.CleanupCheck) { c.check = check }

// newCleanup hands the session manager the worktree safety check, so tallpine
// session cleanup refuses what the cleanup engine refuses.
func TestNewCleanupInstallsTheSafetyCheckOnTheSessionManager(t *testing.T) {
	data := t.TempDir()
	mgr := &checkedSessionManager{}
	newCleanup(data, t.TempDir(), nil, mgr, nil, nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if mgr.check == nil {
		t.Fatal("no cleanup check installed")
	}
	ctx := context.Background()
	if reason := mgr.check(ctx, domain.SessionRecord{ID: "s"}, nil); reason != "the session is live again" {
		t.Fatalf("live session: reason %q", reason)
	}
	ended := domain.SessionRecord{ID: "s", IsTerminated: true}
	if reason := mgr.check(ctx, ended, []string{filepath.Join(t.TempDir(), "elsewhere")}); reason != "outside the managed worktree directory" {
		t.Fatalf("worktree outside %s: reason %q", filepath.Join(data, "worktrees"), reason)
	}
}
