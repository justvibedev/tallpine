package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestKill_TearsDownRuntimeAndWorkspace(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newManager()
	preview := &fakePreviewLifecycle{}
	browser := &fakeBrowserLifecycle{}
	reviewer := &fakeReviewerTerminator{}
	m.preview = preview
	m.browser = browser
	m.SetReviewerTerminator(reviewer)
	dataDir := t.TempDir()
	m.dataDir = dataDir
	st.sessions["mer-1"] = mkLive("mer-1")
	if _, err := m.writeSystemPromptFile("mer-1", "system prompt"); err != nil {
		t.Fatal(err)
	}
	freed, err := m.Kill(ctx, "mer-1")
	if err != nil || !freed {
		t.Fatalf("freed=%v err=%v", freed, err)
	}
	if rt.destroyed != 1 || ws.destroyed != 1 {
		t.Fatal("kill should destroy runtime and workspace")
	}
	if !reflect.DeepEqual(preview.stopped, []domain.SessionID{"mer-1"}) {
		t.Fatalf("preview stops = %v, want [mer-1]", preview.stopped)
	}
	if !reflect.DeepEqual(browser.destroyed, []domain.SessionID{"mer-1"}) {
		t.Fatalf("browser destroys = %v, want [mer-1]", browser.destroyed)
	}
	if !reflect.DeepEqual(reviewer.calls, []domain.SessionID{"mer-1"}) {
		t.Fatalf("reviewer terminates = %v, want [mer-1]", reviewer.calls)
	}
	if len(reviewer.bodies) != 1 || !strings.Contains(reviewer.bodies[0], "termination") {
		t.Fatalf("reviewer terminate bodies = %v", reviewer.bodies)
	}
	requireNoPromptDir(t, dataDir, "mer-1")
}

func TestKillCleanupScriptHasNoTimeLimit(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	m.killTeardown = 2 * time.Second
	m.dataDir = t.TempDir()
	workspace := filepath.Join(m.dataDir, "worktrees", "mer", "mer-1")
	if err := os.MkdirAll(workspace, 0o750); err != nil {
		t.Fatal(err)
	}
	rec := mkLive("mer-1")
	rec.Metadata.WorkspacePath = workspace
	st.sessions[rec.ID] = rec
	project := st.projects["mer"]
	command := "sleep 3 && echo finished > cleanup-marker"
	if runtime.GOOS == "windows" {
		command = "powershell -NoProfile -NonInteractive -Command \"Start-Sleep -Seconds 3\" && echo finished > cleanup-marker"
	}
	project.Config.PreRemove = []string{command}
	st.projects["mer"] = project

	freed, err := m.Kill(ctx, rec.ID)
	if err != nil || !freed || ws.destroyCtxErr != nil || !st.sessions[rec.ID].IsTerminated {
		t.Fatalf("long cleanup must finish before workspace removal: freed=%v err=%v workspaceCtx=%v terminated=%v", freed, err, ws.destroyCtxErr, st.sessions[rec.ID].IsTerminated)
	}
	if _, err := os.Stat(filepath.Join(workspace, "cleanup-marker")); err != nil {
		t.Fatalf("cleanup command did not finish: %v", err)
	}
}

func TestKillCleanupScriptFailurePreservesWorkspaceForRetry(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newManager()
	m.dataDir = t.TempDir()
	workspace := filepath.Join(m.dataDir, "worktrees", "mer", "mer-1")
	if err := os.MkdirAll(workspace, 0o750); err != nil {
		t.Fatal(err)
	}
	rec := mkLive("mer-1")
	rec.Metadata.WorkspacePath = workspace
	st.sessions[rec.ID] = rec
	project := st.projects["mer"]
	project.Path = t.TempDir()
	project.Config.PreRemove = []string{"echo project-secret && exit 7"}
	project.Config.Env = map[string]string{"PROJECT_TOKEN": "project-secret"}
	st.projects["mer"] = project

	freed, err := m.Kill(ctx, rec.ID)
	if freed || !errors.Is(err, ErrCleanupScript) || strings.Contains(err.Error(), "project-secret") {
		t.Fatalf("kill freed=%v err=%v", freed, err)
	}
	if rt.destroyed != 1 || ws.destroyed != 0 || !st.sessions[rec.ID].IsTerminated {
		t.Fatalf("failed cleanup must stop runtime, preserve workspace, and terminate session: runtime=%d workspace=%d rec=%+v", rt.destroyed, ws.destroyed, st.sessions[rec.ID])
	}
	project.Config.PreRemove = []string{"echo cleaned > cleanup-marker"}
	st.projects["mer"] = project
	result, err := m.Cleanup(ctx, "mer")
	if err != nil || len(result.Cleaned) != 1 || ws.destroyed != 1 {
		t.Fatalf("retry cleanup result=%+v err=%v destroyed=%d", result, err, ws.destroyed)
	}
	if _, err := os.Stat(filepath.Join(workspace, "cleanup-marker")); err != nil {
		t.Fatalf("cleanup script did not run in workspace: %v", err)
	}
}

