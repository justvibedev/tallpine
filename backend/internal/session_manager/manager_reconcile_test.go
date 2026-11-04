package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestReconcileLive_DeadSessionRelaunchesInExistingWorktree(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["p1"] = domain.ProjectRecord{ID: "p1", Config: testRoleAgents()}
	rt := &fakeRuntime{aliveByHandle: map[string]bool{}} // handle not alive
	ws := &fakeWorkspace{stashRef: "refs/ao/preserved/s1"}
	lcm := &fakeLCM{store: st}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: rt, Agents: fakeAgents{}, Workspace: ws, Store: st, Messenger: &fakeMessenger{}, Lifecycle: lcm, LookPath: lookPath})

	rec := domain.SessionRecord{
		ID:           "s1",
		ProjectID:    "p1",
		Harness:      domain.HarnessClaudeCode,
		IsTerminated: false,
		Metadata: domain.SessionMetadata{
			Branch: "tp/s1/root", WorkspacePath: "/wt/s1", RuntimeHandleID: "s1", AgentSessionID: "agent-s1",
		},
	}
	st.sessions[rec.ID] = rec

	if err := m.reconcileLive(context.Background(), rec); err != nil {
		t.Fatalf("reconcileLive: %v", err)
	}
	if ws.stashCalls != 0 {
		t.Fatalf("StashUncommitted calls = %d, want 0", ws.stashCalls)
	}
	if lcm.terminated["s1"] != 0 {
		t.Fatalf("MarkTerminated(s1) = %d, want 0", lcm.terminated["s1"])
	}
	if rt.created != 1 {
		t.Fatalf("Create calls = %d, want 1", rt.created)
	}
	if st.sessions["s1"].IsTerminated {
		t.Fatal("reconciled session must remain live")
	}
	if len(ws.restoreConfigs) != 1 || ws.restoreConfigs[0].Path != "/wt/s1" {
		t.Fatalf("Restore configs = %+v, want the existing worktree path", ws.restoreConfigs)
	}
	// B190: a non-Chat session reaches Restore only once its runtime is dead.
	if !ws.restoreConfigs[0].RefreshOrchestrator {
		t.Fatal("dead terminal session restore did not opt in to the worktree refresh")
	}
	for _, c := range ws.calls {
		if c == "ForceDestroy:s1" {
			t.Fatalf("reconcileLive must preserve the existing worktree; calls = %v", ws.calls)
		}
	}
	if rows := st.worktrees["s1"]; len(rows) != 0 {
		t.Fatalf("in-place recovery must not create a restore marker; got %+v", rows)
	}
}

func TestReconcileLive_PreservesScopedShellTerminalsWithExistingWorktree(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["p1"] = domain.ProjectRecord{ID: "p1", Config: testRoleAgents()}
	rt := &fakeRuntime{aliveByHandle: map[string]bool{}} // handle not alive
	ws := &fakeWorkspace{stashRef: "refs/ao/preserved/s1"}
	var sharedLog []string
	ws.sharedLog = &sharedLog
	lcm := &fakeLCM{store: st}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: rt, Agents: fakeAgents{}, Workspace: ws, Store: st, Messenger: &fakeMessenger{}, Lifecycle: lcm, LookPath: lookPath})
	closer := &fakeShellTerminalCloser{sharedLog: &sharedLog}
	m.SetShellTerminalCloser(closer)

	rec := domain.SessionRecord{
		ID:           "s1",
		ProjectID:    "p1",
		Harness:      domain.HarnessClaudeCode,
		IsTerminated: false,
		Metadata: domain.SessionMetadata{
			Branch: "tp/s1/root", WorkspacePath: "/wt/s1", RuntimeHandleID: "s1", AgentSessionID: "agent-s1",
		},
	}
	st.sessions[rec.ID] = rec

	if err := m.reconcileLive(context.Background(), rec); err != nil {
		t.Fatalf("reconcileLive: %v", err)
	}

	if len(closer.began) != 0 {
		t.Fatalf("began = %v, want no shell teardown", closer.began)
	}
	if len(closer.ended) != 0 {
		t.Fatalf("ended = %v, want no shell teardown", closer.ended)
	}
	if strings.Contains(strings.Join(sharedLog, ","), "ForceDestroy:s1") {
		t.Fatalf("existing worktree must not be destroyed; call log = %v", sharedLog)
	}
}

