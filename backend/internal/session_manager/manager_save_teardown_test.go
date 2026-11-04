package sessionmanager

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// TestSaveAndTeardownAll_CaptureOrderAndMarker verifies (a): for a live session
// with a workspace, SaveAndTeardownAll must call StashUncommitted BEFORE
// UpsertSessionWorktree (writing preserved_ref) BEFORE ForceDestroy.
func TestSaveAndTeardownAll_CaptureOrderAndMarker(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newLifecycleManager()

	// Wire a shared ordered call log so we can assert cross-fake ordering:
	// both fakeStore and fakeWorkspace append to the same slice.
	var sharedLog []string
	st.sharedLog = &sharedLog
	ws.sharedLog = &sharedLog

	// A live session with a workspace path and runtime handle.
	ws.stashRef = "refs/ao/preserved/mer-1"
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		Metadata:  domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1/root", RuntimeHandleID: "h1"},
		Activity:  domain.Activity{State: domain.ActivityActive},
	}

	if err := m.SaveAndTeardownAll(ctx); err != nil {
		t.Fatalf("SaveAndTeardownAll err = %v", err)
	}

	// Stash must come before ForceDestroy in the call log.
	stashIdx, forceIdx := -1, -1
	for i, c := range ws.calls {
		if c == "StashUncommitted:mer-1" {
			stashIdx = i
		}
		if c == "ForceDestroy:mer-1" {
			forceIdx = i
		}
	}
	if stashIdx == -1 {
		t.Fatal("StashUncommitted was not called")
	}
	if forceIdx == -1 {
		t.Fatal("ForceDestroy was not called")
	}
	if stashIdx >= forceIdx {
		t.Fatalf("StashUncommitted (call %d) must come before ForceDestroy (call %d)", stashIdx, forceIdx)
	}

	// UpsertSessionWorktree (DB write) must be committed BEFORE ForceDestroy.
	// Use the shared ordered log to compare positions across the store and workspace.
	upsertIdx, sharedForceIdx := -1, -1
	for i, c := range sharedLog {
		if c == "UpsertSessionWorktree:mer-1" {
			upsertIdx = i
		}
		if c == "ForceDestroy:mer-1" {
			sharedForceIdx = i
		}
	}
	if upsertIdx == -1 {
		t.Fatal("UpsertSessionWorktree was not called")
	}
	if sharedForceIdx == -1 {
		t.Fatal("ForceDestroy was not recorded in shared log")
	}
	if upsertIdx >= sharedForceIdx {
		t.Fatalf("UpsertSessionWorktree (pos %d) must come before ForceDestroy (pos %d) in shared call log %v", upsertIdx, sharedForceIdx, sharedLog)
	}

	// DB write (UpsertSessionWorktree) must have recorded the correct row.
	rows := st.worktrees["mer-1"]
	if len(rows) == 0 {
		t.Fatal("UpsertSessionWorktree was not called: no worktree row for mer-1")
	}
	if rows[0].PreservedRef != "refs/ao/preserved/mer-1" {
		t.Fatalf("preserved_ref = %q, want refs/ao/preserved/mer-1", rows[0].PreservedRef)
	}

	// The session must be marked terminated.
	if !st.sessions["mer-1"].IsTerminated {
		t.Fatal("session must be terminated after SaveAndTeardownAll")
	}
}

func TestSaveAndTeardownOne_NativeTerminationFailurePreservesWorkspace(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newLifecycleManager()
	agent := &nativeTerminatingAgent{wantID: "native-7", err: errors.New("prime stop failed")}
	m.agents = singleAgent{agent: agent}
	rec := domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
		Metadata: domain.SessionMetadata{
			WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1/root",
			RuntimeHandleID: "h1", AgentSessionID: "native-7",
		},
		Activity: domain.Activity{State: domain.ActivityActive},
	}
	st.sessions[rec.ID] = rec

	err := m.saveAndTeardownOne(ctx, rec)
	if err == nil || !strings.Contains(err.Error(), "prime stop failed") {
		t.Fatalf("err=%v, want native termination error", err)
	}
	if rt.destroyed != 0 || st.sessions[rec.ID].IsTerminated {
		t.Fatalf("runtime=%d terminated=%v", rt.destroyed, st.sessions[rec.ID].IsTerminated)
	}
	for _, call := range ws.calls {
		if strings.HasPrefix(call, "ForceDestroy:") {
			t.Fatalf("worktree must remain after native termination failure: calls=%v", ws.calls)
		}
	}
}

func TestSaveAndTeardownOne_UnknownHarnessSkipsNativeTerminationAndTearsDown(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newLifecycleManager()
	m.agents = missingAgents{}
	rec := domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: "retired-agent",
		Metadata: domain.SessionMetadata{
			WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1/root",
			RuntimeHandleID: "h1", AgentSessionID: "native-7",
		},
		Activity: domain.Activity{State: domain.ActivityActive},
	}
	st.sessions[rec.ID] = rec

	if err := m.saveAndTeardownOne(ctx, rec); err != nil {
		t.Fatalf("saveAndTeardownOne err = %v", err)
	}
	if rt.destroyed != 1 || !st.sessions[rec.ID].IsTerminated {
		t.Fatalf("runtime=%d terminated=%v, want successful teardown", rt.destroyed, st.sessions[rec.ID].IsTerminated)
	}
	foundForceDestroy := false
	for _, call := range ws.calls {
		if call == "ForceDestroy:mer-1" {
			foundForceDestroy = true
		}
	}
	if !foundForceDestroy {
		t.Fatalf("worktree must be force-destroyed when only native adapter lookup is missing: calls=%v", ws.calls)
	}
}

func TestSaveAndTeardownOne_WorkspaceProjectNativeTerminationFailurePreservesRepos(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newLifecycleManager()
	agent := &nativeTerminatingAgent{wantID: "native-7", err: errors.New("prime stop failed")}
	m.agents = singleAgent{agent: agent}
	ws.stashRef = "refs/ao/preserved/mer-1"
	rec := seedNativeWorkspaceProject(st, "mer-1", domain.KindWorker)

	err := m.saveAndTeardownOne(ctx, rec)
	if err == nil || !strings.Contains(err.Error(), "prime stop failed") {
		t.Fatalf("err=%v, want native termination error", err)
	}
	if rt.destroyed != 0 || st.sessions[rec.ID].IsTerminated {
		t.Fatalf("runtime=%d terminated=%v", rt.destroyed, st.sessions[rec.ID].IsTerminated)
	}
	if rows := st.worktrees[rec.ID]; len(rows) != 2 {
		t.Fatalf("workspace repo inventory = %#v, want both rows retained", rows)
	}
	for _, call := range ws.calls {
		if strings.HasPrefix(call, "ForceDestroy:") {
			t.Fatalf("worktrees must remain after native termination failure: calls=%v", ws.calls)
		}
	}
}

func TestSaveAndTeardownOne_WorkspaceProjectTerminatesNativeSessionOnce(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newLifecycleManager()
	agent := &nativeTerminatingAgent{wantID: "native-7"}
	m.agents = singleAgent{agent: agent}
	rec := seedNativeWorkspaceProject(st, "mer-1", domain.KindWorker)

	if err := m.saveAndTeardownOne(ctx, rec); err != nil {
		t.Fatalf("saveAndTeardownOne err = %v", err)
	}
	if agent.calls != 1 {
		t.Fatalf("native termination calls = %d, want 1", agent.calls)
	}
}

// TestSaveAndTeardownAll_ClosesScopedShellTerminalsBeforeForceDestroy covers
// the last coverage gap the second review round flagged: the shutdown
// save-and-teardown path (also reached by reconcileLive on boot, via the same
// saveAndTeardownOne) force-removes a worktree just like Kill/Cleanup, and
// must gate shut any shell terminal scoped to the session first.
func TestSaveAndTeardownAll_ClosesScopedShellTerminalsBeforeForceDestroy(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newLifecycleManager()
	var sharedLog []string
	st.sharedLog = &sharedLog
	ws.sharedLog = &sharedLog
	closer := &fakeShellTerminalCloser{sharedLog: &sharedLog}
	m.SetShellTerminalCloser(closer)
	ws.stashRef = "refs/ao/preserved/mer-1"
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		Metadata:  domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1/root", RuntimeHandleID: "h1"},
		Activity:  domain.Activity{State: domain.ActivityActive},
	}

	if err := m.SaveAndTeardownAll(ctx); err != nil {
		t.Fatalf("SaveAndTeardownAll err = %v", err)
	}

	if len(closer.began) != 1 || closer.began[0] != "mer-1" {
		t.Fatalf("began = %v, want [mer-1]", closer.began)
	}
	if len(closer.ended) != 1 || closer.ended[0] != "mer-1" {
		t.Fatalf("ended = %v, want [mer-1]", closer.ended)
	}
	beginIdx, forceIdx, endIdx := -1, -1, -1
	for i, c := range sharedLog {
		switch c {
		case "BeginSessionTeardown:mer-1":
			beginIdx = i
		case "ForceDestroy:mer-1":
			forceIdx = i
		case "EndSessionTeardown:mer-1":
			endIdx = i
		}
	}
	if beginIdx == -1 || forceIdx == -1 || endIdx == -1 {
		t.Fatalf("call log missing expected entries: %v", sharedLog)
	}
	if beginIdx >= forceIdx || forceIdx >= endIdx {
		t.Fatalf("call order = %v, want begin, then force destroy, then end", sharedLog)
	}
}