// A caller that gives up must not take the teardown down with it. The REST
// layer caps a request at cfg.RequestTimeout, and a session whose worktree
// carries a large ignored tree used to run past that: the request context was
// cancelled mid-Kill, so the agent was already stopped while the row still read
// as alive, and the caller got a 500 for a session that was half gone.
func TestKill_CompletesTeardownAfterCallerContextIsCancelled(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newManager()
	st.sessions["mer-1"] = mkLive("mer-1")

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	freed, err := m.Kill(cancelled, "mer-1")
	if err != nil || !freed {
		t.Fatalf("freed=%v err=%v, want a completed teardown", freed, err)
	}
	if ws.destroyCtxErr != nil {
		t.Fatalf("workspace teardown saw ctx.Err() = %v, want a live context", ws.destroyCtxErr)
	}
	if rt.destroyed != 1 || ws.destroyed != 1 {
		t.Fatalf("runtime=%d workspace=%d, want both torn down", rt.destroyed, ws.destroyed)
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Fatal("session row must be marked terminated")
	}
}

func TestKill_NativeTerminationFailurePreservesRuntimeAndWorkspace(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newManager()
	agent := &nativeTerminatingAgent{wantID: "native-7", err: errors.New("prime stop failed")}
	m.agents = singleAgent{agent: agent}
	rec := mkLive("mer-1")
	rec.Metadata.AgentSessionID = "native-7"
	st.sessions[rec.ID] = rec

	freed, err := m.Kill(ctx, rec.ID)
	if err == nil || !strings.Contains(err.Error(), "prime stop failed") {
		t.Fatalf("freed=%v err=%v, want native termination error", freed, err)
	}
	if freed || rt.destroyed != 0 || ws.destroyed != 0 {
		t.Fatalf("freed=%v runtime=%d workspace=%d, want no destructive teardown", freed, rt.destroyed, ws.destroyed)
	}
	if st.sessions[rec.ID].IsTerminated {
		t.Fatal("session must remain active when native termination fails")
	}
}

func TestKill_TerminatesNativeSessionBeforeRuntime(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newManager()
	agent := &nativeTerminatingAgent{wantID: "native-7"}
	m.agents = singleAgent{agent: agent}
	rec := mkLive("mer-1")
	rec.Metadata.AgentSessionID = "native-7"
	st.sessions[rec.ID] = rec

	freed, err := m.Kill(ctx, rec.ID)
	if err != nil || !freed {
		t.Fatalf("freed=%v err=%v", freed, err)
	}
	if agent.calls != 1 || rt.destroyed != 1 || ws.destroyed != 1 {
		t.Fatalf("native=%d runtime=%d workspace=%d, want one each", agent.calls, rt.destroyed, ws.destroyed)
	}
	if !st.sessions[rec.ID].IsTerminated {
		t.Fatal("session must be terminated after successful native and Tallpine teardown")
	}
}

func TestKill_UnknownHarnessSkipsNativeTerminationAndTearsDown(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newManager()
	m.agents = missingAgents{}
	rec := mkLive("mer-1")
	rec.Harness = "retired-agent"
	rec.Metadata.AgentSessionID = "native-7"
	st.sessions[rec.ID] = rec

	freed, err := m.Kill(ctx, rec.ID)
	if err != nil || !freed {
		t.Fatalf("freed=%v err=%v, want successful teardown", freed, err)
	}
	if rt.destroyed != 1 || ws.destroyed != 1 {
		t.Fatalf("runtime=%d workspace=%d, want one each", rt.destroyed, ws.destroyed)
	}
	if !st.sessions[rec.ID].IsTerminated {
		t.Fatal("session must be terminated when only native adapter lookup is missing")
	}
}

