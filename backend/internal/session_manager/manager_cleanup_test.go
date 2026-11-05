package sessionmanager

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/adapters/workspace/gitworktree"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestCleanup_ReclaimsTerminalWorkspaces(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1"})
	st.sessions["mer-2"] = mkLive("mer-2")
	res, err := m.Cleanup(ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Cleaned) != 1 || res.Cleaned[0] != "mer-1" {
		t.Fatalf("got %v", res.Cleaned)
	}
	if len(res.Skipped) != 0 {
		t.Fatalf("skipped = %v, want none", res.Skipped)
	}
	if ws.destroyed != 1 {
		t.Fatal("live workspace must not be destroyed")
	}
}

// TestCleanup_SkipsWorkspaceStillReferencedByLiveSession: a terminated
// session's workspace must NOT be reclaimed while a live (non-terminated)
// session references the same path. Persistent/shared worktrees (the
// orchestrator's) are reused across respawn, so a terminated predecessor and
// a live successor can share one path — reclaiming it deletes the live
// session's cwd out from under it.
func TestCleanup_SkipsWorkspaceStillReferencedByLiveSession(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newManager()
	// Terminated predecessor and live successor share one persistent worktree.
	// The predecessor keeps its OWN runtime handle (independent of the shared
	// workspace and of the successor's handle).
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/shared", RuntimeHandleID: "mer-1-runtime"})
	live := mkLive("mer-2")
	live.Metadata.WorkspacePath = "/ws/shared"
	st.sessions["mer-2"] = live

	res, err := m.Cleanup(ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Cleaned) != 0 {
		t.Fatalf("cleaned = %v, want none (path still in use by live session)", res.Cleaned)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].SessionID != "mer-1" {
		t.Fatalf("skipped = %v, want mer-1", res.Skipped)
	}
	if res.Skipped[0].Reason != "workspace in use by a live session" {
		t.Fatalf("reason = %q", res.Skipped[0].Reason)
	}
	if ws.destroyed != 0 {
		t.Fatalf("destroyed = %d, want 0 — shared live workspace must not be torn down", ws.destroyed)
	}
	// The workspace is preserved, but the predecessor's own runtime must still be
	// reclaimed — keying the skip on the workspace path must not leak its runtime.
	if rt.destroyed != 1 || len(rt.destroyedIDs) != 1 || rt.destroyedIDs[0] != "mer-1-runtime" {
		t.Fatalf("runtime destroyed = %d ids=%v, want the skipped session's own handle torn down", rt.destroyed, rt.destroyedIDs)
	}
}

// TestCleanup_LiveWorkspaceGuardNormalizesPaths: the shared-path guard must
// compare canonicalized paths, so a trailing slash or "." segment on one
// record doesn't let a live session's worktree slip through as "not shared".
func TestCleanup_LiveWorkspaceGuardNormalizesPaths(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/shared/"})
	live := mkLive("mer-2")
	live.Metadata.WorkspacePath = "/ws/./shared"
	st.sessions["mer-2"] = live

	res, err := m.Cleanup(ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Cleaned) != 0 || len(res.Skipped) != 1 || res.Skipped[0].SessionID != "mer-1" {
		t.Fatalf("cleaned = %v, skipped = %v; want mer-1 skipped, none cleaned", res.Cleaned, res.Skipped)
	}
	if ws.destroyed != 0 {
		t.Fatalf("destroyed = %d, want 0", ws.destroyed)
	}
}