func TestSaveAndTeardownAll_QuiescesNativeAgentBeforeCapturingWork(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newLifecycleManager()
	var sharedLog []string
	ws.sharedLog = &sharedLog
	native := &nativeTerminatingAgent{wantID: "native-7", sharedLog: &sharedLog}
	m.agents = singleAgent{agent: native}
	ws.stashRef = "refs/ao/preserved/mer-1"
	lateWriteObservedAtCapture := false
	ws.stashHook = func() { lateWriteObservedAtCapture = native.calls == 1 }
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1", AgentSessionID: "native-7"},
	}
	if err := m.SaveAndTeardownAll(ctx); err != nil {
		t.Fatalf("SaveAndTeardownAll err = %v", err)
	}
	if native.calls != 1 || !lateWriteObservedAtCapture || len(sharedLog) < 2 || sharedLog[0] != "TerminateNativeSession" || sharedLog[1] != "StashUncommitted:mer-1" {
		t.Fatalf("native calls=%d shared log=%v; native termination must complete before capture", native.calls, sharedLog)
	}
}

func TestSaveAndTeardownAll_TeardownsReviewerTerminalWithoutTerminate(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newLifecycleManager()
	var sharedLog []string
	st.sharedLog = &sharedLog
	ws.sharedLog = &sharedLog
	reviewer := &fakeReviewerTerminator{sharedLog: &sharedLog}
	m.SetReviewerTerminator(reviewer)
	ws.stashRef = "refs/ao/preserved/mer-1"
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		Metadata:  domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1/root", RuntimeHandleID: "h1"},
		Activity:  domain.Activity{State: domain.ActivityActive},
	}

	if err := m.SaveAndTeardownAll(ctx); err != nil {
		t.Fatalf("SaveAndTeardownAll err = %v", err)
	}
	if len(reviewer.calls) != 0 {
		t.Fatalf("reviewer terminates = %v, want none for shutdown save/teardown", reviewer.calls)
	}
	if len(reviewer.teardownCalls) != 1 || reviewer.teardownCalls[0] != "mer-1" {
		t.Fatalf("reviewer teardowns = %v, want [mer-1]", reviewer.teardownCalls)
	}
	teardownIdx, forceIdx := -1, -1
	for i, c := range sharedLog {
		switch c {
		case "TeardownReviewerTerminal:mer-1":
			teardownIdx = i
		case "ForceDestroy:mer-1":
			forceIdx = i
		}
	}
	if teardownIdx == -1 || forceIdx == -1 {
		t.Fatalf("call log missing expected entries: %v", sharedLog)
	}
	if teardownIdx >= forceIdx {
		t.Fatalf("call order = %v, want reviewer teardown before force destroy", sharedLog)
	}
}

func TestSaveAndTeardownAllThenRestoreAll_TeardownsAndRestoresReviewerTerminal(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newLifecycleManager()
	reviewer := &fakeReviewerTerminator{}
	m.SetReviewerTerminator(reviewer)
	ws.stashRef = "refs/ao/preserved/mer-1"
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{
			WorkspacePath:   "/ws/mer-1",
			Branch:          "tp/mer-1/root",
			RuntimeHandleID: "h1",
			AgentSessionID:  "agent-w",
		},
		Activity: domain.Activity{State: domain.ActivityActive},
	}

	if err := m.SaveAndTeardownAll(ctx); err != nil {
		t.Fatalf("SaveAndTeardownAll err = %v", err)
	}
	if len(reviewer.teardownCalls) != 1 || reviewer.teardownCalls[0] != "mer-1" {
		t.Fatalf("reviewer teardowns = %v, want [mer-1]", reviewer.teardownCalls)
	}
	if err := m.RestoreAll(ctx); err != nil {
		t.Fatalf("RestoreAll err = %v", err)
	}
	if rt.created != 1 {
		t.Fatalf("RestoreAll runtime creates = %d, want 1", rt.created)
	}
	if len(reviewer.restoreCalls) != 1 || reviewer.restoreCalls[0] != "mer-1" {
		t.Fatalf("reviewer restores = %v, want [mer-1]", reviewer.restoreCalls)
	}
	if st.sessions["mer-1"].IsTerminated {
		t.Fatal("session must be live after RestoreAll")
	}
}