func TestKill_ReviewerTeardownFailureLeavesSessionActive(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newManager()
	m.SetReviewerTerminator(&fakeReviewerTerminator{err: errors.New("reviewer still alive")})
	st.sessions["mer-1"] = mkLive("mer-1")

	freed, err := m.Kill(ctx, "mer-1")
	if err == nil || !strings.Contains(err.Error(), "reviewer still alive") {
		t.Fatalf("freed=%v err=%v, want reviewer teardown error", freed, err)
	}
	if freed {
		t.Fatal("workspace must not be reported freed when reviewer teardown fails")
	}
	if rt.destroyed != 1 {
		t.Fatalf("worker runtime destroy calls = %d, want 1", rt.destroyed)
	}
	if ws.destroyed != 0 {
		t.Fatalf("workspace destroy calls = %d, want 0 after reviewer failure", ws.destroyed)
	}
	if st.sessions["mer-1"].IsTerminated {
		t.Fatal("session must remain active when reviewer teardown fails")
	}
}

// TestKill_ClosesScopedShellTerminalsBeforeWorkspaceTeardown is the regression
// for the bug where Kill removed a session's worktree while a shell terminal
// scoped to it was still open, pointed at a directory that no longer existed
// (and, on Windows, an open handle on that directory could refuse deletion).
// The gate must also release (EndSessionTeardown) once Kill's own teardown is
// done, or the session's shell terminals would stay locked out forever.
func TestKill_ClosesScopedShellTerminalsBeforeWorkspaceTeardown(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	var calls []string
	ws.sharedLog = &calls
	closer := &fakeShellTerminalCloser{sharedLog: &calls}
	m.SetShellTerminalCloser(closer)
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer",
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-1", WorkspaceRepoPath: "/ws/mer-1", RuntimeHandleID: "h1"},
		Activity: domain.Activity{State: domain.ActivityActive},
	}

	if _, err := m.Kill(ctx, "mer-1"); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	if len(closer.began) != 1 || closer.began[0] != "mer-1" {
		t.Fatalf("began = %v, want [mer-1]", closer.began)
	}
	if len(closer.ended) != 1 || closer.ended[0] != "mer-1" {
		t.Fatalf("ended = %v, want [mer-1]", closer.ended)
	}
	beginIdx, destroyIdx, endIdx := -1, -1, -1
	for i, c := range calls {
		switch c {
		case "BeginSessionTeardown:mer-1":
			beginIdx = i
		case "Destroy:" + domain.RootWorkspaceRepoName:
			destroyIdx = i
		case "EndSessionTeardown:mer-1":
			endIdx = i
		}
	}
	if beginIdx == -1 || destroyIdx == -1 || endIdx == -1 {
		t.Fatalf("call log missing expected entries: %v", calls)
	}
	if beginIdx >= destroyIdx || destroyIdx >= endIdx {
		t.Fatalf("call order = %v, want begin, then destroy, then end", calls)
	}
}

// TestKill_RefusesWorkspaceTeardownWhenShellTerminalsWontClose is the
// regression for the bug where a shell terminal that failed to close was
// logged and forgotten, then the worktree was destroyed anyway — leaving a
// still-live shell pointed at nothing. Kill must refuse the workspace release
// in that case, the same shape as a dirty-workspace refusal.
func TestKill_RefusesWorkspaceTeardownWhenShellTerminalsWontClose(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newManager()
	m.SetShellTerminalCloser(&fakeShellTerminalCloser{err: errors.New("shellterm-1: still alive")})
	st.sessions["mer-1"] = mkLive("mer-1")
	st.worktrees["mer-1"] = []domain.SessionWorktreeRecord{
		{SessionID: "mer-1", RepoName: domain.RootWorkspaceRepoName, WorktreePath: "/ws/mer-1"},
	}

	freed, err := m.Kill(ctx, "mer-1")
	if err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if freed {
		t.Error("freed = true, want false: an unconfirmed shell close must block workspace teardown")
	}
	if ws.destroyed != 0 {
		t.Errorf("workspace destroyed = %d, want 0: the worktree must be left alone", ws.destroyed)
	}
	if rt.destroyed != 1 {
		t.Error("the session's own runtime must still be destroyed")
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Error("session should still be marked terminated")
	}
	// Same guard the plain dirty-workspace refusal applies (#2319): the restore
	// marker must not survive a user kill, or the next boot's RestoreAll could
	// resurrect a session the user explicitly terminated — even though the
	// worktree itself was left alone because a shell wouldn't confirm closed.
	if rows := st.worktrees["mer-1"]; len(rows) != 0 {
		t.Errorf("restore marker = %+v, want cleared", rows)
	}
}