// TestCleanup_ReclaimsUnsharedWhileSkippingShared: a shared-path skip must not
// stop cleanup from reclaiming an adjacent terminated workspace that no live
// session references. The orchestrator's persistent worktree (shared across
// respawn) is the motivating case for the skip.
func TestCleanup_ReclaimsUnsharedWhileSkippingShared(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	// Terminated orchestrator predecessor shares its persistent worktree with
	// the live orchestrator successor.
	orchTerm := domain.SessionRecord{ID: "mer-orch-1", ProjectID: "mer", Kind: domain.KindOrchestrator, Metadata: domain.SessionMetadata{WorkspacePath: "/ws/orchestrator"}, IsTerminated: true, Activity: domain.Activity{State: domain.ActivityExited}}
	st.sessions["mer-orch-1"] = orchTerm
	orchLive := domain.SessionRecord{ID: "mer-orch-2", ProjectID: "mer", Kind: domain.KindOrchestrator, Metadata: domain.SessionMetadata{WorkspacePath: "/ws/orchestrator"}, Activity: domain.Activity{State: domain.ActivityActive}}
	st.sessions["mer-orch-2"] = orchLive
	// An unrelated terminated worker whose workspace nobody else uses.
	seedTerminal(st, "mer-3", domain.SessionMetadata{WorkspacePath: "/ws/mer-3"})

	res, err := m.Cleanup(ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Cleaned) != 1 || res.Cleaned[0] != "mer-3" {
		t.Fatalf("cleaned = %v, want [mer-3]", res.Cleaned)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].SessionID != "mer-orch-1" {
		t.Fatalf("skipped = %v, want mer-orch-1", res.Skipped)
	}
	if ws.destroyed != 1 {
		t.Fatalf("destroyed = %d, want 1 (only the unshared worker workspace)", ws.destroyed)
	}
}

// TestCleanup_InterleavedSpawnInOnDestroyPreservesSuccessorWorkspace proves that
// when an orchestrator successor spawns during Cleanup (for example via an
// onDestroy runtime teardown callback) and acquires the persistent worktree,
// Cleanup's final ownership check coordinates with Spawn under the workspace gate
// so the live successor's working directory is never deleted.
func TestCleanup_InterleavedSpawnInOnDestroyPreservesSuccessorWorkspace(t *testing.T) {
	t.Parallel()

	repo := newManagerGitRepo(t)
	gw, err := gitworktree.New(gitworktree.Options{
		ManagedRoot:  t.TempDir(),
		RepoResolver: gitworktree.StaticRepoResolver{"mer": repo},
	})
	if err != nil {
		t.Fatal(err)
	}
	st := newFakeStore()
	cfg := testRoleAgents()
	cfg.DefaultBranch = "main"
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: repo, Config: cfg}
	rt := &fakeRuntime{}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{
		Runtime:   rt,
		Agents:    fakeAgents{},
		Workspace: gw,
		Store:     st,
		Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: st},
		LookPath:  lookPath,
	})

	// Spawn the initial orchestrator session and confirm its persistent worktree exists.
	pred, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator})
	if err != nil {
		t.Fatalf("spawn predecessor: %v", err)
	}
	wsPath := pred.Metadata.WorkspacePath
	if wsPath == "" {
		t.Fatal("predecessor workspace path is empty")
	}
	readmePath := filepath.Join(wsPath, "README.md")
	if _, err := os.Stat(readmePath); err != nil {
		t.Fatalf("predecessor README does not exist before cleanup: %v", err)
	}

	// Mark predecessor terminated so Cleanup targets it.
	predRec := st.sessions[pred.ID]
	predRec.IsTerminated = true
	predRec.Activity = domain.Activity{State: domain.ActivityExited}
	st.sessions[pred.ID] = predRec

	// Configure onDestroy callback to spawn the successor during predecessor runtime teardown.
	var successorID domain.SessionID
	rt.onDestroy = func(call int, handle ports.RuntimeHandle) {
		succ, _, _, spawnErr := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator})
		if spawnErr != nil {
			t.Errorf("spawn successor in onDestroy: %v", spawnErr)
			return
		}
		successorID = succ.ID
	}

	res, err := m.Cleanup(ctx, "mer")
	if err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}
	if len(res.Cleaned) != 0 {
		t.Fatalf("cleaned = %v, want none (shared workspace acquired by live successor)", res.Cleaned)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].SessionID != pred.ID {
		t.Fatalf("skipped = %v, want [%s]", res.Skipped, pred.ID)
	}
	if res.Skipped[0].Reason != "workspace in use by a live session" {
		t.Fatalf("skip reason = %q, want %q", res.Skipped[0].Reason, "workspace in use by a live session")
	}
	if successorID == "" {
		t.Fatal("successor was not spawned")
	}
	succRec, ok := st.sessions[successorID]
	if !ok || succRec.IsTerminated {
		t.Fatalf("successor session %s is not live", successorID)
	}
	if _, err := os.Stat(readmePath); err != nil {
		t.Fatalf("successor README.md was removed from disk: %v", err)
	}
}