func TestSaveAndTeardownAllThenRestoreAll_PreservesIgnoredAttachments(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	workspacePath := filepath.Join(t.TempDir(), "mer-1")
	attachmentDir := filepath.Join(workspacePath, filepath.FromSlash(attachmentsDir))
	if err := os.MkdirAll(attachmentDir, 0o750); err != nil {
		t.Fatal(err)
	}
	want := map[string][]byte{
		"attachment-before-restart.png": []byte("image-bytes-before-restart"),
		"attachment-before-restart.txt": []byte("text-bytes-before-restart"),
	}
	for name, data := range want {
		if err := os.WriteFile(filepath.Join(attachmentDir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rt := &fakeRuntime{}
	ws := &fakeWorkspace{path: workspacePath}
	m := New(Deps{
		Runtime: rt, Agents: fakeAgents{}, Workspace: ws, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		DataDir: dataDir, LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{
			WorkspacePath:   workspacePath,
			Branch:          "tp/mer-1/root",
			RuntimeHandleID: "h1",
			AgentSessionID:  "agent-w",
		},
		Activity: domain.Activity{State: domain.ActivityActive},
	}

	if err := m.SaveAndTeardownAll(ctx); err != nil {
		t.Fatalf("SaveAndTeardownAll: %v", err)
	}
	// The fake records ForceDestroy without touching disk. Reproduce the real
	// adapter's removal, then provide the empty worktree returned by Restore.
	if err := os.RemoveAll(workspacePath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspacePath, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := m.RestoreAll(ctx); err != nil {
		t.Fatalf("RestoreAll: %v", err)
	}

	for name, wantData := range want {
		got, err := os.ReadFile(filepath.Join(attachmentDir, name))
		if err != nil {
			t.Fatalf("read %s after restore: %v", name, err)
		}
		if string(got) != string(wantData) {
			t.Fatalf("%s after restore = %q, want %q", name, got, wantData)
		}
	}
	if wantPattern := "/" + attachmentsDir + "/"; !reflect.DeepEqual(ws.excludePatterns, []string{wantPattern}) {
		t.Fatalf("restore exclude patterns = %v, want [%s]", ws.excludePatterns, wantPattern)
	}
}

func TestSaveAndTeardownAllDoesNotDestroyWorkspaceWhenAttachmentImportIsUnsafe(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dataDir, "attachments")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	workspacePath := filepath.Join(t.TempDir(), "mer-1")
	attachmentPath := filepath.Join(workspacePath, filepath.FromSlash(attachmentsDir), "attachment-before-restart.png")
	if err := os.MkdirAll(filepath.Dir(attachmentPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(attachmentPath, []byte("must survive"), 0o600); err != nil {
		t.Fatal(err)
	}

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rt := &fakeRuntime{}
	ws := &fakeWorkspace{path: workspacePath}
	m := New(Deps{
		Runtime: rt, Agents: fakeAgents{}, Workspace: ws, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		DataDir: dataDir, LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{WorkspacePath: workspacePath, Branch: "tp/mer-1/root", RuntimeHandleID: "h1", AgentSessionID: "agent-w"},
		Activity: domain.Activity{State: domain.ActivityActive},
	}

	if err := m.SaveAndTeardownAll(ctx); err != nil {
		t.Fatalf("SaveAndTeardownAll: %v", err)
	}
	if ws.stashCalls != 0 || rt.destroyed != 0 {
		t.Fatalf("unsafe import still tore down session: stash=%d runtimeDestroy=%d", ws.stashCalls, rt.destroyed)
	}
	if got, err := os.ReadFile(attachmentPath); err != nil || string(got) != "must survive" {
		t.Fatalf("live workspace attachment = %q, %v; want preserved", got, err)
	}
	if _, err := os.Stat(filepath.Join(outside, "mer-1", "attachment-before-restart.png")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsafe import wrote outside Tallpine data: %v", err)
	}
}

func TestSaveAndTeardownAll_SkipsScratchSessions(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newLifecycleManager()
	st.projects["scratch"] = domain.ProjectRecord{ID: "scratch", Kind: domain.ProjectKindScratch, Config: testRoleAgents()}
	st.sessions["scratch-1"] = domain.SessionRecord{
		ID:        "scratch-1",
		ProjectID: "scratch",
		Kind:      domain.KindWorker,
		Metadata:  domain.SessionMetadata{WorkspacePath: "/ws/scratch-1", RuntimeHandleID: "h1"},
		Activity:  domain.Activity{State: domain.ActivityActive},
	}

	if err := m.SaveAndTeardownAll(ctx); err != nil {
		t.Fatalf("SaveAndTeardownAll err = %v", err)
	}
	if st.sessions["scratch-1"].IsTerminated {
		t.Fatal("scratch session must stay live during graceful shutdown")
	}
	if ws.stashCalls != 0 || len(ws.calls) != 0 {
		t.Fatalf("scratch shutdown must not stash or force destroy, calls=%v stash=%d", ws.calls, ws.stashCalls)
	}
	if rt.destroyed != 0 {
		t.Fatalf("runtime Destroy calls = %d, want 0", rt.destroyed)
	}
	if rows := st.worktrees["scratch-1"]; len(rows) != 0 {
		t.Fatalf("scratch shutdown must not write restore markers, got %#v", rows)
	}
}

func TestRetireForReplacementCapturesAndReleasesWorkspace(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newLifecycleManager()
	browser := &fakeBrowserLifecycle{}
	m.browser = browser
	var sharedLog []string
	st.sharedLog = &sharedLog
	ws.sharedLog = &sharedLog
	ws.stashRef = "refs/ao/preserved/mer-orch"
	st.sessions["mer-orch"] = domain.SessionRecord{
		ID:        "mer-orch",
		ProjectID: "mer",
		Kind:      domain.KindOrchestrator,
		Metadata:  domain.SessionMetadata{WorkspacePath: "/ws/mer-orch", Branch: "tp/mer-orchestrator", RuntimeHandleID: "orch-handle"},
		Activity:  domain.Activity{State: domain.ActivityActive},
	}
	st.worktrees["mer-orch"] = []domain.SessionWorktreeRecord{{
		SessionID:    "mer-orch",
		RepoName:     domain.RootWorkspaceRepoName,
		Branch:       "tp/mer-orchestrator",
		WorktreePath: "/ws/mer-orch",
		PreservedRef: "refs/ao/preserved/old",
	}}

	if err := m.RetireForReplacement(ctx, "mer-orch"); err != nil {
		t.Fatalf("RetireForReplacement err = %v", err)
	}

	if rows := st.worktrees["mer-orch"]; len(rows) != 0 {
		t.Fatalf("replacement retirement must not write restore markers, got %#v", rows)
	}
	if !st.sessions["mer-orch"].IsTerminated {
		t.Fatal("retired orchestrator must be marked terminated")
	}
	if rt.destroyed != 1 || rt.destroyedIDs[0] != "orch-handle" {
		t.Fatalf("runtime destroyed = %d ids=%v, want orch-handle", rt.destroyed, rt.destroyedIDs)
	}

	stashIdx, deleteIdx, forceIdx := -1, -1, -1
	for i, c := range sharedLog {
		switch c {
		case "StashUncommitted:mer-orch":
			stashIdx = i
		case "DeleteSessionWorktrees:mer-orch":
			deleteIdx = i
		case "ForceDestroy:mer-orch":
			forceIdx = i
		}
	}
	if stashIdx == -1 || deleteIdx == -1 || forceIdx == -1 {
		t.Fatalf("missing expected calls in shared log: %v", sharedLog)
	}
	if stashIdx >= deleteIdx || deleteIdx >= forceIdx {
		t.Fatalf("replacement retire must capture and clear restore intent before workspace release; log=%v", sharedLog)
	}
	if len(browser.destroyed) != 1 || browser.destroyed[0] != "mer-orch" {
		t.Fatalf("browser targets destroyed = %v, want mer-orch", browser.destroyed)
	}
}

func TestRetireForReplacement_NativeTerminationFailurePreservesRuntimeAndWorkspace(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newLifecycleManager()
	agent := &nativeTerminatingAgent{wantID: "native-7", err: errors.New("prime stop failed")}
	m.agents = singleAgent{agent: agent}
	ws.stashRef = "refs/ao/preserved/mer-orch"
	rec := domain.SessionRecord{
		ID: "mer-orch", ProjectID: "mer", Kind: domain.KindOrchestrator,
		Metadata: domain.SessionMetadata{
			WorkspacePath: "/ws/mer-orch", Branch: "tp/mer-orchestrator",
			RuntimeHandleID: "orch-handle", AgentSessionID: "native-7",
		},
		Activity: domain.Activity{State: domain.ActivityActive},
	}
	st.sessions[rec.ID] = rec

	err := m.RetireForReplacement(ctx, rec.ID)
	if err == nil || !strings.Contains(err.Error(), "prime stop failed") {
		t.Fatalf("err=%v, want native termination error", err)
	}
	if rt.destroyed != 0 || st.sessions[rec.ID].IsTerminated {
		t.Fatalf("runtime=%d terminated=%v", rt.destroyed, st.sessions[rec.ID].IsTerminated)
	}
	for _, call := range ws.calls {
		if strings.HasPrefix(call, "ForceDestroy:") {
			t.Fatalf("worktree must remain after native termination failure: calls=%v", ws.calls)
		}
	}
}

func TestRetireForReplacement_WorkspaceProjectNativeTerminationFailurePreservesRepos(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newLifecycleManager()
	agent := &nativeTerminatingAgent{wantID: "native-7", err: errors.New("prime stop failed")}
	m.agents = singleAgent{agent: agent}
	ws.stashRef = "refs/ao/preserved/mer-orch"
	rec := seedNativeWorkspaceProject(st, "mer-orch", domain.KindOrchestrator)

	err := m.RetireForReplacement(ctx, rec.ID)
	if err == nil || !strings.Contains(err.Error(), "prime stop failed") {
		t.Fatalf("err=%v, want native termination error", err)
	}
	if rt.destroyed != 0 || st.sessions[rec.ID].IsTerminated {
		t.Fatalf("runtime=%d terminated=%v", rt.destroyed, st.sessions[rec.ID].IsTerminated)
	}
	if rows := st.worktrees[rec.ID]; len(rows) != 2 {
		t.Fatalf("workspace repo inventory = %#v, want both rows retained", rows)
	}
	for _, call := range ws.calls {
		if strings.HasPrefix(call, "ForceDestroy:") {
			t.Fatalf("worktrees must remain after native termination failure: calls=%v", ws.calls)
		}
	}
}

func TestRetireForReplacement_WorkspaceProjectTerminatesNativeSessionOnce(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newLifecycleManager()
	agent := &nativeTerminatingAgent{wantID: "native-7"}
	m.agents = singleAgent{agent: agent}
	rec := seedNativeWorkspaceProject(st, "mer-orch", domain.KindOrchestrator)

	if err := m.RetireForReplacement(ctx, rec.ID); err != nil {
		t.Fatalf("RetireForReplacement err = %v", err)
	}
	if agent.calls != 1 {
		t.Fatalf("native termination calls = %d, want 1", agent.calls)
	}
}

// TestRetireForReplacementClosesScopedShellTerminalsBeforeForceDestroy covers
// the coverage gap the second review round flagged: RetireForReplacement
// force-removes a worktree the same as Kill/Cleanup, and must gate shut any
// shell terminal scoped to the retiring orchestrator first.
func TestRetireForReplacementClosesScopedShellTerminalsBeforeForceDestroy(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newLifecycleManager()
	var sharedLog []string
	st.sharedLog = &sharedLog
	ws.sharedLog = &sharedLog
	closer := &fakeShellTerminalCloser{sharedLog: &sharedLog}
	m.SetShellTerminalCloser(closer)
	ws.stashRef = "refs/ao/preserved/mer-orch"
	st.sessions["mer-orch"] = domain.SessionRecord{
		ID:        "mer-orch",
		ProjectID: "mer",
		Kind:      domain.KindOrchestrator,
		Metadata:  domain.SessionMetadata{WorkspacePath: "/ws/mer-orch", Branch: "tp/mer-orchestrator", RuntimeHandleID: "orch-handle"},
		Activity:  domain.Activity{State: domain.ActivityActive},
	}

	if err := m.RetireForReplacement(ctx, "mer-orch"); err != nil {
		t.Fatalf("RetireForReplacement err = %v", err)
	}

	if len(closer.began) != 1 || closer.began[0] != "mer-orch" {
		t.Fatalf("began = %v, want [mer-orch]", closer.began)
	}
	if len(closer.ended) != 1 || closer.ended[0] != "mer-orch" {
		t.Fatalf("ended = %v, want [mer-orch]", closer.ended)
	}
	beginIdx, forceIdx, endIdx := -1, -1, -1
	for i, c := range sharedLog {
		switch c {
		case "BeginSessionTeardown:mer-orch":
			beginIdx = i
		case "ForceDestroy:mer-orch":
			forceIdx = i
		case "EndSessionTeardown:mer-orch":
			endIdx = i
		}
	}
	if beginIdx == -1 || forceIdx == -1 || endIdx == -1 {
		t.Fatalf("call log missing expected entries: %v", sharedLog)
	}
	if beginIdx >= forceIdx || forceIdx >= endIdx {
		t.Fatalf("call order = %v, want begin, then force destroy, then end", sharedLog)
	}
}

// A shell terminal that cannot be confirmed closed must fail the whole
// retirement: unlike Kill, RetireForReplacement has no dirty-refusal path — it
// always force-destroys — so silently proceeding would remove the worktree
// out from under a still-live shell.
func TestRetireForReplacementFailsWhenShellTerminalsWontClose(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newLifecycleManager()
	m.SetShellTerminalCloser(&fakeShellTerminalCloser{err: errors.New("shellterm-1: still alive")})
	st.sessions["mer-orch"] = domain.SessionRecord{
		ID:        "mer-orch",
		ProjectID: "mer",
		Kind:      domain.KindOrchestrator,
		Metadata:  domain.SessionMetadata{WorkspacePath: "/ws/mer-orch", Branch: "tp/mer-orchestrator", RuntimeHandleID: "orch-handle"},
		Activity:  domain.Activity{State: domain.ActivityActive},
	}

	if err := m.RetireForReplacement(ctx, "mer-orch"); err == nil {
		t.Fatal("RetireForReplacement: want an error, the shell terminal was not confirmed closed")
	}
	if ws.destroyed != 0 {
		t.Errorf("workspace destroyed = %d, want 0", ws.destroyed)
	}
	if st.sessions["mer-orch"].IsTerminated {
		t.Error("session must not be terminated when retirement fails outright")
	}
}

// TestRetireForReplacementWorkspaceProjectClosesScopedShellTerminalsBeforeForceDestroy
// is the workspace-project variant of the same coverage gap.
func TestRetireForReplacementWorkspaceProjectClosesScopedShellTerminalsBeforeForceDestroy(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newLifecycleManager()
	var sharedLog []string
	st.sharedLog = &sharedLog
	ws.sharedLog = &sharedLog
	closer := &fakeShellTerminalCloser{sharedLog: &sharedLog}
	m.SetShellTerminalCloser(closer)
	ws.stashRef = "refs/ao/preserved/mer-orch"
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repos/mer", Kind: domain.ProjectKindWorkspace, Config: testRoleAgents()}
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{
		ProjectID:    "mer",
		Name:         "api",
		RelativePath: "api",
	}}
	st.sessions["mer-orch"] = domain.SessionRecord{
		ID:        "mer-orch",
		ProjectID: "mer",
		Kind:      domain.KindOrchestrator,
		Metadata:  domain.SessionMetadata{WorkspacePath: "/ws/mer-orch", Branch: "tp/mer-orchestrator", RuntimeHandleID: "orch-handle"},
		Activity:  domain.Activity{State: domain.ActivityActive},
	}
	st.worktrees["mer-orch"] = []domain.SessionWorktreeRecord{
		{SessionID: "mer-orch", RepoName: domain.RootWorkspaceRepoName, Branch: "tp/mer-orchestrator", WorktreePath: "/ws/mer-orch", State: "active"},
		{SessionID: "mer-orch", RepoName: "api", Branch: "tp/mer-orchestrator", WorktreePath: "/ws/mer-orch/api", State: "active"},
	}

	if err := m.RetireForReplacement(ctx, "mer-orch"); err != nil {
		t.Fatalf("RetireForReplacement err = %v", err)
	}

	if len(closer.began) != 1 || closer.began[0] != "mer-orch" {
		t.Fatalf("began = %v, want [mer-orch]", closer.began)
	}
	if len(closer.ended) != 1 || closer.ended[0] != "mer-orch" {
		t.Fatalf("ended = %v, want [mer-orch]", closer.ended)
	}
	beginIdx, forceIdx, endIdx := -1, -1, -1
	for i, c := range sharedLog {
		switch c {
		case "BeginSessionTeardown:mer-orch":
			beginIdx = i
		case "ForceDestroy:api":
			forceIdx = i
		case "EndSessionTeardown:mer-orch":
			endIdx = i
		}
	}
	if beginIdx == -1 || forceIdx == -1 || endIdx == -1 {
		t.Fatalf("call log missing expected entries: %v", sharedLog)
	}
	if beginIdx >= forceIdx || forceIdx >= endIdx {
		t.Fatalf("call order = %v, want begin, then force destroy, then end", sharedLog)
	}
}

func TestRetireForReplacement_ScratchPreservesWorkspace(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newLifecycleManager()
	st.projects["scratch"] = domain.ProjectRecord{ID: "scratch", Kind: domain.ProjectKindScratch, Config: testRoleAgents()}
	st.sessions["scratch-1"] = domain.SessionRecord{
		ID:        "scratch-1",
		ProjectID: "scratch",
		Kind:      domain.KindOrchestrator,
		Metadata:  domain.SessionMetadata{WorkspacePath: "/ws/scratch-1", RuntimeHandleID: "orch-handle"},
		Activity:  domain.Activity{State: domain.ActivityActive},
	}

	if err := m.RetireForReplacement(ctx, "scratch-1"); err != nil {
		t.Fatalf("RetireForReplacement err = %v", err)
	}
	if !st.sessions["scratch-1"].IsTerminated {
		t.Fatal("scratch orchestrator must be marked terminated")
	}
	if rt.destroyed != 1 || rt.destroyedIDs[0] != "orch-handle" {
		t.Fatalf("runtime destroyed = %d ids=%v, want orch-handle", rt.destroyed, rt.destroyedIDs)
	}
	if ws.stashCalls != 0 || len(ws.calls) != 0 {
		t.Fatalf("scratch replacement must preserve workspace without stash/force destroy, calls=%v stash=%d", ws.calls, ws.stashCalls)
	}
	if rows := st.worktrees["scratch-1"]; len(rows) != 0 {
		t.Fatalf("scratch replacement must not write restore markers, got %#v", rows)
	}
}

func TestRetireForReplacementStaleWorkspaceSkipsPreserveAndTerminates(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newLifecycleManager()
	var sharedLog []string
	st.sharedLog = &sharedLog
	ws.sharedLog = &sharedLog
	ws.stashErr = ports.ErrWorkspaceStale
	st.sessions["mer-orch"] = domain.SessionRecord{
		ID:        "mer-orch",
		ProjectID: "mer",
		Kind:      domain.KindOrchestrator,
		Metadata:  domain.SessionMetadata{WorkspacePath: "/ws/mer-orch", Branch: "tp/mer-orchestrator", RuntimeHandleID: "orch-handle"},
		Activity:  domain.Activity{State: domain.ActivityActive},
	}
	st.worktrees["mer-orch"] = []domain.SessionWorktreeRecord{{
		SessionID:    "mer-orch",
		RepoName:     domain.RootWorkspaceRepoName,
		Branch:       "tp/mer-orchestrator",
		WorktreePath: "/ws/mer-orch",
		PreservedRef: "refs/ao/preserved/old",
	}}

	if err := m.RetireForReplacement(ctx, "mer-orch"); err != nil {
		t.Fatalf("RetireForReplacement err = %v", err)
	}

	if rows := st.worktrees["mer-orch"]; len(rows) != 0 {
		t.Fatalf("stale replacement must clear restore markers, got %#v", rows)
	}
	if !st.sessions["mer-orch"].IsTerminated {
		t.Fatal("stale replaced orchestrator must be marked terminated")
	}
	if rt.destroyed != 1 || rt.destroyedIDs[0] != "orch-handle" {
		t.Fatalf("runtime destroyed = %d ids=%v, want orch-handle", rt.destroyed, rt.destroyedIDs)
	}
	wantOrder := []string{
		"StashUncommitted:mer-orch",
		"DeleteSessionWorktrees:mer-orch",
		"ForceDestroy:mer-orch",
	}
	next := 0
	for _, call := range sharedLog {
		if next < len(wantOrder) && call == wantOrder[next] {
			next++
		}
	}
	if next != len(wantOrder) {
		t.Fatalf("stale replacement order missing %v in log %v", wantOrder, sharedLog)
	}
}

func TestRetireForReplacementStaleWorkspaceCleanupFailureTerminatesSession(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newLifecycleManager()
	ws.stashErr = ports.ErrWorkspaceStale
	ws.forceDestroyErr = errors.New("stale cleanup failed")
	st.sessions["mer-orch"] = domain.SessionRecord{
		ID:        "mer-orch",
		ProjectID: "mer",
		Kind:      domain.KindOrchestrator,
		Metadata:  domain.SessionMetadata{WorkspacePath: "/ws/mer-orch", Branch: "tp/mer-orchestrator", RuntimeHandleID: "orch-handle"},
		Activity:  domain.Activity{State: domain.ActivityActive},
	}
	st.worktrees["mer-orch"] = []domain.SessionWorktreeRecord{{
		SessionID:    "mer-orch",
		RepoName:     domain.RootWorkspaceRepoName,
		Branch:       "tp/mer-orchestrator",
		WorktreePath: "/ws/mer-orch",
		PreservedRef: "refs/ao/preserved/old",
	}}

	err := m.RetireForReplacement(ctx, "mer-orch")
	if err == nil || !strings.Contains(err.Error(), "force destroy") {
		t.Fatalf("RetireForReplacement err = %v, want force destroy failure", err)
	}
	if !st.sessions["mer-orch"].IsTerminated {
		t.Fatal("stopped orchestrator must be terminated for cleanup retry")
	}
	if rows := st.worktrees["mer-orch"]; len(rows) != 0 {
		t.Fatalf("restore markers must be cleared before cleanup: %v", rows)
	}
	if rt.destroyed != 1 || rt.destroyedIDs[0] != "orch-handle" {
		t.Fatalf("runtime destroyed = %d ids=%v, want orch-handle", rt.destroyed, rt.destroyedIDs)
	}
}

func TestRetireForReplacementStashFailureLeavesSessionActive(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newLifecycleManager()
	ws.stashErr = errors.New("preserve failed")
	st.sessions["mer-orch"] = domain.SessionRecord{
		ID:        "mer-orch",
		ProjectID: "mer",
		Kind:      domain.KindOrchestrator,
		Metadata:  domain.SessionMetadata{WorkspacePath: "/ws/mer-orch", Branch: "tp/mer-orchestrator", RuntimeHandleID: "orch-handle"},
		Activity:  domain.Activity{State: domain.ActivityActive},
	}
	st.worktrees["mer-orch"] = []domain.SessionWorktreeRecord{{
		SessionID:    "mer-orch",
		RepoName:     domain.RootWorkspaceRepoName,
		Branch:       "tp/mer-orchestrator",
		WorktreePath: "/ws/mer-orch",
		PreservedRef: "refs/ao/preserved/old",
	}}

	err := m.RetireForReplacement(ctx, "mer-orch")
	if err == nil || !strings.Contains(err.Error(), "stash") {
		t.Fatalf("RetireForReplacement err = %v, want stash failure", err)
	}
	if st.sessions["mer-orch"].IsTerminated {
		t.Fatal("session must remain active when preserve fails")
	}
	if rows := st.worktrees["mer-orch"]; len(rows) != 1 {
		t.Fatalf("restore markers after preserve failure = %v, want retained", rows)
	}
	if rt.destroyed != 0 {
		t.Fatalf("runtime destroyed = %d, want 0 after preserve failure", rt.destroyed)
	}
	for _, call := range ws.calls {
		if call == "ForceDestroy:mer-orch" {
			t.Fatalf("ForceDestroy must not run after preserve failure; calls=%v", ws.calls)
		}
	}
}

func TestRetireForReplacementWorkspaceProjectCapturesAndReleasesEveryRepo(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newLifecycleManager()
	var sharedLog []string
	st.sharedLog = &sharedLog
	ws.sharedLog = &sharedLog
	ws.stashRef = "refs/ao/preserved/mer-orch"
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repos/mer", Kind: domain.ProjectKindWorkspace, Config: testRoleAgents()}
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{
		ProjectID:    "mer",
		Name:         "api",
		RelativePath: "api",
	}}
	st.sessions["mer-orch"] = domain.SessionRecord{
		ID:        "mer-orch",
		ProjectID: "mer",
		Kind:      domain.KindOrchestrator,
		Metadata:  domain.SessionMetadata{WorkspacePath: "/ws/mer-orch", Branch: "tp/mer-orchestrator", RuntimeHandleID: "orch-handle"},
		Activity:  domain.Activity{State: domain.ActivityActive},
	}
	st.worktrees["mer-orch"] = []domain.SessionWorktreeRecord{
		{
			SessionID:    "mer-orch",
			RepoName:     domain.RootWorkspaceRepoName,
			Branch:       "tp/mer-orchestrator",
			WorktreePath: "/ws/mer-orch",
			PreservedRef: "refs/ao/preserved/old-root",
			State:        "active",
		},
		{
			SessionID:    "mer-orch",
			RepoName:     "api",
			Branch:       "tp/mer-orchestrator",
			WorktreePath: "/ws/mer-orch/api",
			PreservedRef: "refs/ao/preserved/old-api",
			State:        "active",
		},
	}

	if err := m.RetireForReplacement(ctx, "mer-orch"); err != nil {
		t.Fatalf("RetireForReplacement err = %v", err)
	}

	if rows := st.worktrees["mer-orch"]; len(rows) != 0 {
		t.Fatalf("replacement retirement must not write restore markers, got %#v", rows)
	}
	if !st.sessions["mer-orch"].IsTerminated {
		t.Fatal("retired orchestrator must be marked terminated")
	}
	if rt.destroyed != 1 || rt.destroyedIDs[0] != "orch-handle" {
		t.Fatalf("runtime destroyed = %d ids=%v, want orch-handle", rt.destroyed, rt.destroyedIDs)
	}

	wantOrder := []string{
		"StashUncommitted:__root__",
		"StashUncommitted:api",
		"ForceDestroy:api",
		"ForceDestroy:__root__",
		"DeleteSessionWorktrees:mer-orch",
	}
	next := 0
	for _, call := range sharedLog {
		if next < len(wantOrder) && call == wantOrder[next] {
			next++
		}
	}
	if next != len(wantOrder) {
		t.Fatalf("workspace project retirement order missing %v in log %v", wantOrder, sharedLog)
	}
}

func TestRetireForReplacementWorkspaceProjectRuntimeDestroyFailureKeepsRepoInventory(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newLifecycleManager()
	rt.destroyErr = errors.New("tmux transient")
	ws.stashRef = "refs/ao/preserved/mer-orch"
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repos/mer", Kind: domain.ProjectKindWorkspace, Config: testRoleAgents()}
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{
		ProjectID:    "mer",
		Name:         "api",
		RelativePath: "api",
	}}
	st.sessions["mer-orch"] = domain.SessionRecord{
		ID:        "mer-orch",
		ProjectID: "mer",
		Kind:      domain.KindOrchestrator,
		Metadata:  domain.SessionMetadata{WorkspacePath: "/ws/mer-orch", Branch: "tp/mer-orchestrator", RuntimeHandleID: "orch-handle"},
		Activity:  domain.Activity{State: domain.ActivityActive},
	}
	st.worktrees["mer-orch"] = []domain.SessionWorktreeRecord{
		{SessionID: "mer-orch", RepoName: domain.RootWorkspaceRepoName, Branch: "tp/mer-orchestrator", WorktreePath: "/ws/mer-orch", State: "active"},
		{SessionID: "mer-orch", RepoName: "api", Branch: "tp/mer-orchestrator", WorktreePath: "/ws/mer-orch/api", State: "active"},
	}

	err := m.RetireForReplacement(ctx, "mer-orch")
	if err == nil || !strings.Contains(err.Error(), "runtime") {
		t.Fatalf("RetireForReplacement err = %v, want runtime failure", err)
	}
	if st.sessions["mer-orch"].IsTerminated {
		t.Fatal("session must remain active when runtime destroy fails")
	}
	if rows := st.worktrees["mer-orch"]; len(rows) != 2 {
		t.Fatalf("workspace repo inventory after runtime failure = %v, want root and child retained", rows)
	}
	for _, call := range ws.calls {
		if strings.HasPrefix(call, "ForceDestroy:") {
			t.Fatalf("ForceDestroy must not run after runtime destroy failure; calls=%v", ws.calls)
		}
	}
}

func TestRetireForReplacementWorkspaceProjectForceDestroyFailureKeepsRepoInventory(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newLifecycleManager()
	ws.forceDestroyErr = errors.New("worktree still registered")
	ws.stashRef = "refs/ao/preserved/mer-orch"
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repos/mer", Kind: domain.ProjectKindWorkspace, Config: testRoleAgents()}
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{
		ProjectID:    "mer",
		Name:         "api",
		RelativePath: "api",
	}}
	st.sessions["mer-orch"] = domain.SessionRecord{
		ID:        "mer-orch",
		ProjectID: "mer",
		Kind:      domain.KindOrchestrator,
		Metadata:  domain.SessionMetadata{WorkspacePath: "/ws/mer-orch", Branch: "tp/mer-orchestrator", RuntimeHandleID: "orch-handle"},
		Activity:  domain.Activity{State: domain.ActivityActive},
	}
	st.worktrees["mer-orch"] = []domain.SessionWorktreeRecord{
		{SessionID: "mer-orch", RepoName: domain.RootWorkspaceRepoName, Branch: "tp/mer-orchestrator", WorktreePath: "/ws/mer-orch", State: "active"},
		{SessionID: "mer-orch", RepoName: "api", Branch: "tp/mer-orchestrator", WorktreePath: "/ws/mer-orch/api", State: "active"},
	}

	err := m.RetireForReplacement(ctx, "mer-orch")
	if err == nil || !strings.Contains(err.Error(), "force destroy") {
		t.Fatalf("RetireForReplacement err = %v, want force destroy failure", err)
	}
	if !st.sessions["mer-orch"].IsTerminated {
		t.Fatal("stopped orchestrator must be terminated for cleanup retry")
	}
	if rows := st.worktrees["mer-orch"]; len(rows) != 2 {
		t.Fatalf("workspace repo inventory after force destroy failure = %v, want root and child retained", rows)
	}
}

func TestRetireForReplacementWorkspaceProjectStaleCleanupFailureKeepsRepoInventory(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newLifecycleManager()
	ws.stashErr = ports.ErrWorkspaceStale
	ws.forceDestroyErr = errors.New("stale cleanup failed")
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repos/mer", Kind: domain.ProjectKindWorkspace, Config: testRoleAgents()}
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{
		ProjectID:    "mer",
		Name:         "api",
		RelativePath: "api",
	}}
	st.sessions["mer-orch"] = domain.SessionRecord{
		ID:        "mer-orch",
		ProjectID: "mer",
		Kind:      domain.KindOrchestrator,
		Metadata:  domain.SessionMetadata{WorkspacePath: "/ws/mer-orch", Branch: "tp/mer-orchestrator", RuntimeHandleID: "orch-handle"},
		Activity:  domain.Activity{State: domain.ActivityActive},
	}
	st.worktrees["mer-orch"] = []domain.SessionWorktreeRecord{
		{SessionID: "mer-orch", RepoName: domain.RootWorkspaceRepoName, Branch: "tp/mer-orchestrator", WorktreePath: "/ws/mer-orch", State: "active"},
		{SessionID: "mer-orch", RepoName: "api", Branch: "tp/mer-orchestrator", WorktreePath: "/ws/mer-orch/api", State: "active"},
	}

	err := m.RetireForReplacement(ctx, "mer-orch")
	if err == nil || !strings.Contains(err.Error(), "force destroy") {
		t.Fatalf("RetireForReplacement err = %v, want force destroy failure", err)
	}
	if !st.sessions["mer-orch"].IsTerminated {
		t.Fatal("stopped orchestrator must be terminated for cleanup retry")
	}
	if rows := st.worktrees["mer-orch"]; len(rows) != 2 {
		t.Fatalf("workspace repo inventory after stale cleanup failure = %v, want root and child retained", rows)
	}
}

func TestRetireForReplacementForceDestroyFailureTerminatesSession(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newLifecycleManager()
	ws.forceDestroyErr = errors.New("worktree still registered")
	ws.stashRef = "refs/ao/preserved/mer-orch"
	st.sessions["mer-orch"] = domain.SessionRecord{
		ID:        "mer-orch",
		ProjectID: "mer",
		Kind:      domain.KindOrchestrator,
		Metadata:  domain.SessionMetadata{WorkspacePath: "/ws/mer-orch", Branch: "tp/mer-orchestrator", RuntimeHandleID: "orch-handle"},
		Activity:  domain.Activity{State: domain.ActivityActive},
	}
	st.worktrees["mer-orch"] = []domain.SessionWorktreeRecord{{
		SessionID:    "mer-orch",
		RepoName:     domain.RootWorkspaceRepoName,
		Branch:       "tp/mer-orchestrator",
		WorktreePath: "/ws/mer-orch",
		PreservedRef: "refs/ao/preserved/old",
	}}

	err := m.RetireForReplacement(ctx, "mer-orch")
	if err == nil || !strings.Contains(err.Error(), "force destroy") {
		t.Fatalf("RetireForReplacement err = %v, want force destroy failure", err)
	}
	if !st.sessions["mer-orch"].IsTerminated {
		t.Fatal("stopped orchestrator must be terminated for cleanup retry")
	}
	if rt.destroyed != 1 {
		t.Fatalf("runtime destroyed = %d, want 1 before workspace release", rt.destroyed)
	}
	if ws.stashCalls != 1 {
		t.Fatalf("stash calls = %d, want 1", ws.stashCalls)
	}
}

func TestRetireForReplacementRuntimeDestroyFailureBlocksWorkspaceRelease(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newLifecycleManager()
	rt.destroyErr = errors.New("tmux transient")
	ws.stashRef = "refs/ao/preserved/mer-orch"
	st.sessions["mer-orch"] = domain.SessionRecord{
		ID:        "mer-orch",
		ProjectID: "mer",
		Kind:      domain.KindOrchestrator,
		Metadata:  domain.SessionMetadata{WorkspacePath: "/ws/mer-orch", Branch: "tp/mer-orchestrator", RuntimeHandleID: "orch-handle"},
		Activity:  domain.Activity{State: domain.ActivityActive},
	}
	st.worktrees["mer-orch"] = []domain.SessionWorktreeRecord{{
		SessionID:    "mer-orch",
		RepoName:     domain.RootWorkspaceRepoName,
		Branch:       "tp/mer-orchestrator",
		WorktreePath: "/ws/mer-orch",
		PreservedRef: "refs/ao/preserved/old",
	}}

	err := m.RetireForReplacement(ctx, "mer-orch")
	if err == nil || !strings.Contains(err.Error(), "runtime") {
		t.Fatalf("RetireForReplacement err = %v, want runtime failure", err)
	}
	if st.sessions["mer-orch"].IsTerminated {
		t.Fatal("session must remain active when runtime destroy fails")
	}
	if rt.destroyed != 1 || rt.destroyedIDs[0] != "orch-handle" {
		t.Fatalf("runtime destroyed = %d ids=%v, want one attempt for orch-handle", rt.destroyed, rt.destroyedIDs)
	}
	for _, call := range ws.calls {
		if call == "ForceDestroy:mer-orch" {
			t.Fatalf("ForceDestroy must not run after runtime destroy failure; calls=%v", ws.calls)
		}
	}
}

// TestSaveAndTeardownAll_CleanWorktreeWritesEmptyRef verifies that a clean
// worktree (StashUncommitted returns "") still writes a worktree row (with
// empty preserved_ref). The row's presence is the shutdown-saved marker.
func TestSaveAndTeardownAll_CleanWorktreeWritesEmptyRef(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newLifecycleManager()
	ws.stashRef = "" // clean worktree
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		Metadata:  domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1/root", RuntimeHandleID: "h1"},
		Activity:  domain.Activity{State: domain.ActivityActive},
	}

	if err := m.SaveAndTeardownAll(ctx); err != nil {
		t.Fatalf("SaveAndTeardownAll err = %v", err)
	}

	rows := st.worktrees["mer-1"]
	if len(rows) == 0 {
		t.Fatal("clean worktree must still write a session_worktrees row as the shutdown-saved marker")
	}
	if rows[0].PreservedRef != "" {
		t.Fatalf("preserved_ref = %q, want empty for clean worktree", rows[0].PreservedRef)
	}
}

// TestSaveAndTeardownAll_SkipsNoWorkspacePath: sessions without a workspace
// path are skipped (spawn failed before workspace.Create).
func TestSaveAndTeardownAll_SkipsNoWorkspacePath(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newLifecycleManager()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		Metadata:  domain.SessionMetadata{}, // no workspace path
		Activity:  domain.Activity{State: domain.ActivityActive},
	}

	if err := m.SaveAndTeardownAll(ctx); err != nil {
		t.Fatalf("SaveAndTeardownAll err = %v", err)
	}

	if len(ws.calls) != 0 {
		t.Fatalf("no workspace calls expected for sessions with no workspace path, got %v", ws.calls)
	}
	if len(st.worktrees["mer-1"]) != 0 {
		t.Fatal("no worktree row should be written for sessions with no workspace path")
	}
}