func TestReconcileLive_RelaunchFailureLeavesSessionExitedAndRecoverable(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["p1"] = domain.ProjectRecord{ID: "p1", Config: testRoleAgents()}
	rt := &fakeRuntime{aliveByHandle: map[string]bool{}}
	ws := &fakeWorkspace{
		createErr: errors.New("worktree cannot be reattached"),
		stashRef:  "refs/ao/preserved/s1",
	}
	lcm := &fakeLCM{store: st}
	m := New(Deps{
		Runtime: rt, Agents: fakeAgents{}, Workspace: ws, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: lcm,
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	rec := domain.SessionRecord{
		ID: "s1", ProjectID: "p1", Harness: domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{
			Branch: "tp/s1/root", WorkspacePath: "/wt/s1", RuntimeHandleID: "old", AgentSessionID: "agent-s1",
		},
	}
	st.sessions[rec.ID] = rec

	if err := m.reconcileLive(context.Background(), rec); err == nil || !strings.Contains(err.Error(), "worktree cannot be reattached") {
		t.Fatalf("reconcileLive error = %v, want relaunch failure", err)
	}
	got := st.sessions[rec.ID]
	if got.IsTerminated || got.Activity.State != domain.ActivityExited {
		t.Fatalf("failed relaunch session = %+v, want live/exited", got)
	}
	if ws.stashCalls != 0 || lcm.terminated[rec.ID] != 0 {
		t.Fatalf("failed relaunch changed durable lifecycle: stash=%d terminated=%d", ws.stashCalls, lcm.terminated[rec.ID])
	}
	for _, call := range ws.calls {
		if call == "ForceDestroy:s1" {
			t.Fatalf("failed relaunch destroyed recoverable worktree; calls=%v", ws.calls)
		}
	}
	if rows := st.worktrees[rec.ID]; len(rows) != 0 {
		t.Fatalf("failed relaunch wrote shutdown restore markers: %+v", rows)
	}
}

func TestReconcileLive_RuntimeFailureAfterCapabilityUpdateLeavesSessionResumable(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["p1"] = domain.ProjectRecord{ID: "p1", Config: testRoleAgents()}
	rt := &fakeRuntime{
		createErr:     errors.New("runtime create failed"),
		aliveByHandle: map[string]bool{"old": false},
	}
	ws := &fakeWorkspace{}
	lcm := &fakeLCM{store: st}
	bootUpdatedAt := time.Date(2026, time.August, 27, 16, 54, 0, 0, time.UTC)
	launchUpdatedAt := bootUpdatedAt.Add(time.Second)
	m := New(Deps{
		Runtime: rt, Agents: fakeAgents{}, Workspace: ws, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: lcm,
		BrowserCapabilities: fixedBrowserCapability("capability-1"),
		Clock:               func() time.Time { return launchUpdatedAt },
		LookPath:            func(string) (string, error) { return "/bin/true", nil },
	})
	rec := domain.SessionRecord{
		ID: "s1", ProjectID: "p1", Harness: domain.HarnessCodex,
		Activity:  domain.Activity{State: domain.ActivityActive, LastActivityAt: bootUpdatedAt},
		UpdatedAt: bootUpdatedAt,
		Metadata: domain.SessionMetadata{
			Branch: "tp/s1/root", WorkspacePath: "/wt/s1", RuntimeHandleID: "old",
			RuntimeLaunchID: "old-launch", AgentSessionID: "native-conversation-1",
		},
	}
	st.sessions[rec.ID] = rec

	err := m.reconcileLive(context.Background(), rec)
	if err == nil || !strings.Contains(err.Error(), "runtime create failed") {
		t.Fatalf("reconcileLive error = %v, want runtime create failure", err)
	}
	failed := st.sessions[rec.ID]
	if failed.IsTerminated || failed.Activity.State != domain.ActivityExited {
		t.Fatalf("failed relaunch session = %+v, want live/exited", failed)
	}
	if failed.Metadata.BrowserCapabilityVerifier != "verifier-1" {
		t.Fatalf("browser capability verifier = %q, want launch metadata persisted", failed.Metadata.BrowserCapabilityVerifier)
	}
	if !failed.UpdatedAt.Equal(bootUpdatedAt) {
		t.Fatalf("UpdatedAt = %v, want preserved recency %v", failed.UpdatedAt, bootUpdatedAt)
	}
	if failed.Metadata.AgentSessionID != "native-conversation-1" || failed.Metadata.WorkspacePath != "/wt/s1" {
		t.Fatalf("native identity/worktree changed after failed relaunch: %+v", failed.Metadata)
	}

	// The failed startup attempt must land in the ordinary Resume Agent state
	// without treating capability rotation as user-visible session activity.
	rt.createErr = nil
	if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); err != nil {
		t.Fatalf("ResumeAgentWithMode after dependency recovery: %v", err)
	}
	resumed := st.sessions[rec.ID]
	if resumed.IsTerminated || resumed.Metadata.AgentSessionID != "native-conversation-1" {
		t.Fatalf("resumed session lost durable identity: %+v", resumed)
	}
}

func TestPreserveFailedReconcileRelaunchRetriesContendedCAS(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	now := time.Date(2026, time.August, 27, 16, 54, 0, 0, time.UTC)
	rec := domain.SessionRecord{
		ID: "s1", Activity: domain.Activity{State: domain.ActivityActive}, UpdatedAt: now,
		Metadata: domain.SessionMetadata{RuntimeHandleID: "old", RuntimeLaunchID: "old-launch"},
	}
	st.sessions[rec.ID] = rec
	lcm := &contendedActivityLCM{fakeLCM: &fakeLCM{store: st}}
	lcm.before = func(call int, id domain.SessionID, signal ports.ActivitySignal) {
		if call != 1 {
			return
		}
		current := st.sessions[id]
		current.Revision = *signal.ExpectedRevision + 1
		st.sessions[id] = current
	}
	m := New(Deps{Store: st, Lifecycle: lcm})

	committed, err := m.preserveFailedReconcileRelaunch(context.Background(), rec)
	if err != nil {
		t.Fatalf("preserveFailedReconcileRelaunch: %v", err)
	}
	if committed {
		t.Fatal("failed controller was reported committed")
	}
	if lcm.calls != 2 {
		t.Fatalf("ApplyActivitySignal calls = %d, want retry after one CAS loss", lcm.calls)
	}
	if got := st.sessions[rec.ID].Activity.State; got != domain.ActivityExited {
		t.Fatalf("activity = %q, want exited after retry", got)
	}
}

func TestPreserveFailedReconcileRelaunchBoundsPersistentCASContention(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	now := time.Date(2026, time.August, 27, 16, 54, 0, 0, time.UTC)
	rec := domain.SessionRecord{
		ID: "s1", Activity: domain.Activity{State: domain.ActivityActive}, UpdatedAt: now,
		Metadata: domain.SessionMetadata{RuntimeHandleID: "old", RuntimeLaunchID: "old-launch"},
	}
	st.sessions[rec.ID] = rec
	lcm := &contendedActivityLCM{fakeLCM: &fakeLCM{store: st}}
	lcm.before = func(_ int, id domain.SessionID, signal ports.ActivitySignal) {
		current := st.sessions[id]
		current.Revision = *signal.ExpectedRevision + 1
		st.sessions[id] = current
	}
	m := New(Deps{Store: st, Lifecycle: lcm})

	committed, err := m.preserveFailedReconcileRelaunch(context.Background(), rec)
	if err == nil || !strings.Contains(err.Error(), "session changed") {
		t.Fatalf("preserveFailedReconcileRelaunch error = %v, want bounded contention error", err)
	}
	if committed {
		t.Fatal("contended failed controller was reported committed")
	}
	if lcm.calls != 3 {
		t.Fatalf("ApplyActivitySignal calls = %d, want bounded 3 attempts", lcm.calls)
	}
	if got := st.sessions[rec.ID].Activity.State; got != domain.ActivityActive {
		t.Fatalf("activity = %q, want unchanged after persistent contention", got)
	}
}

func TestReconcileLive_DoesNotTeardownAfterUncertainRelaunchCommit(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["p1"] = domain.ProjectRecord{ID: "p1", Config: testRoleAgents()}
	rt := &fakeRuntime{aliveByHandle: map[string]bool{}}
	ws := &fakeWorkspace{stashRef: "refs/ao/preserved/s1"}
	lcm := &fakeLCM{store: st}
	m := New(Deps{
		Runtime: rt, Agents: fakeAgents{}, Workspace: ws, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: lcm,
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	rec := domain.SessionRecord{
		ID: "s1", ProjectID: "p1", Harness: domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{
			Branch: "tp/s1/root", WorkspacePath: "/wt/s1", RuntimeHandleID: "old", AgentSessionID: "agent-s1",
		},
	}
	st.sessions[rec.ID] = rec
	// MarkSpawned commits through the lifecycle fake, then relaunchSession reads
	// the row back. Model storage becoming unavailable only at that final read.
	st.getSessionErr = errors.New("readback unavailable")

	err := m.reconcileLive(context.Background(), rec)
	if err == nil {
		t.Fatal("reconcileLive must report uncertain durable controller state")
	}
	if rt.created != 1 {
		t.Fatalf("Create calls = %d, want 1 controller launched before readback failed", rt.created)
	}
	if ws.stashCalls != 0 || lcm.terminated[rec.ID] != 0 {
		t.Fatalf("uncertain live controller must keep its worktree intact: stash=%d terminated=%d", ws.stashCalls, lcm.terminated[rec.ID])
	}
	for _, call := range ws.calls {
		if call == "ForceDestroy:s1" {
			t.Fatalf("uncertain live controller must not lose its worktree; calls=%v", ws.calls)
		}
	}
}

func TestReconcileLive_AliveSessionAdoptedNoop(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	rt := &fakeRuntime{aliveByHandle: map[string]bool{"s2": true}}
	ws := &fakeWorkspace{}
	lcm := &fakeLCM{store: st}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: rt, Agents: fakeAgents{}, Workspace: ws, Store: st, Messenger: &fakeMessenger{}, Lifecycle: lcm, LookPath: lookPath})

	rec := domain.SessionRecord{
		ID: "s2", ProjectID: "p1", IsTerminated: false,
		Metadata: domain.SessionMetadata{Branch: "tp/s2/root", WorkspacePath: "/wt/s2", RuntimeHandleID: "s2"},
	}

	if err := m.reconcileLive(context.Background(), rec); err != nil {
		t.Fatalf("reconcileLive: %v", err)
	}
	if ws.stashCalls != 0 || lcm.terminated["s2"] != 0 || rt.destroyed != 0 {
		t.Fatalf("adopt should be a no-op: stash=%d term=%d destroy=%d", ws.stashCalls, lcm.terminated["s2"], rt.destroyed)
	}
}

// TestReconcileLive_InterruptedSpawnSeedRowIsRemoved covers a daemon that died
// between Spawn's CreateSession and its MarkSpawned commit (e.g. the desktop
// app was closed while "Preparing the worker terminal" was still creating the
// workspace). The seed row has no workspace and never will: the next daemon
// must remove it instead of leaving a non-terminated phantom in the sidebar
// forever, since nothing observable (worktree, runtime) was ever built.
func TestReconcileLive_InterruptedSpawnSeedRowIsRemoved(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["p1"] = domain.ProjectRecord{ID: "p1", Config: testRoleAgents()}
	rec := domain.SessionRecord{
		ID: "s-interrupted", ProjectID: "p1", Harness: domain.HarnessClaudeCode, IsTerminated: false,
	}
	st.sessions[rec.ID] = rec
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: fakeAgents{}, Workspace: &fakeWorkspace{}, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})

	if err := m.reconcileLive(context.Background(), rec); err != nil {
		t.Fatalf("reconcileLive: %v", err)
	}
	if _, ok := st.sessions[rec.ID]; ok {
		t.Fatalf("interrupted seed row should have been removed, still present: %+v", st.sessions[rec.ID])
	}
}