// TestCleanup_SeparatesAlreadyGoneWorkspacesFromReclaimedOnes keeps the
// reported count evidence rather than bookkeeping. A workspace whose directory
// was already missing tears down without error, so counting it as Cleaned
// claims disk that was never reclaimed and makes a batch run look like it did
// work it did not do.
func TestCleanup_SeparatesAlreadyGoneWorkspacesFromReclaimedOnes(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	ws.destroyReclaim = ports.WorkspaceReclaimAlreadyAbsent
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1"})

	res, err := m.Cleanup(ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}

	if len(res.Cleaned) != 0 {
		t.Fatalf("cleaned = %v, want none: nothing was reclaimed", res.Cleaned)
	}
	if len(res.AlreadyGone) != 1 || res.AlreadyGone[0] != "mer-1" {
		t.Fatalf("alreadyGone = %v, want [mer-1]", res.AlreadyGone)
	}
	if len(res.Skipped) != 0 {
		t.Fatalf("skipped = %v, want none: teardown did not fail", res.Skipped)
	}
	// Teardown still runs, so any stale registration is reconciled exactly as
	// before; only the accounting changed.
	if ws.destroyed != 1 {
		t.Fatalf("destroyed = %d, want 1", ws.destroyed)
	}
}

// TestCleanup_ClosesScopedShellTerminalsBeforeWorkspaceTeardown mirrors the
// Kill regression: Cleanup must also gate shut a session's scoped shell
// terminals before reclaiming its worktree, and release the gate afterward.
func TestCleanup_ClosesScopedShellTerminalsBeforeWorkspaceTeardown(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	var calls []string
	ws.sharedLog = &calls
	closer := &fakeShellTerminalCloser{sharedLog: &calls}
	m.SetShellTerminalCloser(closer)
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", WorkspaceRepoPath: "/ws/mer-1"})

	if _, err := m.Cleanup(ctx, "mer"); err != nil {
		t.Fatal(err)
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

// TestCleanup_SkipsWorkspaceReleaseWhenShellTerminalsWontClose is the
// regression for the bug where a shell terminal that failed to close was
// logged and forgotten, then the worktree reclaimed anyway. Cleanup must skip
// that session for this run (reporting it in Skipped) rather than reclaiming
// ground out from under a still-live shell — a later run can retry it.
func TestCleanup_SkipsWorkspaceReleaseWhenShellTerminalsWontClose(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	m.SetShellTerminalCloser(&fakeShellTerminalCloser{err: errors.New("shellterm-1: still alive")})
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1"})

	res, err := m.Cleanup(ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Cleaned) != 0 {
		t.Fatalf("cleaned = %v, want none: the shell close was not confirmed", res.Cleaned)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].SessionID != "mer-1" {
		t.Fatalf("skipped = %+v, want mer-1 reported", res.Skipped)
	}
	if ws.destroyed != 0 {
		t.Fatal("workspace must not be destroyed while a scoped shell is still alive")
	}
}

// TestCleanup_ReportsSkippedWorkspaces: a refused teardown must be visible in
// the result with a reason — a silent skip leaves users staring at
// "Would clean N … 0 sessions cleaned" with no explanation.
func TestCleanup_ReportsSkippedWorkspaces(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1"})
	ws.destroyErr = fmt.Errorf("gitworktree: refusing to remove: %w", ports.ErrWorkspaceDirty)
	res, err := m.Cleanup(ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Cleaned) != 0 {
		t.Fatalf("cleaned = %v, want none", res.Cleaned)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].SessionID != "mer-1" {
		t.Fatalf("skipped = %v, want mer-1", res.Skipped)
	}
	if res.Skipped[0].Reason != "workspace has uncommitted changes" {
		t.Fatalf("reason = %q", res.Skipped[0].Reason)
	}

	// A non-dirty teardown failure is reported too — but with a fixed public
	// reason: the raw cause carries internal filesystem paths and belongs in
	// the server log, not the API response.
	ws.destroyErr = errors.New("disk on fire")
	res, err = m.Cleanup(ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Reason != "workspace teardown failed" {
		t.Fatalf("skipped = %v, want fixed teardown-failed reason", res.Skipped)
	}
	if strings.Contains(res.Skipped[0].Reason, "disk on fire") {
		t.Fatalf("raw internal error leaked into public reason: %q", res.Skipped[0].Reason)
	}

	// A teardown that fails because the session's project is archived or
	// unregistered (its repo can no longer be resolved) is reported with a
	// distinct reason telling the user the worktree must be removed by hand.
	ws.destroyErr = fmt.Errorf("resolve project repo: %w", ErrProjectNotResolvable)
	res, err = m.Cleanup(ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].SessionID != "mer-1" {
		t.Fatalf("skipped = %v, want mer-1", res.Skipped)
	}
	if res.Skipped[0].Reason != "project is archived or unregistered — remove worktree manually" {
		t.Fatalf("reason = %q, want archived-project reason", res.Skipped[0].Reason)
	}
}

// A deferred removal is a skip, not a failure: the session stays terminated,
// the leftover directory is reported visibly so the user is not left staring at
// a silent "0 sessions cleaned", and the next cleanup run retries the unlink
// (#3408).
func TestCleanup_ReportsDeferredWorkspaceRemoval(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1"})
	ws.destroyErr = fmt.Errorf("gitworktree: remove unregistered path %q: %w (deferred: %w)", "/ws/mer-1", ports.ErrWorkspaceDeferred, errors.New("access denied"))
	res, err := m.Cleanup(ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Cleaned) != 0 {
		t.Fatalf("cleaned = %v, want none", res.Cleaned)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].SessionID != "mer-1" {
		t.Fatalf("skipped = %v, want mer-1", res.Skipped)
	}
	if res.Skipped[0].Reason != "worktree is in use; will retry on a later run" {
		t.Fatalf("reason = %q", res.Skipped[0].Reason)
	}
}

// TestSpawnTeardown_WorkspaceRepoPathRoundTrip pins the WorkspaceRepoPath round
// trip that lets teardown reclaim a worktree without re-resolving the project
// repo. The workspace layer needs the repo path (the canonical repo a worktree
// hangs off of) to tear a worktree down; persisting it on spawn means teardown
// reads it straight from metadata instead of re-deriving it from the project,
// which fails for archived/unregistered projects (#2608).
//
// Two independent assertions bite the two halves of the plumbing:
//   - WRITE side (manager.go Spawn metadata literal): the spawned session's
//     stored metadata must carry the workspace's RepoPath. Dropping
//     `WorkspaceRepoPath: ws.RepoPath` there leaves it empty and fails here.
//   - READ side (manager.go workspaceInfo): teardown must feed that persisted
//     path back into workspace.Destroy. Dropping `RepoPath:
//     rec.Metadata.WorkspaceRepoPath` there makes teardown pass an empty repo
//     path (a real destroyer would then have to re-resolve the project) and
//     fails here.
//
// The prior suite injected teardown failures by stubbing ws.destroyErr directly,
// so it never exercised this path and left both mutations green.
func TestSpawnTeardown_WorkspaceRepoPathRoundTrip(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	const repoPath = "/repos/mer/canonical"
	// Production Create resolves and returns the canonical repo path; mirror that
	// so the value is available to be persisted and later reused.
	ws.createRepoPath = repoPath

	rec, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode})
	if err != nil {
		t.Fatal(err)
	}

	// WRITE side: spawn must persist the workspace repo path into stored metadata.
	stored, ok, err := st.GetSession(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if !ok {
		t.Fatalf("session %s not found after spawn", rec.ID)
	}
	if stored.Metadata.WorkspaceRepoPath != repoPath {
		t.Fatalf("persisted WorkspaceRepoPath = %q, want %q (write side: manager.go Spawn must set WorkspaceRepoPath: ws.RepoPath)", stored.Metadata.WorkspaceRepoPath, repoPath)
	}

	// READ side: teardown must feed the persisted repo path into the destroyer
	// rather than leaving it empty for a project re-resolution.
	if _, err := m.Kill(ctx, rec.ID); err != nil {
		t.Fatal(err)
	}
	if ws.destroyed == 0 {
		t.Fatal("expected workspace teardown to destroy the worktree")
	}
	if ws.lastDestroyInfo.RepoPath != repoPath {
		t.Fatalf("teardown RepoPath = %q, want persisted %q (read side: manager.go workspaceInfo must set RepoPath from metadata); empty means teardown fell back to re-resolving the project repo", ws.lastDestroyInfo.RepoPath, repoPath)
	}
}

func TestCleanup_WorkspaceProjectDestroysChildrenBeforeRoot(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Kind: domain.ProjectKindWorkspace, Config: testRoleAgents()}
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{Name: "api", RelativePath: "api"}}
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1"})
	st.worktrees["mer-1"] = []domain.SessionWorktreeRecord{
		{SessionID: "mer-1", RepoName: domain.RootWorkspaceRepoName, Branch: "tp/mer-1", WorktreePath: "/ws/mer-1"},
		{SessionID: "mer-1", RepoName: "api", Branch: "tp/mer-1", WorktreePath: "/ws/mer-1/api"},
	}

	res, err := m.Cleanup(ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Cleaned) != 1 || res.Cleaned[0] != "mer-1" {
		t.Fatalf("cleaned = %v, want mer-1", res.Cleaned)
	}
	want := []string{"Destroy:api", "Destroy:__root__"}
	if got := ws.calls; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("destroy order = %v, want %v", got, want)
	}
}