// TestSaveAndTeardownAll_SkipsAlreadyTerminated: already-terminated sessions
// are skipped.
func TestSaveAndTeardownAll_SkipsAlreadyTerminated(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newLifecycleManager()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:           "mer-1",
		ProjectID:    "mer",
		Kind:         domain.KindWorker,
		IsTerminated: true,
		Metadata:     domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1/root"},
		Activity:     domain.Activity{State: domain.ActivityExited},
	}

	if err := m.SaveAndTeardownAll(ctx); err != nil {
		t.Fatalf("SaveAndTeardownAll err = %v", err)
	}
	if len(ws.calls) != 0 {
		t.Fatalf("already-terminated sessions must be skipped, got calls %v", ws.calls)
	}
}

// TestSaveAndTeardownAll_NoKindFilter: both worker and orchestrator sessions
// are saved (no kind filter).
func TestSaveAndTeardownAll_NoKindFilter(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newLifecycleManager()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1/root", RuntimeHandleID: "h1"},
		Activity: domain.Activity{State: domain.ActivityActive},
	}
	st.sessions["mer-2"] = domain.SessionRecord{
		ID: "mer-2", ProjectID: "mer", Kind: domain.KindOrchestrator,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-2", Branch: "tp/mer-orchestrator", RuntimeHandleID: "h2"},
		Activity: domain.Activity{State: domain.ActivityActive},
	}

	if err := m.SaveAndTeardownAll(ctx); err != nil {
		t.Fatalf("SaveAndTeardownAll err = %v", err)
	}

	if len(st.worktrees["mer-1"]) == 0 {
		t.Error("worker session mer-1 must be saved")
	}
	if len(st.worktrees["mer-2"]) == 0 {
		t.Error("orchestrator session mer-2 must be saved")
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Error("worker session mer-1 must be terminated")
	}
	if !st.sessions["mer-2"].IsTerminated {
		t.Error("orchestrator session mer-2 must be terminated")
	}
}