func TestReconcile_LivePassUsesConfiguredConcurrency(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["p1"] = domain.ProjectRecord{ID: "p1", Config: testRoleAgents()}
	ids := []domain.SessionID{"s1", "s2", "s3"}
	for _, id := range ids {
		st.sessions[id] = domain.SessionRecord{
			ID: id, ProjectID: "p1", Harness: domain.HarnessClaudeCode,
			Metadata: domain.SessionMetadata{
				Branch: "tp/" + string(id) + "/root", WorkspacePath: "/wt/" + string(id), RuntimeHandleID: string(id),
			},
		}
	}
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseAll()
	rt := &blockingAliveRuntime{
		fakeRuntime: &fakeRuntime{},
		entered:     make(chan domain.SessionID, 2),
		release:     release,
	}
	m := New(Deps{
		Runtime: rt, Agents: fakeAgents{}, Workspace: &fakeWorkspace{}, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath:         func(string) (string, error) { return "/bin/true", nil },
		ReconcileWorkers: 2,
	})
	done := make(chan error, 1)
	go func() { done <- m.Reconcile(context.Background()) }()

	seen := map[domain.SessionID]bool{}
	for len(seen) < 2 {
		select {
		case id := <-rt.entered:
			seen[id] = true
		case <-time.After(time.Second):
			t.Fatalf("live probes did not overlap; entered = %v", seen)
		}
	}
	// The third session is still queued behind the two blocked probes. It must
	// already be fenced so neither input nor the periodic reaper can mutate it.
	for _, id := range ids {
		if !m.SessionMutationInProgress(id) {
			t.Fatalf("session %s was not lifecycle-fenced during startup reconcile", id)
		}
	}
	releaseAll()
	if err := <-done; err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	for _, id := range ids {
		if m.SessionMutationInProgress(id) {
			t.Fatalf("session %s reconcile fence leaked after completion", id)
		}
	}
}