// A nil closer (SetShellTerminalCloser never called, e.g. a daemon boot path
// that skips shellterm) must be a no-op, not a nil-pointer panic.
func TestKill_NilShellTerminalCloserIsNoop(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newManager()
	st.sessions["mer-1"] = mkLive("mer-1")

	if _, err := m.Kill(ctx, "mer-1"); err != nil {
		t.Fatalf("Kill: %v", err)
	}
}

func TestKill_TerminatesIncompleteHandle(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newManager()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Activity: domain.Activity{State: domain.ActivityActive}}
	freed, err := m.Kill(ctx, "mer-1")
	if err != nil {
		t.Fatalf("want nil error, got %v", err)
	}
	if freed {
		t.Fatal("freed = true, want false for session with no workspace")
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Fatal("session should be terminated even without a handle")
	}
}

// TestKill_DirtyWorkspacePreservesAndTerminates: a workspace teardown
// refused because of uncommitted work must NOT force-remove the worktree. Kill
// succeeds with freed=false and still marks the session terminated; cleanup can
// reclaim the preserved worktree after the user resolves the dirty state.
func TestKill_DirtyWorkspacePreservesAndTerminates(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newManager()
	st.sessions["mer-1"] = mkLive("mer-1")
	ws.destroyErr = fmt.Errorf("gitworktree: refusing to remove: %w", ports.ErrWorkspaceDirty)
	freed, err := m.Kill(ctx, "mer-1")
	if err != nil {
		t.Fatalf("kill dirty workspace err = %v, want nil", err)
	}
	if freed {
		t.Fatal("freed = true, want false for preserved workspace")
	}
	if rt.destroyed != 1 {
		t.Fatal("runtime should be destroyed")
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Fatal("session should be terminated even when the workspace is preserved")
	}
}

// A project directory the user deleted leaves git unable to reclaim the
// session's worktree, and failing the kill for that stranded the session in
// the sidebar permanently: every retry answered 500 and the row never left.
// The session must still terminate, with the worktree preserved on disk.
func TestKill_MissingProjectRepoPreservesWorkspaceAndTerminates(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	ws.destroyErr = fmt.Errorf("gitworktree: repository %q is no longer on disk: %w", "/gone", ports.ErrWorkspaceRepoUnavailable)
	st.sessions["mer-1"] = mkLive("mer-1")

	freed, err := m.Kill(ctx, "mer-1")
	if err != nil {
		t.Fatalf("kill err = %v, want the session to terminate anyway", err)
	}
	if freed {
		t.Fatal("freed = true, want false: the worktree was left on disk")
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Fatal("session must be marked terminated so it leaves the sidebar")
	}
}

// A worktree whose directory is still pinned by a process handle (a live agent
// or scoped shell on Windows) survives the removeAll retry budget. git has
// already unregistered it by then, so nothing is being reconciled — only the
// directory cannot go away right now. Erroring the kill for that stranded the
// session in the sidebar forever: every retry answered 500 and the row never
// left (#3408). The kill must succeed with freed=false, and the leftover
// directory is left for a later `tallpine session cleanup` pass to retry.
func TestKill_DeferredWorkspaceRemovalPreservesAndTerminates(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	st.sessions["mer-1"] = mkLive("mer-1")
	ws.destroyErr = fmt.Errorf("gitworktree: remove unregistered path %q: %w (deferred: %w)", "/ws/mer-1", ports.ErrWorkspaceDeferred, errors.New("access denied"))

	freed, err := m.Kill(ctx, "mer-1")
	if err != nil {
		t.Fatalf("kill err = %v, want the session to terminate anyway", err)
	}
	if freed {
		t.Fatal("freed = true, want false: the worktree was left on disk")
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Fatal("session must be marked terminated so it leaves the sidebar")
	}
}