// TestCleanup_WorkspaceProjectRepeatRunSkipsReleasedWorkspace is the
// multi-repo half of the false-count fix. A workspace project's teardown
// succeeds on every later run — the worktrees are gone, so there is nothing
// left to fail on — and counting those runs as reclaimed is exactly the report
// that made a batch cleanup look like it freed disk it never touched. The first
// run records the release, so the second one does not revisit the session.
func TestCleanup_WorkspaceProjectRepeatRunSkipsReleasedWorkspace(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Kind: domain.ProjectKindWorkspace, Config: testRoleAgents()}
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{Name: "api", RelativePath: "api"}}
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1"})
	st.worktrees["mer-1"] = []domain.SessionWorktreeRecord{
		{SessionID: "mer-1", RepoName: domain.RootWorkspaceRepoName, Branch: "tp/mer-1", WorktreePath: "/ws/mer-1"},
		{SessionID: "mer-1", RepoName: "api", Branch: "tp/mer-1", WorktreePath: "/ws/mer-1/api"},
	}

	first, err := m.Cleanup(ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Cleaned) != 1 || first.Cleaned[0] != "mer-1" {
		t.Fatalf("first run cleaned = %v, want [mer-1]: both worktrees were present", first.Cleaned)
	}
	if len(first.AlreadyGone) != 0 {
		t.Fatalf("first run alreadyGone = %v, want none", first.AlreadyGone)
	}

	destroyed := ws.destroyed

	second, err := m.Cleanup(ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Cleaned)+len(second.AlreadyGone)+len(second.Skipped) != 0 {
		t.Fatalf("second run = %+v, want nothing: the first run released the workspace", second)
	}
	if ws.destroyed != destroyed {
		t.Fatalf("destroyed = %d, want %d: the released workspace was not revisited", ws.destroyed, destroyed)
	}
}