func TestReconcileStartupSafetyDefersRuntimeReconciliation(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["p1"] = domain.ProjectRecord{ID: "p1", Config: testRoleAgents()}
	st.sessions["s1"] = domain.SessionRecord{
		ID: "s1", ProjectID: "p1", Harness: domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{Branch: "tp/s1/root", WorkspacePath: "/wt/s1", RuntimeHandleID: "s1"},
	}
	release := make(chan struct{})
	rt := &blockingAliveRuntime{
		fakeRuntime: &fakeRuntime{},
		entered:     make(chan domain.SessionID, 1),
		release:     release,
	}
	m := New(Deps{
		Runtime: rt, Agents: fakeAgents{}, Workspace: &fakeWorkspace{}, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})

	if err := m.ReconcileStartupSafety(context.Background()); err != nil {
		t.Fatalf("ReconcileStartupSafety: %v", err)
	}
	select {
	case id := <-rt.entered:
		t.Fatalf("startup safety unexpectedly probed runtime %s", id)
	default:
	}

	done := make(chan error, 1)
	go func() { done <- m.ReconcileBackground(context.Background()) }()
	select {
	case id := <-rt.entered:
		if id != "s1" {
			t.Fatalf("runtime probe = %s, want s1", id)
		}
	case <-time.After(time.Second):
		t.Fatal("background reconciliation did not probe the live runtime")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("ReconcileBackground: %v", err)
	}
}