func TestKill_DeletesStaleRestoreMarker(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newManager()
	st.sessions["mer-1"] = mkLive("mer-1")
	st.worktrees["mer-1"] = []domain.SessionWorktreeRecord{
		{SessionID: "mer-1", RepoName: domain.RootWorkspaceRepoName, WorktreePath: "/tmp/wt"},
	}

	freed, err := m.Kill(ctx, "mer-1")
	if err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if !freed {
		t.Fatal("Kill freed = false, want true")
	}
	if rows := st.worktrees["mer-1"]; len(rows) != 0 {
		t.Fatalf("stale restore marker = %+v, want deleted", rows)
	}
}

// TestKill_UnnamedWorkspaceErrorPreservesAndTerminates is the #5463 regression
// on the workspace half. A teardown failure Tallpine has no typed name for (a locked
// worktree git will not prune, a path that no longer resolves to a live
// worktree) used to fail the kill, which left `terminated` false — and
// `tallpine session cleanup` only walks terminated sessions, so the row was reachable
// by no path at all. Nothing is force-removed: the worktree stays on disk for
// cleanup to retry and report on. Only the session's claim on the sidebar goes.
func TestKill_UnnamedWorkspaceErrorPreservesAndTerminates(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	st.sessions["mer-1"] = mkLive("mer-1")
	ws.destroyErr = errors.New("path is still registered after git worktree prune")

	freed, err := m.Kill(ctx, "mer-1")
	if err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if freed {
		t.Fatal("freed = true, want false: the worktree was left on disk")
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Fatal("session must be marked terminated so cleanup can reach it")
	}
	if calls := strings.Join(ws.calls, ","); strings.Contains(calls, "ForceDestroy") {
		t.Fatalf("calls = %s, want no ForceDestroy: a refused teardown is never forced", calls)
	}
}

// TestKill_RuntimeDestroyErrorFailsClosedEvenWhenProbeReadsGone pins the
// runtime half of #5463 shut. Every runtime's Destroy returns nil when it
// confirms the session is absent, so an error means it may still be live — and
// a follow-up IsAlive cannot overrule that: conpty's probe dials a listener the
// graceful shutdown already closed, so a hung pty-host reads as gone. Kill must
// surface the error and leave the session and its workspace alone.
func TestKill_RuntimeDestroyErrorFailsClosedEvenWhenProbeReadsGone(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newManager()
	st.sessions["mer-1"] = mkLive("mer-1")
	rt.destroyErr = errors.New("conpty: pty-host pid 42 is still alive after teardown")
	rt.aliveByHandle = map[string]bool{"h1": false}

	freed, err := m.Kill(ctx, "mer-1")
	if err == nil || !strings.Contains(err.Error(), "still alive after teardown") {
		t.Fatalf("freed=%v err=%v, want the runtime error surfaced", freed, err)
	}
	if ws.destroyed != 0 {
		t.Fatalf("workspace destroys = %d, want 0: teardown must stop at the runtime", ws.destroyed)
	}
	if st.sessions["mer-1"].IsTerminated {
		t.Fatal("session must stay active while the runtime may be live")
	}
}