// TestCleanup_WorkspaceProjectPartialReclaimCountsAsCleaned pins the aggregate
// on the other side. Repos of one workspace project can disagree — a child
// removed by hand, the root still there — and a session that released any disk
// at all was reclaimed, so only a project where nothing was left may report
// already gone.
func TestCleanup_WorkspaceProjectPartialReclaimCountsAsCleaned(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Kind: domain.ProjectKindWorkspace, Config: testRoleAgents()}
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{Name: "api", RelativePath: "api"}}
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1"})
	st.worktrees["mer-1"] = []domain.SessionWorktreeRecord{
		{SessionID: "mer-1", RepoName: domain.RootWorkspaceRepoName, Branch: "tp/mer-1", WorktreePath: "/ws/mer-1"},
		{SessionID: "mer-1", RepoName: "api", Branch: "tp/mer-1", WorktreePath: "/ws/mer-1/api"},
	}
	ws.destroyReclaim = ports.WorkspaceReclaimAlreadyAbsent
	ws.destroyReclaimByPath = map[string]ports.WorkspaceReclaim{"/ws/mer-1": ports.WorkspaceReclaimRemoved}

	res, err := m.Cleanup(ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Cleaned) != 1 || res.Cleaned[0] != "mer-1" {
		t.Fatalf("cleaned = %v, want [mer-1]: the root worktree was reclaimed", res.Cleaned)
	}
	if len(res.AlreadyGone) != 0 {
		t.Fatalf("alreadyGone = %v, want none", res.AlreadyGone)
	}
}