// TestReconcileLive_ProbeErrorIsNotDeath locks the invariant that a failed
// IsAlive probe is NOT treated as proof that the session is dead. reconcileLive
// must propagate the error and must NOT stash, terminate, or destroy.
func TestReconcileLive_ProbeErrorIsNotDeath(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	rt := &fakeRuntime{aliveErr: errors.New("probe boom")}
	ws := &fakeWorkspace{}
	lcm := &fakeLCM{store: st}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: rt, Agents: fakeAgents{}, Workspace: ws, Store: st, Messenger: &fakeMessenger{}, Lifecycle: lcm, LookPath: lookPath})

	rec := domain.SessionRecord{
		ID:           "s3",
		ProjectID:    "p1",
		IsTerminated: false,
		Metadata: domain.SessionMetadata{
			Branch: "tp/s3/root", WorkspacePath: "/wt/s3", RuntimeHandleID: "s3",
		},
	}

	err := m.reconcileLive(context.Background(), rec)
	if err == nil {
		t.Fatal("reconcileLive: expected non-nil error on probe failure, got nil")
	}
	if ws.stashCalls != 0 {
		t.Fatalf("StashUncommitted calls = %d, want 0 (probe error is not death)", ws.stashCalls)
	}
	if lcm.terminated["s3"] != 0 {
		t.Fatalf("MarkTerminated(s3) = %d, want 0 (probe error is not death)", lcm.terminated["s3"])
	}
	if rt.destroyed != 0 {
		t.Fatalf("Destroy calls = %d, want 0 (probe error is not death)", rt.destroyed)
	}
}

func TestReconcileLive_InconclusiveChatRecoveryDoesNotTeardown(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["p1"] = domain.ProjectRecord{ID: "p1", Config: testRoleAgents()}
	ws := &fakeWorkspace{stashRef: "refs/ao/preserved/chat-live"}
	lcm := &fakeLCM{store: st}
	chat := &recordingLauncher{
		startErr: fmt.Errorf("persistent host unavailable: %w", ports.ErrChatRecoveryInconclusive),
	}
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: fakeAgents{}, Workspace: ws, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: lcm, Chat: chat,
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	rec := domain.SessionRecord{
		ID: "chat-live", ProjectID: "p1", Harness: domain.HarnessCodex,
		Mode: domain.SessionModeChat,
		Metadata: domain.SessionMetadata{
			Branch: "tp/chat-live", WorkspacePath: "/wt/chat-live",
			ProviderConversationID: "thread-live", ControllerGeneration: "generation-old",
		},
	}
	st.sessions[rec.ID] = rec

	err := m.reconcileLive(context.Background(), rec)
	if !errors.Is(err, ports.ErrChatRecoveryInconclusive) {
		t.Fatalf("reconcileLive error = %v, want ErrChatRecoveryInconclusive", err)
	}
	if ws.stashCalls != 0 || lcm.terminated[rec.ID] != 0 {
		t.Fatalf("inconclusive live host must remain intact: stash=%d terminated=%d",
			ws.stashCalls, lcm.terminated[rec.ID])
	}
	for _, call := range ws.calls {
		if call == "ForceDestroy:chat-live" {
			t.Fatalf("inconclusive live host lost its worktree: calls=%v", ws.calls)
		}
	}
}