// TestKill_InconclusiveRuntimeProbeStaysFailClosed pins the other side.
// ErrRuntimeProbeInconclusive means the runtime may still be live, and its port
// contract forbids callers from treating the session as dead. Terminating here
// would leave a possibly-live agent running with no row pointing at it, so the
// kill must keep failing and the workspace must stay untouched.
func TestKill_InconclusiveRuntimeProbeStaysFailClosed(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newManager()
	st.sessions["mer-1"] = mkLive("mer-1")
	rt.destroyErr = fmt.Errorf("conpty: pty registry scan incomplete: %w", ports.ErrRuntimeProbeInconclusive)

	freed, err := m.Kill(ctx, "mer-1")
	if err == nil || !strings.Contains(err.Error(), "runtime") {
		t.Fatalf("freed=%v err=%v, want the runtime error surfaced", freed, err)
	}
	if ws.destroyed != 0 {
		t.Fatalf("workspace destroys = %d, want 0: teardown must stop at the runtime", ws.destroyed)
	}
	if st.sessions["mer-1"].IsTerminated {
		t.Fatal("session must stay active while the runtime may be live")
	}
}
func TestKill_WorkspaceProjectDestroysChildrenBeforeRoot(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newManager()
	seedWorkspaceProjectSession(st)

	freed, err := m.Kill(ctx, "mer-1")
	if err != nil || !freed {
		t.Fatalf("freed=%v err=%v", freed, err)
	}
	if rt.destroyed != 1 {
		t.Fatalf("runtime destroy calls = %d, want 1", rt.destroyed)
	}
	want := []string{"Destroy:api", "Destroy:__root__"}
	if got := ws.calls; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("destroy order = %v, want %v", got, want)
	}
}

// TestKill_WorkspaceProjectAlreadyAbsentStaysFreed guards the boundary between
// the two answers destroyWorkspaceProjectRows returns. Kill's freed flag is not
// a disk-reclaim count: freed=false means the workspace was preserved, and the
// CLI prints "workspace preserved" from it. A worktree that was already gone
// was torn down, not preserved, so collapsing freed onto the reclaim outcome
// would make kill report a preserved workspace that does not exist.
func TestKill_WorkspaceProjectAlreadyAbsentStaysFreed(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	ws.destroyReclaim = ports.WorkspaceReclaimAlreadyAbsent
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Kind: domain.ProjectKindWorkspace, Config: testRoleAgents()}
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{Name: "api", RelativePath: "api"}}
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Metadata:  domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1"},
		Activity:  domain.Activity{State: domain.ActivityActive},
	}
	st.worktrees["mer-1"] = []domain.SessionWorktreeRecord{
		{SessionID: "mer-1", RepoName: domain.RootWorkspaceRepoName, Branch: "tp/mer-1", WorktreePath: "/ws/mer-1"},
		{SessionID: "mer-1", RepoName: "api", Branch: "tp/mer-1", WorktreePath: "/ws/mer-1/api"},
	}

	freed, err := m.Kill(ctx, "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if !freed {
		t.Fatal("freed = false, want true: the worktrees were torn down, not preserved")
	}
}

func TestKill_WorkspaceProjectFailsClosedOnUnregisteredChildRows(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Kind: domain.ProjectKindWorkspace, Config: testRoleAgents()}
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{Name: "api", RelativePath: "api"}}
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Metadata:  domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1", RuntimeHandleID: "h1"},
		Activity:  domain.Activity{State: domain.ActivityActive},
	}
	st.worktrees["mer-1"] = []domain.SessionWorktreeRecord{
		{SessionID: "mer-1", RepoName: domain.RootWorkspaceRepoName, Branch: "tp/mer-1", WorktreePath: "/ws/mer-1"},
		{SessionID: "mer-1", RepoName: "old-api", Branch: "tp/mer-1", WorktreePath: "/ws/mer-1/old-api"},
		{SessionID: "mer-1", RepoName: "api", Branch: "tp/mer-1", WorktreePath: "/ws/mer-1/api"},
	}

	freed, err := m.Kill(ctx, "mer-1")
	if err == nil || !strings.Contains(err.Error(), "old-api") {
		t.Fatalf("freed=%v err=%v, want unresolved historical row error", freed, err)
	}
	if freed {
		t.Fatal("workspace must not be reported freed when historical rows are unresolved")
	}
	if len(ws.calls) != 0 {
		t.Fatalf("destroy calls = %v, want none", ws.calls)
	}
	if st.sessions["mer-1"].IsTerminated {
		t.Fatal("session must remain active when workspace rows cannot be resolved")
	}
}