func TestCleanup_WorkspaceProjectMarksRetryRemoveAfterTeardownFailure(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	ws.destroyErr = errors.New("locked")
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Kind: domain.ProjectKindWorkspace, Config: testRoleAgents()}
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{Name: "api", RelativePath: "api"}}
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1"})
	st.worktrees["mer-1"] = []domain.SessionWorktreeRecord{
		{SessionID: "mer-1", RepoName: domain.RootWorkspaceRepoName, Branch: "tp/mer-1", WorktreePath: "/ws/mer-1"},
		{SessionID: "mer-1", RepoName: "api", Branch: "tp/mer-1", WorktreePath: "/ws/mer-1/api"},
	}

	res, err := m.Cleanup(ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Cleaned) != 0 || len(res.Skipped) != 1 {
		t.Fatalf("cleanup result = %+v, want one skipped session", res)
	}
	states := map[string]string{}
	for _, row := range st.worktrees["mer-1"] {
		states[row.RepoName] = row.State
	}
	if states["api"] != "retry_remove" || states[domain.RootWorkspaceRepoName] != "retry_remove" {
		t.Fatalf("states = %v, want retry_remove rows", states)
	}
}

func TestCleanup_WorkspaceProjectDirtyRowsAreSkipped(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	ws.destroyErr = fmt.Errorf("dirty: %w", ports.ErrWorkspaceDirty)
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Kind: domain.ProjectKindWorkspace, Config: testRoleAgents()}
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{Name: "api", RelativePath: "api"}}
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1"})
	st.worktrees["mer-1"] = []domain.SessionWorktreeRecord{
		{SessionID: "mer-1", RepoName: domain.RootWorkspaceRepoName, Branch: "tp/mer-1", WorktreePath: "/ws/mer-1"},
		{SessionID: "mer-1", RepoName: "api", Branch: "tp/mer-1", WorktreePath: "/ws/mer-1/api"},
	}

	res, err := m.Cleanup(ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skipped) != 1 {
		t.Fatalf("cleanup result = %+v, want one skipped session", res)
	}
	refs := map[string]string{}
	states := map[string]string{}
	for _, row := range st.worktrees["mer-1"] {
		refs[row.RepoName] = row.PreservedRef
		states[row.RepoName] = row.State
	}
	if states["api"] != "" || refs["api"] != "" {
		t.Fatalf("api state/ref = %q/%q, want unchanged dirty row", states["api"], refs["api"])
	}
}