func TestSaveAndTeardownAll_WorkspaceProjectPreservesEachRepoAndRemovesChildrenFirst(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newLifecycleManager()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Kind: domain.ProjectKindWorkspace, Config: testRoleAgents()}
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{Name: "api", RelativePath: "api"}}
	ws.stashRef = "refs/ao/preserved/mer-1"
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		Metadata:  domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1", RuntimeHandleID: "h1"},
		Activity:  domain.Activity{State: domain.ActivityActive},
	}
	st.worktrees["mer-1"] = []domain.SessionWorktreeRecord{
		{SessionID: "mer-1", RepoName: domain.RootWorkspaceRepoName, Branch: "tp/mer-1", WorktreePath: "/ws/mer-1", BaseSHA: "root-base"},
		{SessionID: "mer-1", RepoName: "api", Branch: "tp/mer-1", WorktreePath: "/ws/mer-1/api", BaseSHA: "api-base"},
	}

	if err := m.SaveAndTeardownAll(ctx); err != nil {
		t.Fatalf("SaveAndTeardownAll err = %v", err)
	}
	rows := st.worktrees["mer-1"]
	if len(rows) != 2 {
		t.Fatalf("worktree rows = %v, want 2", rows)
	}
	refs := map[string]string{}
	for _, row := range rows {
		refs[row.RepoName] = row.PreservedRef
	}
	if refs[domain.RootWorkspaceRepoName] != "refs/ao/preserved/mer-1/__root__" || refs["api"] != "refs/ao/preserved/mer-1/api" {
		t.Fatalf("preserved refs = %v", refs)
	}
	wantSuffix := []string{"ForceDestroy:api", "ForceDestroy:__root__"}
	gotSuffix := ws.calls[len(ws.calls)-2:]
	if strings.Join(gotSuffix, ",") != strings.Join(wantSuffix, ",") {
		t.Fatalf("force destroy suffix = %v, want %v; all calls %v", gotSuffix, wantSuffix, ws.calls)
	}
}