func TestKill_WorkspaceProjectDirtyRowRefusesRemoval(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	ws.destroyErr = fmt.Errorf("dirty: %w", ports.ErrWorkspaceDirty)
	seedWorkspaceProjectSession(st)

	freed, err := m.Kill(ctx, "mer-1")
	if err != nil || freed {
		t.Fatalf("freed=%v err=%v, want dirty row to preserve workspace", freed, err)
	}
	want := []string{"Destroy:api"}
	if got := ws.calls; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Fatal("session should be terminated even when dirty workspace cleanup is deferred")
	}
	if rows := st.worktrees["mer-1"]; len(rows) != 2 {
		t.Fatalf("restore/cleanup inventory rows = %d, want both kept for a later retry (#3408)", len(rows))
	}
}

// Same deferred outcome through the workspace-project path: a child repo whose
// removal is deferred must not fail the kill, and the leftover rows stay marked
// for a later cleanup pass to retry (#3408).
func TestKill_WorkspaceProjectDeferredRowDefersRemoval(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	ws.destroyErr = fmt.Errorf("gitworktree: force remove path %q: %w (deferred: %w)", "/ws/mer-1/api", ports.ErrWorkspaceDeferred, errors.New("access denied"))
	seedWorkspaceProjectSession(st)

	freed, err := m.Kill(ctx, "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if freed {
		t.Fatal("freed = true, want false: deferred rows preserve the workspace")
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Fatal("session should be terminated even when workspace removal is deferred")
	}
	want := []string{"Destroy:api", "Destroy:__root__"}
	if got := ws.calls; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}

func TestKill_RuntimeDestroyFailureLeavesSessionActive(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newManager()
	rt.destroyErr = errors.New("tmux transient")
	st.sessions["mer-1"] = mkLive("mer-1")

	freed, err := m.Kill(ctx, "mer-1")
	if err == nil || !strings.Contains(err.Error(), "runtime") {
		t.Fatalf("freed=%v err=%v, want runtime error", freed, err)
	}
	if freed {
		t.Fatal("workspace must not be reported freed when runtime destroy fails")
	}
	if st.sessions["mer-1"].IsTerminated {
		t.Fatal("session must remain active when runtime destroy fails")
	}
	if ws.destroyed != 0 {
		t.Fatalf("workspace destroy calls = %d, want 0 after runtime failure", ws.destroyed)
	}
}

// TestKill_TerminatesEvenWhenTeardownBudgetExpires is the third #5463 path,
// and the one that most likely produced the reported state: a slow teardown
// (a large worktree on NTFS) runs past killTeardownBudget, so the context that
// carried it is already dead by the time Kill records terminal intent. Every
// destructive step has happened at that point — the agent is gone, the worktree
// is gone — and refusing the one remaining write leaves a session that is dead
// everywhere except the row the UI reads, reachable by no path afterwards.
func TestKill_TerminatesEvenWhenTeardownBudgetExpires(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newManager()
	st.sessions["mer-1"] = mkLive("mer-1")
	// Smaller than the time the fake workspace burns below, so the budget is
	// already expired when Kill reaches its terminal-intent write.
	m.killTeardown = 20 * time.Millisecond
	ws.destroyHook = func() { time.Sleep(60 * time.Millisecond) }

	freed, err := m.Kill(ctx, "mer-1")
	if err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if !freed {
		t.Fatal("freed = false: the workspace was torn down")
	}
	if rt.destroyed != 1 || ws.destroyed != 1 {
		t.Fatalf("teardown incomplete: runtime=%d workspace=%d", rt.destroyed, ws.destroyed)
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Fatal("session must be marked terminated even though the teardown budget expired")
	}
}

// seedWorkspaceProjectSession stores a live workspace-project session "mer-1"
// with a root worktree and one child repo worktree.
func seedWorkspaceProjectSession(st *fakeStore) {
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Kind: domain.ProjectKindWorkspace, Config: testRoleAgents()}
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{Name: "api", RelativePath: "api"}}
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Metadata:  domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1", RuntimeHandleID: "h1"},
		Activity:  domain.Activity{State: domain.ActivityActive},
	}
	st.worktrees["mer-1"] = []domain.SessionWorktreeRecord{
		{SessionID: "mer-1", RepoName: domain.RootWorkspaceRepoName, Branch: "tp/mer-1", WorktreePath: "/ws/mer-1"},
		{SessionID: "mer-1", RepoName: "api", Branch: "tp/mer-1", WorktreePath: "/ws/mer-1/api"},
	}
}