func TestReconcileLive_InconclusiveRuntimeProbeDoesNotRelaunch(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["p1"] = domain.ProjectRecord{ID: "p1", Config: testRoleAgents()}
	rt := &fakeRuntime{aliveErr: fmt.Errorf("legacy client mismatch: %w", ports.ErrRuntimeProbeInconclusive)}
	ws := &fakeWorkspace{}
	lcm := &fakeLCM{store: st}
	m := New(Deps{
		Runtime: rt, Agents: fakeAgents{}, Workspace: ws, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: lcm,
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	rec := domain.SessionRecord{
		ID: "s-inconclusive", ProjectID: "p1", Harness: domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{
			Branch: "tp/s-inconclusive/root", WorkspacePath: "/wt/s-inconclusive", RuntimeHandleID: "s-inconclusive",
		},
	}
	st.sessions[rec.ID] = rec

	err := m.reconcileLive(context.Background(), rec)
	if !errors.Is(err, ports.ErrRuntimeProbeInconclusive) {
		t.Fatalf("reconcileLive err = %v, want ErrRuntimeProbeInconclusive", err)
	}
	if rt.created != 0 || rt.destroyed != 0 {
		t.Fatalf("inconclusive probe changed runtime: created=%d destroyed=%d", rt.created, rt.destroyed)
	}
	if ws.stashCalls != 0 || lcm.terminated[rec.ID] != 0 {
		t.Fatalf("inconclusive probe changed lifecycle: stash=%d terminated=%d", ws.stashCalls, lcm.terminated[rec.ID])
	}
}

func TestRestartRuntime_InconclusiveProbeDoesNotCreateReplacement(t *testing.T) {
	t.Parallel()

	rt := &fakeRuntime{aliveErr: fmt.Errorf("legacy client unavailable: %w", ports.ErrRuntimeProbeInconclusive)}
	m := New(Deps{Runtime: rt})

	_, err := m.restartRuntime(context.Background(), ports.RuntimeHandle{ID: "legacy-live"}, ports.RuntimeConfig{
		SessionID: "legacy-live",
	})
	if !errors.Is(err, ports.ErrRuntimeProbeInconclusive) {
		t.Fatalf("restartRuntime err = %v, want ErrRuntimeProbeInconclusive", err)
	}
	if rt.created != 0 || rt.destroyed != 0 {
		t.Fatalf("inconclusive probe replaced runtime: created=%d destroyed=%d", rt.created, rt.destroyed)
	}
}

func TestReconcileLive_ScratchDeadRuntimeTerminatesWithoutWorkspaceTeardown(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["scratch"] = domain.ProjectRecord{ID: "scratch", Kind: domain.ProjectKindScratch, Config: testRoleAgents()}
	rt := &fakeRuntime{aliveByHandle: map[string]bool{}}
	ws := &fakeWorkspace{stashRef: "refs/ao/preserved/scratch-1"}
	lcm := &fakeLCM{store: st}
	m := New(Deps{
		Runtime:   rt,
		Agents:    fakeAgents{},
		Workspace: ws,
		Store:     st,
		Messenger: &fakeMessenger{},
		Lifecycle: lcm,
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})
	rec := domain.SessionRecord{
		ID:        "scratch-1",
		ProjectID: "scratch",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessClaudeCode,
		Metadata:  domain.SessionMetadata{WorkspacePath: "/ws/scratch-1", RuntimeHandleID: "dead"},
		Activity:  domain.Activity{State: domain.ActivityActive},
	}
	st.sessions[rec.ID] = rec

	if err := m.reconcileLive(ctx, rec); err != nil {
		t.Fatalf("reconcileLive: %v", err)
	}
	if lcm.terminated["scratch-1"] != 1 {
		t.Fatalf("MarkTerminated = %d, want 1", lcm.terminated["scratch-1"])
	}
	if ws.stashCalls != 0 || len(ws.calls) != 0 {
		t.Fatalf("scratch reconcile must not stash or force destroy, calls=%v stash=%d", ws.calls, ws.stashCalls)
	}
	if rows := st.worktrees["scratch-1"]; len(rows) != 0 {
		t.Fatalf("scratch reconcile must not write restore markers, got %#v", rows)
	}
}

func TestReconcileLive_ScratchChatReattachesPersistentController(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["scratch"] = domain.ProjectRecord{
		ID: "scratch", Kind: domain.ProjectKindScratch, Config: testRoleAgents(),
	}
	launcher := &recordingLauncher{}
	lcm := &fakeLCM{store: st}
	m := New(Deps{
		Runtime:   &fakeRuntime{},
		Agents:    fakeAgents{},
		Workspace: &fakeWorkspace{},
		Store:     st,
		Messenger: &fakeMessenger{},
		Lifecycle: lcm,
		Chat:      launcher,
		DataDir:   "/tallpine-test-data",
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})
	rec := domain.SessionRecord{
		ID: "scratch-chat", ProjectID: "scratch", Kind: domain.KindWorker,
		Harness: domain.AgentHarness("cursor"), Mode: domain.SessionModeChat,
		Metadata: domain.SessionMetadata{
			WorkspacePath: "/ws/scratch-chat", ProviderConversationID: "cursor-thread",
			ControllerGeneration: "generation-old",
		},
		Activity: domain.Activity{State: domain.ActivityActive},
	}
	st.sessions[rec.ID] = rec

	if err := m.reconcileLive(ctx, rec); err != nil {
		t.Fatalf("reconcileLive: %v", err)
	}
	if lcm.terminated[rec.ID] != 0 {
		t.Fatalf("MarkTerminated = %d, want 0", lcm.terminated[rec.ID])
	}
	if len(launcher.started) != 1 {
		t.Fatalf("StartChat calls = %d, want 1", len(launcher.started))
	}
	started := launcher.started[0]
	if started.ProviderConversationID != "cursor-thread" || started.WorkspacePath != "/ws/scratch-chat" {
		t.Fatalf("StartChat = %#v, want durable Cursor conversation and scratch workspace", started)
	}
}

// TestReconcile_AdoptAcrossDaemonRestart is the end-to-end durability proof for
// #2335: it drives the full boot-time Reconcile pass over the exact mix of
// session states a daemon restart/upgrade leaves behind and asserts agent
// sessions are decoupled from the daemon's lifetime:
//
//   - an alive orchestrator is ADOPTED in place: same id, still live, its runtime
//     never torn down, and NO new session minted (the id-increment regression
//     guard: adoption failure used to mint a fresh orchestrator id 14->15->16).
//   - an alive worker is adopted as a no-op.
//   - a worker whose runtime died with the daemon is relaunched in its existing
//     worktree on this same boot under its ORIGINAL id.
//   - a truly-dead session with no restore marker is NOT resurrected.
func TestReconcile_AdoptAcrossDaemonRestart(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rt := &fakeRuntime{aliveByHandle: map[string]bool{
		"orch":    true, // orchestrator runtime survived the daemon exit
		"w-alive": true, // worker runtime survived the daemon exit
		// "w-dead" is absent -> that worker's runtime died with the daemon.
	}}
	ws := &fakeWorkspace{stashRef: "refs/ao/preserved/mer-3"}
	lcm := &fakeLCM{store: st}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: rt, Agents: fakeAgents{}, Workspace: ws, Store: st, Messenger: &fakeMessenger{}, Lifecycle: lcm, LookPath: lookPath})

	// Alive orchestrator: the promptless session whose adoption failure used to
	// mint a fresh orchestrator id. It must be adopted in place.
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator, Harness: domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{Branch: "tp/mer-1/root", WorkspacePath: "/ws/mer-1", RuntimeHandleID: "orch"},
	}
	// Alive worker: adopted as a no-op.
	st.sessions["mer-2"] = domain.SessionRecord{
		ID: "mer-2", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{Branch: "tp/mer-2/root", WorkspacePath: "/ws/mer-2", RuntimeHandleID: "w-alive", AgentSessionID: "agent-2"},
	}
	// Dead worker: its runtime died with the daemon; relaunch under the same id
	// without tearing down the worktree first.
	st.sessions["mer-3"] = domain.SessionRecord{
		ID: "mer-3", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{Branch: "tp/mer-3/root", WorkspacePath: "/ws/mer-3", RuntimeHandleID: "w-dead", AgentSessionID: "agent-3"},
	}
	// Truly-dead session the user killed before restart (terminated, no marker).
	st.sessions["mer-4"] = domain.SessionRecord{
		ID: "mer-4", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		IsTerminated: true, Activity: domain.Activity{State: domain.ActivityExited},
		Metadata: domain.SessionMetadata{Branch: "tp/mer-4/root", WorkspacePath: "/ws/mer-4"},
	}

	if err := m.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// Alive orchestrator + worker adopted in place: same id, still live.
	if st.sessions["mer-1"].IsTerminated {
		t.Fatal("alive orchestrator must be adopted in place, not terminated")
	}
	if st.sessions["mer-2"].IsTerminated {
		t.Fatal("alive worker must be adopted in place, not terminated")
	}
	// No id increment: Reconcile must never mint a new session row.
	if st.num != 0 {
		t.Fatalf("Reconcile minted %d new session(s); adoption must reuse existing ids", st.num)
	}
	// Adopted runtimes were never torn down.
	if rt.destroyed != 0 {
		t.Fatalf("adopted sessions must not be destroyed; Destroy called %d times", rt.destroyed)
	}
	// A dead worker stays stopped until an explicit Resume request.
	if lcm.terminated["mer-3"] != 0 {
		t.Fatalf("dead worker must not be marked terminated before relaunch; got %d", lcm.terminated["mer-3"])
	}
	if st.sessions["mer-3"].IsTerminated {
		t.Fatal("dead worker must remain resumable after Reconcile")
	}
	if rt.created != 0 {
		t.Fatalf("health checks must not create runtimes; got %d", rt.created)
	}
	if st.sessions["mer-3"].Activity.State != domain.ActivityExited {
		t.Fatal("dead worker must be recorded as exited")
	}
	if ws.stashCalls != 0 {
		t.Fatalf("dead worker must reuse its worktree without stashing; got %d stash calls", ws.stashCalls)
	}
	// In-place recovery needs no one-shot restore marker.
	if rows := st.worktrees["mer-3"]; len(rows) != 0 {
		t.Fatalf("restore marker for mer-3 must be deleted after relaunch; got %+v", rows)
	}
	// Truly-dead, unmarked session is NOT resurrected.
	if !st.sessions["mer-4"].IsTerminated {
		t.Fatal("terminated session with no restore marker must stay terminated")
	}
}