func TestSaveAndTeardownAll_WorkspaceProjectRegistryDriftPreservesWholeWorkspace(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newLifecycleManager()
	st.projects["mer"] = domain.ProjectRecord{
		ID:     "mer",
		Path:   "/repo/mer",
		Kind:   domain.ProjectKindWorkspace,
		Config: testRoleAgents(),
	}
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{Name: "api", RelativePath: "api"}}
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		Metadata:  domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1/root", RuntimeHandleID: "h1"},
		Activity:  domain.Activity{State: domain.ActivityActive},
	}
	st.worktrees["mer-1"] = []domain.SessionWorktreeRecord{
		{SessionID: "mer-1", RepoName: domain.RootWorkspaceRepoName, Branch: "tp/mer-1/root", WorktreePath: "/ws/mer-1", State: "active"},
		{SessionID: "mer-1", RepoName: "old-child", Branch: "tp/mer-1/root", WorktreePath: "/ws/mer-1/old-child", State: "active"},
	}

	if err := m.SaveAndTeardownAll(ctx); err != nil {
		t.Fatalf("SaveAndTeardownAll err = %v", err)
	}
	if ws.stashCalls != 0 {
		t.Fatalf("stash calls = %d, want 0 when registry drift makes rows unsafe", ws.stashCalls)
	}
	for _, call := range ws.calls {
		if strings.HasPrefix(call, "ForceDestroy:") {
			t.Fatalf("ForceDestroy must not run when a historical child row is unresolved; calls=%v", ws.calls)
		}
	}
	if st.sessions["mer-1"].IsTerminated {
		t.Fatal("session should remain live when teardown is skipped for registry drift")
	}
}

