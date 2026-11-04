package sessionmanager

import (
	"context"
	"fmt"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestCleanupSessionReclaimsOnlyThatTerminatedSession(t *testing.T) {
	m, st, _, ws := newManager()
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1"})
	seedTerminal(st, "mer-2", domain.SessionMetadata{WorkspacePath: "/ws/mer-2"})
	res, err := m.CleanupSession(ctx, "mer-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Cleaned) != 1 || res.Cleaned[0] != "mer-1" || ws.destroyed != 1 {
		t.Fatalf("cleaned=%v destroyed=%d, want only mer-1", res.Cleaned, ws.destroyed)
	}
}

func TestCleanupSessionNeverTouchesALiveSession(t *testing.T) {
	m, st, rt, ws := newManager()
	live := mkLive("mer-1")
	live.Metadata.WorkspacePath = "/ws/mer-1"
	live.Metadata.RuntimeHandleID = "mer-1-runtime"
	st.sessions["mer-1"] = live
	res, err := m.CleanupSession(ctx, "mer-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Reason != "session is live" {
		t.Fatalf("skipped = %v, want the live session refused", res.Skipped)
	}
	if ws.destroyed != 0 || rt.destroyed != 0 {
		t.Fatalf("live session torn down: workspace=%d runtime=%d", ws.destroyed, rt.destroyed)
	}
	if res, _ := m.CleanupSession(ctx, "missing", nil); len(res.Skipped) != 1 || res.Skipped[0].Reason != "session not found" {
		t.Fatalf("unknown session = %+v", res)
	}
}

func TestCleanupSessionKeepsADirtyWorktree(t *testing.T) {
	m, st, _, ws := newManager()
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1"})
	ws.destroyErr = fmt.Errorf("refusing: %w", ports.ErrWorkspaceDirty)
	res, err := m.CleanupSession(ctx, "mer-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Cleaned) != 0 || len(res.Skipped) != 1 || res.Skipped[0].Reason != "workspace has uncommitted changes" {
		t.Fatalf("result = %+v, want the dirty worktree kept", res)
	}
}

// A session listed as terminated but restored before cleanup takes its guard
// must keep its runtime and workspace.
func TestCleanupTerminatedRechecksTheSessionUnderItsGuard(t *testing.T) {
	m, st, rt, ws := newManager()
	live := mkLive("mer-1")
	live.Metadata.WorkspacePath = "/ws/mer-1"
	live.Metadata.RuntimeHandleID = "mer-1-runtime"
	st.sessions["mer-1"] = live
	stale := live
	stale.IsTerminated = true
	var res CleanupResult
	m.cleanupTerminated(ctx, stale, &res, nil)
	if len(res.Skipped) != 1 || res.Skipped[0].Reason != "session was restored" {
		t.Fatalf("result = %+v, want the restored session skipped", res)
	}
	if rt.destroyed != 0 || ws.destroyed != 0 {
		t.Fatalf("restored session torn down: runtime=%d workspace=%d", rt.destroyed, ws.destroyed)
	}
}

// The caller's check runs under the guard with the fresh record and every
// worktree of the session; a reason from it keeps everything.
func TestCleanupSessionRunsTheCheckOnEveryWorktreeBeforeTeardown(t *testing.T) {
	m, st, rt, ws := newManager()
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", RuntimeHandleID: "mer-1-runtime"})
	st.worktrees["mer-1"] = []domain.SessionWorktreeRecord{
		{SessionID: "mer-1", RepoName: domain.RootWorkspaceRepoName, WorktreePath: "/ws/mer-1"},
		{SessionID: "mer-1", RepoName: "api", WorktreePath: "/ws/mer-1/api"},
	}
	var gotPaths []string
	var gotRec domain.SessionRecord
	res, err := m.CleanupSession(ctx, "mer-1", func(_ context.Context, rec domain.SessionRecord, paths []string) string {
		gotRec, gotPaths = rec, paths
		return "a rebase, merge or similar operation is in progress"
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotRec.ID != "mer-1" || !gotRec.IsTerminated {
		t.Fatalf("check got %+v, want the fresh terminated record", gotRec)
	}
	if len(gotPaths) != 2 || gotPaths[0] != "/ws/mer-1" || gotPaths[1] != "/ws/mer-1/api" {
		t.Fatalf("check got paths %v, want the root and the child worktree", gotPaths)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Reason != "a rebase, merge or similar operation is in progress" {
		t.Fatalf("result = %+v, want the check's reason", res)
	}
	if rt.destroyed != 0 || ws.destroyed != 0 {
		t.Fatalf("torn down although the check kept it: runtime=%d workspace=%d", rt.destroyed, ws.destroyed)
	}
	if res, _ := m.CleanupSession(ctx, "mer-1", func(context.Context, domain.SessionRecord, []string) string { return "" }); len(res.Cleaned) != 1 {
		t.Fatalf("an approving check did not clean: %+v", res)
	}
}

// tallpine session cleanup (Cleanup) runs the manager's default check under the
// guard, like a scheduled pass.
func TestCleanupRunsTheDefaultCheck(t *testing.T) {
	m, st, _, ws := newManager()
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1"})
	m.SetCleanupCheck(func(context.Context, domain.SessionRecord, []string) string { return "uncommitted changes" })
	res, err := m.Cleanup(ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Cleaned) != 0 || len(res.Skipped) != 1 || res.Skipped[0].Reason != "uncommitted changes" || ws.destroyed != 0 {
		t.Fatalf("result = %+v destroyed=%d, want the default check to keep it", res, ws.destroyed)
	}
}

// The check runs again after the pre-remove step, right before removal, so a
// worktree the project's script changed is kept.
func TestCleanupChecksAgainRightBeforeRemoval(t *testing.T) {
	m, st, _, ws := newManager()
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1"})
	calls := 0
	res, err := m.CleanupSession(ctx, "mer-1", func(context.Context, domain.SessionRecord, []string) string {
		calls++
		if calls == 1 {
			return ""
		}
		return "a rebase, merge or similar operation is in progress"
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("check ran %d times, want before teardown and again before removal", calls)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Reason != "a rebase, merge or similar operation is in progress" || ws.destroyed != 0 {
		t.Fatalf("result = %+v destroyed=%d, want kept by the second check", res, ws.destroyed)
	}
}

// The check runs git commands on every repo of the session, so it never runs
// with the project's workspace gate held: spawns and restores into the project
// must not wait on it.
func TestCleanupCheckRunsWithoutTheWorkspaceGate(t *testing.T) {
	m, st, _, ws := newManager()
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1"})
	calls := 0
	res, err := m.CleanupSession(ctx, "mer-1", func(_ context.Context, rec domain.SessionRecord, _ []string) string {
		calls++
		gate := m.workspaceGate(rec.ProjectID)
		if !gate.TryLock() {
			t.Errorf("check %d ran with the workspace gate held", calls)
			return "gate held"
		}
		gate.Unlock()
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(res.Cleaned) != 1 || ws.destroyed != 1 {
		t.Fatalf("calls=%d result=%+v destroyed=%d, want both checks and the cleanup", calls, res, ws.destroyed)
	}
}