func TestReconcileReap_TerminatedButAliveTmuxDestroyed(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	rt := &fakeRuntime{aliveByHandle: map[string]bool{"t1": true}}
	ws := &fakeWorkspace{}
	lcm := &fakeLCM{store: st}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: rt, Agents: fakeAgents{}, Workspace: ws, Store: st, Messenger: &fakeMessenger{}, Lifecycle: lcm, LookPath: lookPath})

	rec := domain.SessionRecord{
		ID: "t1", ProjectID: "p1", IsTerminated: true,
		Metadata: domain.SessionMetadata{RuntimeHandleID: "t1"},
	}

	if err := m.reconcileReap(context.Background(), rec); err != nil {
		t.Fatalf("reconcileReap: %v", err)
	}
	if len(rt.destroyedIDs) != 1 || rt.destroyedIDs[0] != "t1" {
		t.Fatalf("destroyedIDs = %v, want [t1]", rt.destroyedIDs)
	}
}

func TestReconcileReap_TerminatedAndDeadTmuxLeftAlone(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	rt := &fakeRuntime{aliveByHandle: map[string]bool{}} // t2 not alive
	ws := &fakeWorkspace{}
	lcm := &fakeLCM{store: st}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: rt, Agents: fakeAgents{}, Workspace: ws, Store: st, Messenger: &fakeMessenger{}, Lifecycle: lcm, LookPath: lookPath})

	rec := domain.SessionRecord{
		ID: "t2", ProjectID: "p1", IsTerminated: true,
		Metadata: domain.SessionMetadata{RuntimeHandleID: "t2"},
	}
	if err := m.reconcileReap(context.Background(), rec); err != nil {
		t.Fatalf("reconcileReap: %v", err)
	}
	if rt.destroyed != 0 {
		t.Fatalf("Destroy calls = %d, want 0", rt.destroyed)
	}
}

// --- Send activity-confirmation tests (issue #2342) ---