// TestRestoreAll_RestoresBothWorkerAndOrchestrator verifies (b): RestoreAll
// restores both a worker and an orchestrator session saved by SaveAndTeardownAll.
func TestRestoreAll_RestoresBothWorkerAndOrchestrator(t *testing.T) {
	t.Parallel()

	m, st, rt, _ := newLifecycleManager()

	// Seed two terminated sessions that were saved by SaveAndTeardownAll
	// (presence of session_worktrees rows is the marker).
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:           "mer-1",
		ProjectID:    "mer",
		Kind:         domain.KindWorker,
		Harness:      domain.HarnessClaudeCode,
		IsTerminated: true,
		Metadata:     domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1/root", AgentSessionID: "agent-w"},
		Activity:     domain.Activity{State: domain.ActivityExited},
	}
	st.sessions["mer-2"] = domain.SessionRecord{
		ID:           "mer-2",
		ProjectID:    "mer",
		Kind:         domain.KindOrchestrator,
		Harness:      domain.HarnessClaudeCode,
		IsTerminated: true,
		Metadata:     domain.SessionMetadata{WorkspacePath: "/ws/mer-2", Branch: "tp/mer-orchestrator", AgentSessionID: "agent-o"},
		Activity:     domain.Activity{State: domain.ActivityExited},
	}
	// Write the shutdown-saved marker rows.
	st.worktrees["mer-1"] = []domain.SessionWorktreeRecord{{SessionID: "mer-1", RepoName: "__root__", PreservedRef: "", State: "removed"}}
	st.worktrees["mer-2"] = []domain.SessionWorktreeRecord{{SessionID: "mer-2", RepoName: "__root__", PreservedRef: "", State: "removed"}}

	if err := m.RestoreAll(ctx); err != nil {
		t.Fatalf("RestoreAll err = %v", err)
	}

	if rt.created != 2 {
		t.Fatalf("RestoreAll must relaunch both sessions, runtime.Create called %d times", rt.created)
	}
	if st.sessions["mer-1"].IsTerminated {
		t.Error("worker session mer-1 must be live after RestoreAll")
	}
	if st.sessions["mer-2"].IsTerminated {
		t.Error("orchestrator session mer-2 must be live after RestoreAll")
	}
	// B190: RestoreAll relaunches sessions stopped at shutdown (restart
	// resume), so it opts in to the orchestrator worktree refresh.
	ws := m.workspace.(*fakeWorkspace)
	if len(ws.restoreConfigs) != 2 {
		t.Fatalf("Restore configs = %+v, want 2", ws.restoreConfigs)
	}
	for _, c := range ws.restoreConfigs {
		if !c.RefreshOrchestrator {
			t.Errorf("RestoreAll Restore(%s) did not opt in to the worktree refresh", c.SessionID)
		}
	}
}

func TestRestoreAllCarriesConfiguredAndRecordedBaseToWorkspaceRestore(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newLifecycleManager()
	project := st.projects["mer"]
	project.Config.DefaultBranch = "trunk"
	st.projects["mer"] = project
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:           "mer-1",
		ProjectID:    "mer",
		Kind:         domain.KindWorker,
		Harness:      domain.HarnessClaudeCode,
		IsTerminated: true,
		Metadata: domain.SessionMetadata{
			WorkspacePath:  "/ws/mer-1",
			Branch:         "tp/mer-1/root",
			DiffBaseRef:    "refs/remotes/origin/trunk",
			AgentSessionID: "agent-w",
		},
		Activity: domain.Activity{State: domain.ActivityExited},
	}
	st.worktrees["mer-1"] = []domain.SessionWorktreeRecord{{
		SessionID: "mer-1",
		RepoName:  domain.RootWorkspaceRepoName,
		State:     "removed",
	}}

	if err := m.RestoreAll(ctx); err != nil {
		t.Fatalf("RestoreAll err = %v", err)
	}
	if len(ws.restoreConfigs) != 1 {
		t.Fatalf("restore configs = %d, want 1", len(ws.restoreConfigs))
	}
	if got := ws.restoreConfigs[0].BaseBranch; got != "trunk" {
		t.Fatalf("restore BaseBranch = %q, want trunk", got)
	}
	if got := ws.restoreConfigs[0].BaseRef; got != "refs/remotes/origin/trunk" {
		t.Fatalf("restore BaseRef = %q, want refs/remotes/origin/trunk", got)
	}
}

func TestRestoreAll_RestoresLegacyShutdownMarkerWithoutState(t *testing.T) {
	t.Parallel()

	m, st, rt, _ := newLifecycleManager()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:           "mer-1",
		ProjectID:    "mer",
		Kind:         domain.KindWorker,
		Harness:      domain.HarnessClaudeCode,
		IsTerminated: true,
		Metadata:     domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1/root", AgentSessionID: "agent-w"},
		Activity:     domain.Activity{State: domain.ActivityExited},
	}
	st.worktrees["mer-1"] = []domain.SessionWorktreeRecord{
		{SessionID: "mer-1", RepoName: domain.RootWorkspaceRepoName, WorktreePath: "/ws/mer-1"},
	}

	if err := m.RestoreAll(ctx); err != nil {
		t.Fatalf("RestoreAll err = %v", err)
	}
	if rt.created != 1 {
		t.Fatalf("legacy shutdown marker must relaunch once, runtime.Create called %d times", rt.created)
	}
	if st.sessions["mer-1"].IsTerminated {
		t.Fatal("legacy shutdown marker session must be live after RestoreAll")
	}
	if rows := st.worktrees["mer-1"]; len(rows) != 0 {
		t.Fatalf("consumed restore marker = %+v, want deleted", rows)
	}
}

// TestRestoreAll_SkipsSessionsKilledBeforeShutdown verifies (c): a session
// the user killed BEFORE shutdown has no session_worktrees row and must NOT
// be resurrected.
func TestRestoreAll_SkipsSessionsKilledBeforeShutdown(t *testing.T) {
	t.Parallel()

	m, st, rt, _ := newLifecycleManager()

	// This session was killed by the user before shutdown: IsTerminated=true,
	// but no session_worktrees row (SaveAndTeardownAll skipped it).
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:           "mer-1",
		ProjectID:    "mer",
		Kind:         domain.KindWorker,
		Harness:      domain.HarnessClaudeCode,
		IsTerminated: true,
		Metadata:     domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1/root", Prompt: "do it"},
		Activity:     domain.Activity{State: domain.ActivityExited},
	}
	// Deliberately no entry in st.worktrees for mer-1.

	if err := m.RestoreAll(ctx); err != nil {
		t.Fatalf("RestoreAll err = %v", err)
	}

	if rt.created != 0 {
		t.Fatalf("user-killed session must not be restored, runtime.Create called %d times", rt.created)
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Error("user-killed session must remain terminated")
	}
}

// TestRestoreAll_DeletesMarkerAfterRelaunch covers issue #2319 (b): the
// shutdown-saved marker is one-shot. After RestoreAll relaunches a session, its
// session_worktrees marker is deleted, so a second RestoreAll (with no fresh
// marker) does NOT relaunch it again.
func TestRestoreAll_DeletesMarkerAfterRelaunch(t *testing.T) {
	t.Parallel()

	m, st, rt, _ := newLifecycleManager()

	st.sessions["mer-1"] = domain.SessionRecord{
		ID:           "mer-1",
		ProjectID:    "mer",
		Kind:         domain.KindWorker,
		Harness:      domain.HarnessClaudeCode,
		IsTerminated: true,
		Metadata:     domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1/root", AgentSessionID: "agent-w"},
		Activity:     domain.Activity{State: domain.ActivityExited},
	}
	st.worktrees["mer-1"] = []domain.SessionWorktreeRecord{{SessionID: "mer-1", RepoName: "__root__", State: "removed"}}

	if err := m.RestoreAll(ctx); err != nil {
		t.Fatalf("RestoreAll err = %v", err)
	}
	if rt.created != 1 {
		t.Fatalf("first RestoreAll must relaunch once, runtime.Create called %d times", rt.created)
	}
	rows, err := st.ListSessionWorktrees(ctx, "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("RestoreAll must delete the one-shot marker, got %d rows", len(rows))
	}
}

// TestRestoreAll_KilledSessionNotResurrectedOnSecondBoot covers issue #2319 (c),
// the killed-session-resurrection scenario. A terminated session WITH a marker
// is relaunched exactly once; on a second RestoreAll (no new marker) it stays
// terminated and is not relaunched again.
func TestRestoreAll_KilledSessionNotResurrectedOnSecondBoot(t *testing.T) {
	t.Parallel()

	m, st, rt, _ := newLifecycleManager()

	st.sessions["mer-1"] = domain.SessionRecord{
		ID:           "mer-1",
		ProjectID:    "mer",
		Kind:         domain.KindWorker,
		Harness:      domain.HarnessClaudeCode,
		IsTerminated: true,
		Metadata:     domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1/root", AgentSessionID: "agent-w"},
		Activity:     domain.Activity{State: domain.ActivityExited},
	}
	st.worktrees["mer-1"] = []domain.SessionWorktreeRecord{{SessionID: "mer-1", RepoName: "__root__", State: "removed"}}

	// First boot: marker present, session relaunches once.
	if err := m.RestoreAll(ctx); err != nil {
		t.Fatalf("first RestoreAll err = %v", err)
	}
	if rt.created != 1 {
		t.Fatalf("first RestoreAll must relaunch once, runtime.Create called %d times", rt.created)
	}

	// Simulate the user killing the relaunched session before the next quit, so
	// it has no fresh marker, then a second boot.
	if _, err := m.Kill(ctx, "mer-1"); err != nil {
		t.Fatalf("kill err = %v", err)
	}
	if err := m.RestoreAll(ctx); err != nil {
		t.Fatalf("second RestoreAll err = %v", err)
	}
	if rt.created != 1 {
		t.Fatalf("killed session must NOT be resurrected on second boot, runtime.Create total = %d, want 1", rt.created)
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Error("killed session must remain terminated after second RestoreAll")
	}
}

func TestRestoreAll_SkipsActiveWorkspaceProjectRowsFromUserKilledSession(t *testing.T) {
	t.Parallel()

	m, st, rt, _ := newLifecycleManager()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Kind: domain.ProjectKindWorkspace, Config: testRoleAgents()}
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{Name: "api", RelativePath: "api"}}
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:           "mer-1",
		ProjectID:    "mer",
		Kind:         domain.KindWorker,
		Harness:      domain.HarnessClaudeCode,
		IsTerminated: true,
		Metadata:     domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1", Prompt: "do it"},
		Activity:     domain.Activity{State: domain.ActivityExited},
	}
	st.worktrees["mer-1"] = []domain.SessionWorktreeRecord{
		{SessionID: "mer-1", RepoName: domain.RootWorkspaceRepoName, Branch: "tp/mer-1", WorktreePath: "/ws/mer-1", State: "active"},
		{SessionID: "mer-1", RepoName: "api", Branch: "tp/mer-1", WorktreePath: "/ws/mer-1/api", State: "active"},
	}

	if err := m.RestoreAll(ctx); err != nil {
		t.Fatalf("RestoreAll err = %v", err)
	}
	if rt.created != 0 {
		t.Fatalf("active inventory rows must not resurrect user-killed sessions, runtime.Create called %d times", rt.created)
	}
}

// TestRestoreAll_AppliesPreservedRef: when the session_worktrees row has a
// non-empty preserved_ref, RestoreAll calls ApplyPreserved after workspace
// restore but before relaunching.
func TestRestoreAll_AppliesPreservedRef(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newLifecycleManager()

	st.sessions["mer-1"] = domain.SessionRecord{
		ID:           "mer-1",
		ProjectID:    "mer",
		Kind:         domain.KindWorker,
		Harness:      domain.HarnessClaudeCode,
		IsTerminated: true,
		Metadata:     domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1/root", AgentSessionID: "agent-w"},
		Activity:     domain.Activity{State: domain.ActivityExited},
	}
	st.worktrees["mer-1"] = []domain.SessionWorktreeRecord{
		{SessionID: "mer-1", RepoName: "__root__", PreservedRef: "refs/ao/preserved/mer-1", State: "removed"},
	}

	if err := m.RestoreAll(ctx); err != nil {
		t.Fatalf("RestoreAll err = %v", err)
	}

	applied := false
	for _, c := range ws.calls {
		if c == "ApplyPreserved:mer-1" {
			applied = true
		}
	}
	if !applied {
		t.Fatal("ApplyPreserved was not called for session with preserved_ref")
	}
	if rt.created != 1 {
		t.Fatal("session must still be relaunched even after ApplyPreserved")
	}
}

// TestRestoreAll_ConflictLogsAndContinues: when ApplyPreserved returns
// ErrPreservedConflict, RestoreAll logs and continues (still relaunches).
func TestRestoreAll_ConflictLogsAndContinues(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rt := &fakeRuntime{}
	ws := &fakeWorkspace{applyErr: fmt.Errorf("conflict: %w", ports.ErrPreservedConflict)}
	var logBuf bytes.Buffer
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{
		Runtime:   rt,
		Agents:    fakeAgents{},
		Workspace: ws,
		Store:     st,
		Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: st},
		LookPath:  lookPath,
		Logger:    slog.New(slog.NewTextHandler(&logBuf, nil)),
	})

	st.sessions["mer-1"] = domain.SessionRecord{
		ID:           "mer-1",
		ProjectID:    "mer",
		Kind:         domain.KindWorker,
		Harness:      domain.HarnessClaudeCode,
		IsTerminated: true,
		Metadata:     domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1/root", AgentSessionID: "agent-w"},
		Activity:     domain.Activity{State: domain.ActivityExited},
	}
	st.worktrees["mer-1"] = []domain.SessionWorktreeRecord{
		{SessionID: "mer-1", RepoName: "__root__", PreservedRef: "refs/ao/preserved/mer-1", State: "removed"},
	}

	if err := m.RestoreAll(ctx); err != nil {
		t.Fatalf("RestoreAll err = %v; conflict must not abort", err)
	}
	if rt.created != 1 {
		t.Fatalf("session must still relaunch after conflict, runtime.Create called %d times", rt.created)
	}
}

func TestRestoreAll_WorkspaceProjectRestoresAndAppliesEachRepo(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newLifecycleManager()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Kind: domain.ProjectKindWorkspace, Config: testRoleAgents()}
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{Name: "api", RelativePath: "api"}}
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:           "mer-1",
		ProjectID:    "mer",
		Kind:         domain.KindWorker,
		Harness:      domain.HarnessClaudeCode,
		IsTerminated: true,
		Metadata:     domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1", AgentSessionID: "agent-w"},
		Activity:     domain.Activity{State: domain.ActivityExited},
	}
	st.worktrees["mer-1"] = []domain.SessionWorktreeRecord{
		{SessionID: "mer-1", RepoName: domain.RootWorkspaceRepoName, Branch: "tp/mer-1", BaseRef: "refs/remotes/origin/main", WorktreePath: "/ws/mer-1", PreservedRef: "refs/ao/preserved/mer-1", State: "removed"},
		{SessionID: "mer-1", RepoName: "api", Branch: "tp/mer-1", BaseRef: "refs/remotes/origin/dev", WorktreePath: "/ws/mer-1/api", PreservedRef: "refs/ao/preserved/mer-1", State: "removed"},
	}

	if err := m.RestoreAll(ctx); err != nil {
		t.Fatalf("RestoreAll err = %v", err)
	}
	wantPrefix := []string{"Restore:__root__", "Restore:api"}
	if got := ws.calls[:2]; strings.Join(got, ",") != strings.Join(wantPrefix, ",") {
		t.Fatalf("restore prefix = %v, want %v; all calls %v", got, wantPrefix, ws.calls)
	}
	if len(ws.restoreConfigs) != 2 || ws.restoreConfigs[0].BaseRef != "refs/remotes/origin/main" || ws.restoreConfigs[1].BaseRef != "refs/remotes/origin/dev" {
		t.Fatalf("restore base refs = %#v, want root main and api dev", ws.restoreConfigs)
	}
	applied := strings.Join(ws.calls, ",")
	if !strings.Contains(applied, "ApplyPreserved:__root__:refs/ao/preserved/mer-1") ||
		!strings.Contains(applied, "ApplyPreserved:api:refs/ao/preserved/mer-1") {
		t.Fatalf("apply calls missing, got %v", ws.calls)
	}
	if rt.created != 1 {
		t.Fatalf("runtime.Create calls = %d, want 1", rt.created)
	}
	rows, err := st.ListSessionWorktrees(ctx, "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("workspace project rows after RestoreAll = %v, want root and child inventory", rows)
	}
	states := map[string]string{}
	for _, row := range rows {
		states[row.RepoName] = row.State
		if row.PreservedRef != "" {
			t.Fatalf("row %s preserved_ref = %q, want consumed", row.RepoName, row.PreservedRef)
		}
	}
	if states[domain.RootWorkspaceRepoName] != "active" || states["api"] != "active" {
		t.Fatalf("workspace project row states = %v, want active inventory", states)
	}
}

func TestRestoreAll_WorkspaceProjectRootOnlyMarkerRestoresRegisteredChildren(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newLifecycleManager()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Kind: domain.ProjectKindWorkspace, Config: testRoleAgents()}
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{Name: "api", RelativePath: "api"}}
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:           "mer-1",
		ProjectID:    "mer",
		Kind:         domain.KindWorker,
		Harness:      domain.HarnessClaudeCode,
		IsTerminated: true,
		Metadata:     domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1", AgentSessionID: "agent-w"},
		Activity:     domain.Activity{State: domain.ActivityExited},
	}
	st.worktrees["mer-1"] = []domain.SessionWorktreeRecord{{
		SessionID:    "mer-1",
		RepoName:     domain.RootWorkspaceRepoName,
		Branch:       "tp/mer-1",
		WorktreePath: "/ws/mer-1",
		PreservedRef: "refs/ao/preserved/root",
		State:        "removed",
	}}

	if err := m.RestoreAll(ctx); err != nil {
		t.Fatalf("RestoreAll err = %v", err)
	}
	wantPrefix := []string{"Restore:__root__", "Restore:api"}
	if got := ws.calls[:2]; strings.Join(got, ",") != strings.Join(wantPrefix, ",") {
		t.Fatalf("restore prefix = %v, want %v; all calls %v", got, wantPrefix, ws.calls)
	}
	applied := strings.Join(ws.calls, ",")
	if !strings.Contains(applied, "ApplyPreserved:__root__:refs/ao/preserved/root") {
		t.Fatalf("root preserved ref was not applied; calls=%v", ws.calls)
	}
	if rt.created != 1 {
		t.Fatalf("runtime.Create calls = %d, want 1", rt.created)
	}
	rows, err := st.ListSessionWorktrees(ctx, "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("workspace project rows after RestoreAll = %v, want root and registered child", rows)
	}
	states := map[string]string{}
	for _, row := range rows {
		states[row.RepoName] = row.State
		if row.PreservedRef != "" {
			t.Fatalf("row %s preserved_ref = %q, want consumed", row.RepoName, row.PreservedRef)
		}
	}
	if states[domain.RootWorkspaceRepoName] != "active" || states["api"] != "active" {
		t.Fatalf("workspace project row states = %v, want active root and child", states)
	}
}
