package sessionmanager

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/adapters/workspace/scratch"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestRestoreHarnessGateRejectsCodexMaintenance(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newManager()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Harness: domain.HarnessCodex, IsTerminated: true,
	}
	gate := &rejectingHarnessUseGate{}
	m.SetHarnessUseGate(gate)

	if _, err := m.RestoreWithMode(context.Background(), "mer-1"); !errors.Is(err, ErrHarnessInstallActive) {
		t.Fatalf("RestoreWithMode error = %v, want ErrHarnessInstallActive", err)
	}
	if gate.harness != domain.HarnessCodex {
		t.Fatalf("gated harness = %q, want codex", gate.harness)
	}
}

func TestResumeHarnessGateRejectsCodexMaintenance(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newManager()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Harness: domain.HarnessCodex,
	}
	gate := &rejectingHarnessUseGate{}
	m.SetHarnessUseGate(gate)

	if _, err := m.ResumeAgentWithMode(context.Background(), "mer-1"); !errors.Is(err, ErrHarnessInstallActive) {
		t.Fatalf("ResumeAgentWithMode error = %v, want ErrHarnessInstallActive", err)
	}
	if gate.harness != domain.HarnessCodex {
		t.Fatalf("gated harness = %q, want codex", gate.harness)
	}
}

func TestRestore_RotatesSupervisedAgentGeneration(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "agent-x", RuntimeLaunchID: "launch-old"})
	rec := st.sessions["mer-1"]
	rec.Harness = domain.HarnessCodex
	st.sessions["mer-1"] = rec
	rt := &fakeRuntime{}
	agent := supervisedLaunchAgent{launchArgvAgent{argv: []string{"codex", "resume", "agent-x"}}}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath:    func(string) (string, error) { return "/bin/true", nil },
		Executable:  func() (string, error) { return "/opt/tallpine", nil },
		NewLaunchID: func() string { return "launch-new" },
	})

	result, err := m.RestoreWithMode(ctx, "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Session.Metadata.RuntimeLaunchID != "launch-new" {
		t.Fatalf("restored launch id = %q, want launch-new", result.Session.Metadata.RuntimeLaunchID)
	}
	if result.Session.Metadata.AgentSessionIDLaunchID != "launch-new" {
		t.Fatalf("restored native identity launch = %q, want launch-new", result.Session.Metadata.AgentSessionIDLaunchID)
	}
	if got := rt.lastCfg.Env[EnvRuntimeLaunchID]; got != "launch-new" {
		t.Fatalf("restored launch env = %q, want launch-new", got)
	}
	wantArgv := []string{"/opt/tallpine", "agent-process", "supervise", "--session", "mer-1", "--launch", "launch-new", "--", "codex", "resume", "agent-x"}
	if !reflect.DeepEqual(rt.lastCfg.Argv, wantArgv) {
		t.Fatalf("restored runtime argv = %#v, want %#v", rt.lastCfg.Argv, wantArgv)
	}
}

func TestExitAgentStopsOnlyControllerAndPreservesSessionIdentity(t *testing.T) {
	t.Parallel()

	m, st, runtime, _ := newManager()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessCodex,
		Activity:  domain.Activity{State: domain.ActivityIdle},
		Metadata: domain.SessionMetadata{
			WorkspacePath:   "/ws/mer-1",
			Branch:          "tp/mer-1",
			RuntimeHandleID: "tmux-mer-1",
			RuntimeLaunchID: "launch-current",
			AgentSessionID:  "native-thread-1",
		},
	}
	runtime.aliveByHandle = map[string]bool{"tmux-mer-1": true}
	var releasedAtDestroy string
	runtime.onDestroy = func(int, ports.RuntimeHandle) {
		releasedAtDestroy = st.sessions["mer-1"].Metadata.RuntimeReleasedLaunchID
	}

	got, err := m.ExitAgent(ctx, "mer-1")
	if err != nil {
		t.Fatalf("ExitAgent: %v", err)
	}
	if reason, _ := st.SessionAgentExitReason(ctx, "mer-1"); reason != domain.AgentExitUser {
		t.Fatalf("exit reason = %q, want user so restarts keep it stopped", reason)
	}
	if runtime.destroyed != 1 || !reflect.DeepEqual(runtime.destroyedIDs, []string{"tmux-mer-1"}) {
		t.Fatalf("runtime teardown = %d %v", runtime.destroyed, runtime.destroyedIDs)
	}
	if got.IsTerminated || got.Activity.State != domain.ActivityExited {
		t.Fatalf("exited session = %+v", got)
	}
	if got.Metadata.WorkspacePath != "/ws/mer-1" || got.Metadata.RuntimeHandleID != "tmux-mer-1" ||
		got.Metadata.RuntimeLaunchID != "launch-current" || got.Metadata.AgentSessionID != "native-thread-1" {
		t.Fatalf("exit changed resumable identity: %+v", got.Metadata)
	}
	// The release is recorded before the terminal goes, so the reaper never
	// sees the dead runtime as a crash and ends the session.
	if got.Metadata.RuntimeReleasedLaunchID != "launch-current" {
		t.Fatalf("released launch = %q, want launch-current", got.Metadata.RuntimeReleasedLaunchID)
	}
	if releasedAtDestroy != "launch-current" {
		t.Fatalf("released launch when the runtime was destroyed = %q, want it recorded first", releasedAtDestroy)
	}
}

func TestResumeAgent_RestartsRuntimeWithManagedGeneration(t *testing.T) {
	t.Parallel()

	baseRuntime := &fakeRuntime{aliveByHandle: map[string]bool{"tmux-mer-1": true}}
	runtime := &fakeRestartRuntime{fakeRuntime: baseRuntime}
	agent := supervisedLaunchAgent{launchArgvAgent{argv: []string{"codex", "resume", "agent-x"}}}
	m, st, ws := newExitedResumeManager(t, runtime, agent)
	lcm := m.lcm.(*fakeLCM)
	runtime.onRestart = func() {
		if !reflect.DeepEqual(lcm.prepared, []string{"mer-1:launch-new"}) {
			t.Fatalf("runtime restarted before lifecycle prepared generation: %v", lcm.prepared)
		}
	}

	result, err := m.ResumeAgentWithMode(ctx, "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.restarted != 1 || runtime.restartHandle.ID != "tmux-mer-1" {
		t.Fatalf("restart = %d handle=%+v", runtime.restarted, runtime.restartHandle)
	}
	if baseRuntime.created != 0 || baseRuntime.destroyed != 0 {
		t.Fatalf("tmux restart should not recreate runtime: created=%d destroyed=%d", baseRuntime.created, baseRuntime.destroyed)
	}
	if ws.lastCfg.SessionID != "" || len(ws.calls) != 0 {
		t.Fatalf("resume should not restore or recreate workspace: cfg=%+v calls=%v", ws.lastCfg, ws.calls)
	}
	wantArgv := []string{"/opt/tallpine", "agent-process", "supervise", "--session", "mer-1", "--launch", "launch-new", "--", "codex", "resume", "agent-x"}
	if !reflect.DeepEqual(baseRuntime.lastCfg.Argv, wantArgv) {
		t.Fatalf("resumed runtime argv = %#v, want %#v", baseRuntime.lastCfg.Argv, wantArgv)
	}
	if got := baseRuntime.lastCfg.Env[EnvRuntimeLaunchID]; got != "launch-new" {
		t.Fatalf("runtime launch env = %q, want launch-new", got)
	}
	got := st.sessions["mer-1"]
	if got.IsTerminated || got.Activity.State != domain.ActivityIdle {
		t.Fatalf("resumed session = %+v, want live idle", got)
	}
	if got.Metadata.RuntimeHandleID != "tmux-mer-1" || got.Metadata.RuntimeLaunchID != "launch-new" {
		t.Fatalf("resumed metadata = %+v", got.Metadata)
	}
	if got.Metadata.AgentSessionIDLaunchID != "launch-new" {
		t.Fatalf("native Codex resume identity launch = %q, want launch-new", got.Metadata.AgentSessionIDLaunchID)
	}
	if result.Mode != RestoreModeNative {
		t.Fatalf("resume mode = %q, want native", result.Mode)
	}
	if !reflect.DeepEqual(lcm.cancelled, []string{"mer-1:launch-new"}) {
		t.Fatalf("launch cleanup = %v, want new generation released", lcm.cancelled)
	}
}

func TestResumeAgent_FallsBackToRuntimeRecreateWithoutRestartCapability(t *testing.T) {
	t.Parallel()

	runtime := &fakeRuntime{aliveByHandle: map[string]bool{"tmux-mer-1": true}}
	agent := supervisedLaunchAgent{launchArgvAgent{argv: []string{"codex", "resume", "agent-x"}}}
	m, st, _ := newExitedResumeManager(t, runtime, agent)

	if _, err := m.ResumeAgentWithMode(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if runtime.destroyed != 1 || runtime.created != 1 || !reflect.DeepEqual(runtime.destroyedIDs, []string{"tmux-mer-1"}) {
		t.Fatalf("fallback runtime lifecycle: created=%d destroyed=%d ids=%v", runtime.created, runtime.destroyed, runtime.destroyedIDs)
	}
	if got := st.sessions["mer-1"].Metadata.RuntimeHandleID; got != "h1" {
		t.Fatalf("fallback runtime handle = %q, want h1", got)
	}
}

func TestResumeAgent_RequiresLiveExitedSession(t *testing.T) {
	t.Parallel()

	runtime := &fakeRuntime{aliveByHandle: map[string]bool{"tmux-mer-1": true}}
	agent := supervisedLaunchAgent{launchArgvAgent{argv: []string{"codex", "resume", "agent-x"}}}
	m, st, _ := newExitedResumeManager(t, runtime, agent)

	rec := st.sessions["mer-1"]
	rec.Activity.State = domain.ActivityIdle
	st.sessions["mer-1"] = rec
	if _, err := m.ResumeAgentWithMode(ctx, "mer-1"); !errors.Is(err, ErrAgentNotExited) {
		t.Fatalf("idle resume error = %v, want ErrAgentNotExited", err)
	}

	rec.Activity.State = domain.ActivityExited
	rec.IsTerminated = true
	st.sessions["mer-1"] = rec
	if _, err := m.ResumeAgentWithMode(ctx, "mer-1"); !errors.Is(err, ErrTerminated) {
		t.Fatalf("terminated resume error = %v, want ErrTerminated", err)
	}
	if runtime.created != 0 || runtime.destroyed != 0 {
		t.Fatalf("invalid resume touched runtime: created=%d destroyed=%d", runtime.created, runtime.destroyed)
	}
}

func TestResumeAgent_RestartFailureLeavesSessionExited(t *testing.T) {
	t.Parallel()

	baseRuntime := &fakeRuntime{aliveByHandle: map[string]bool{"tmux-mer-1": true}}
	runtime := &fakeRestartRuntime{fakeRuntime: baseRuntime, restartErr: errors.New("respawn failed")}
	agent := supervisedLaunchAgent{launchArgvAgent{argv: []string{"codex", "resume", "agent-x"}}}
	m, st, _ := newExitedResumeManager(t, runtime, agent)
	lcm := m.lcm.(*fakeLCM)

	if _, err := m.ResumeAgentWithMode(ctx, "mer-1"); err == nil || !strings.Contains(err.Error(), "respawn failed") {
		t.Fatalf("resume error = %v", err)
	}
	got := st.sessions["mer-1"]
	if got.IsTerminated || got.Activity.State != domain.ActivityExited || got.Metadata.RuntimeLaunchID != "launch-old" {
		t.Fatalf("failed resume changed session: %+v", got)
	}
	if !reflect.DeepEqual(lcm.cancelled, []string{"mer-1:launch-new"}) {
		t.Fatalf("failed launch cleanup = %v, want waiting hooks released", lcm.cancelled)
	}
}

func TestResumeAgent_RefusesTUIWhoseLaunchedAgentIsStillAlive(t *testing.T) {
	t.Parallel()

	baseRuntime := &fakeRuntime{aliveByHandle: map[string]bool{"tmux-mer-1": true}}
	runtime := &fakeRestartRuntime{fakeRuntime: baseRuntime}
	agent := supervisedLaunchAgent{launchArgvAgent{argv: []string{"claude", "--resume", "side-run"}}}
	m, st, _ := newExitedResumeManager(t, runtime, agent)
	// A side `claude -p` in the worktree reported SessionEnd under the current
	// launch, but the exact agent Tallpine launched is still running (#6454).
	delete(baseRuntime.exitedLaunches, "launch-old")
	before := st.sessions["mer-1"]

	if _, err := m.ResumeAgentWithMode(ctx, "mer-1"); !errors.Is(err, ErrAgentNotExited) {
		t.Fatalf("resume error = %v, want ErrAgentNotExited", err)
	}
	if runtime.restarted != 0 || baseRuntime.created != 0 || baseRuntime.destroyed != 0 {
		t.Fatalf("resume replaced a live agent: restarted=%d created=%d destroyed=%d",
			runtime.restarted, baseRuntime.created, baseRuntime.destroyed)
	}
	got := st.sessions["mer-1"]
	if got.Activity.State != domain.ActivityIdle {
		t.Fatalf("refused resume activity = %v, want %v", got.Activity.State, domain.ActivityIdle)
	}
	want := before
	want.Activity = got.Activity
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("refused resume mutated session beyond activity reconciliation: got %+v, want %+v", got, want)
	}
}

func TestResumeAgent_RejectsConcurrentRequest(t *testing.T) {
	t.Parallel()

	baseRuntime := &fakeRuntime{aliveByHandle: map[string]bool{"tmux-mer-1": true}}
	runtime := &blockingRestartRuntime{
		fakeRuntime: baseRuntime,
		entered:     make(chan struct{}),
		release:     make(chan struct{}),
	}
	agent := supervisedLaunchAgent{launchArgvAgent{argv: []string{"codex", "resume", "agent-x"}}}
	m, _, _ := newExitedResumeManager(t, runtime, agent)
	firstDone := make(chan error, 1)
	go func() {
		_, err := m.ResumeAgentWithMode(ctx, "mer-1")
		firstDone <- err
	}()
	<-runtime.entered

	if _, err := m.ResumeAgentWithMode(ctx, "mer-1"); !errors.Is(err, ErrResumeInProgress) {
		t.Fatalf("concurrent resume error = %v, want ErrResumeInProgress", err)
	}
	close(runtime.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first resume: %v", err)
	}
}

func TestResumeAgent_ReleasesInputGateAfterInterfaceTransitionRejection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		activeErr  error
		transition domain.SessionInterfaceTransition
		wantError  string
	}{
		{
			name:      "transition lookup error",
			activeErr: errors.New("transition store unavailable"),
			wantError: "transition store unavailable",
		},
		{
			name: "active transition",
			transition: domain.SessionInterfaceTransition{
				ID: "transition-1", SessionID: "mer-1", Phase: domain.SessionInterfaceTransitionDraining,
			},
			wantError: ErrInterfaceTransitionInProgress.Error(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtime := &fakeRuntime{aliveByHandle: map[string]bool{"tmux-mer-1": true}}
			agent := supervisedLaunchAgent{launchArgvAgent{argv: []string{"codex", "resume", "agent-x"}}}
			manager, store, _ := newExitedResumeManager(t, runtime, agent)
			transitionStore := newTransitionStore()
			transitionStore.projects = store.projects
			transitionStore.sessions = store.sessions
			transitionStore.activeErr = tt.activeErr
			if tt.transition.ID != "" {
				transitionStore.transitions[tt.transition.ID] = tt.transition
			}
			manager.store = transitionStore

			if _, err := manager.ResumeAgentWithMode(ctx, "mer-1"); err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("ResumeAgentWithMode error = %v, want %q", err, tt.wantError)
			}
			if manager.SessionMutationInProgress("mer-1") {
				t.Fatal("interface-transition rejection left input admission closed")
			}
		})
	}
}

func TestRestore_ReopensTerminal(t *testing.T) {
	t.Parallel()

	m, st, rt, _ := newManager()
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "agent-x"})
	s, err := m.RestoreWithMode(ctx, "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if s.Session.Activity.State != domain.ActivityIdle {
		t.Fatalf("restored records idle, got %q", s.Session.Activity.State)
	}
	if rt.created != 1 {
		t.Fatal("restore should relaunch")
	}
}

func TestRestoreRejectsUnauthorizedLaunchContext(t *testing.T) {
	t.Parallel()

	m, st, rt, _ := newManager()
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "agent-x"})
	project := st.projects["mer"]
	project.Config.Env = map[string]string{"ANTHROPIC_BASE_URL": "https://gateway.example"}
	st.projects["mer"] = project
	agent := &launchAuthAgent{recordingAgent: &recordingAgent{}, status: ports.AgentAuthStatusUnauthorized}
	m.agents = singleAgent{agent: agent}

	_, err := m.RestoreWithMode(ctx, "mer-1")
	if !errors.Is(err, ports.ErrAgentAuthRequired) {
		t.Fatalf("restore error = %v, want ErrAgentAuthRequired", err)
	}
	if agent.workingDir != "/ws/mer-1" || agent.env["ANTHROPIC_BASE_URL"] != "https://gateway.example" {
		t.Fatalf("launch auth context = cwd %q env %#v", agent.workingDir, agent.env)
	}
	if rt.created != 0 {
		t.Fatalf("runtime created %d times after rejected auth", rt.created)
	}
}

func TestRestore_RestoresReviewerWithoutTerminating(t *testing.T) {
	t.Parallel()

	m, st, rt, _ := newManager()
	reviewer := &fakeReviewerTerminator{err: errors.New("reviewer still alive")}
	m.SetReviewerTerminator(reviewer)
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "agent-x"})
	rec := st.sessions["mer-1"]
	rec.Kind = domain.KindWorker
	rec.Harness = domain.HarnessClaudeCode
	st.sessions["mer-1"] = rec

	if _, err := m.RestoreWithMode(ctx, "mer-1"); err != nil {
		t.Fatalf("RestoreWithMode: %v", err)
	}
	if rt.created != 1 {
		t.Fatalf("runtime created = %d, want 1", rt.created)
	}
	if len(reviewer.calls) != 0 {
		t.Fatalf("reviewer terminates = %v, want none", reviewer.calls)
	}
	if !reflect.DeepEqual(reviewer.restoreCalls, []domain.SessionID{"mer-1"}) {
		t.Fatalf("reviewer restores = %v, want [mer-1]", reviewer.restoreCalls)
	}
}

func TestRestore_ReviewerRestoreFailureLeavesWorkerRestored(t *testing.T) {
	t.Parallel()

	m, st, rt, _ := newManager()
	reviewer := &fakeReviewerTerminator{restoreErr: errors.New("reviewer unavailable")}
	m.SetReviewerTerminator(reviewer)
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "agent-x"})
	rec := st.sessions["mer-1"]
	rec.Kind = domain.KindWorker
	rec.Harness = domain.HarnessClaudeCode
	st.sessions["mer-1"] = rec

	res, err := m.RestoreWithMode(ctx, "mer-1")
	if err != nil {
		t.Fatalf("RestoreWithMode: %v", err)
	}
	if res.Session.ID != "mer-1" || res.Session.IsTerminated {
		t.Fatalf("restore result session = %+v, want active mer-1", res.Session)
	}
	if rt.created != 1 {
		t.Fatalf("runtime created = %d, want 1", rt.created)
	}
	if !reflect.DeepEqual(reviewer.restoreCalls, []domain.SessionID{"mer-1"}) {
		t.Fatalf("reviewer restores = %v, want [mer-1]", reviewer.restoreCalls)
	}
	if st.sessions["mer-1"].IsTerminated {
		t.Fatal("worker session must remain restored when reviewer restore fails")
	}
}

func TestRestore_ScratchAllowsEmptyBranch(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newManager()
	st.projects["scratch"] = domain.ProjectRecord{ID: "scratch", Kind: domain.ProjectKindScratch, Config: testRoleAgents()}
	st.sessions["scratch-1"] = domain.SessionRecord{
		ID:           "scratch-1",
		ProjectID:    "scratch",
		Kind:         domain.KindWorker,
		Harness:      domain.HarnessClaudeCode,
		IsTerminated: true,
		Activity:     domain.Activity{State: domain.ActivityExited},
		Metadata:     domain.SessionMetadata{WorkspacePath: "/ws/scratch-1", Prompt: "continue"},
	}

	res, err := m.RestoreWithMode(ctx, "scratch-1")
	if err != nil {
		t.Fatalf("Restore scratch: %v", err)
	}
	s := res.Session
	if s.Metadata.Branch != "" {
		t.Fatalf("scratch restore branch = %q, want empty", s.Metadata.Branch)
	}
	if ws.lastCfg.Branch != "" {
		t.Fatalf("workspace restore branch = %q, want empty", ws.lastCfg.Branch)
	}
	if ws.lastCfg.Path != "/ws/scratch-1" {
		t.Fatalf("workspace restore path = %q, want stored scratch workspace path", ws.lastCfg.Path)
	}
	if rt.created != 1 {
		t.Fatalf("runtime created = %d, want 1", rt.created)
	}
}

func TestRestoreRecreatedWorkspaceRunsSetupOnce(t *testing.T) {
	t.Parallel()

	m, _, _, _ := newManager()
	managed := filepath.Join(t.TempDir(), "workspaces")
	adapter, err := scratch.New(scratch.Options{ManagedRoot: managed})
	if err != nil {
		t.Fatal(err)
	}
	m.workspace = adapter
	workspace := filepath.Join(managed, "scratch", "workers", "scratch-1")
	project := domain.ProjectRecord{ID: "scratch", Kind: domain.ProjectKindScratch, Path: t.TempDir(), Config: domain.ProjectConfig{PostCreate: []string{"echo setup >> setup-runs"}}}
	rec := domain.SessionRecord{ID: "scratch-1", ProjectID: "scratch", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: workspace}}
	for i := 0; i < 2; i++ {
		if _, err := m.restoreSessionWorkspace(context.Background(), project, rec, false); err != nil {
			t.Fatalf("restore %d: %v", i+1, err)
		}
	}
	output, err := os.ReadFile(filepath.Join(workspace, "setup-runs"))
	if err != nil || strings.Count(string(output), "setup") != 1 {
		t.Fatalf("setup runs = %q, err = %v", output, err)
	}
}

func TestRestore_WorkspaceProjectRestoresChildrenAndRecordsInventory(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newManager()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Kind: domain.ProjectKindWorkspace, Config: testRoleAgents()}
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{Name: "api", RelativePath: "services/api"}}
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1", AgentSessionID: "agent-x"})

	if _, err := m.RestoreWithMode(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	wantCalls := []string{"Restore:__root__", "Restore:api"}
	if got := strings.Join(ws.calls, ","); got != strings.Join(wantCalls, ",") {
		t.Fatalf("restore calls = %v, want %v", ws.calls, wantCalls)
	}
	if rt.created != 1 {
		t.Fatalf("runtime.Create calls = %d, want 1", rt.created)
	}
	rows, err := st.ListSessionWorktrees(ctx, "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("workspace rows = %v, want root and child inventory", rows)
	}
	byRepo := map[string]domain.SessionWorktreeRecord{}
	for _, row := range rows {
		byRepo[row.RepoName] = row
	}
	if byRepo[domain.RootWorkspaceRepoName].State != "active" || byRepo["api"].State != "active" {
		t.Fatalf("row states = root:%q api:%q, want active inventory", byRepo[domain.RootWorkspaceRepoName].State, byRepo["api"].State)
	}
	if got := byRepo["api"].WorktreePath; got != filepath.Join("/ws/mer-1", "services", "api") {
		t.Fatalf("api worktree path = %q", got)
	}
}

func TestRestore_AppliesProjectAgentMode(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{AgentConfig: domain.AgentConfig{Model: "changed-model", Effort: "low", Mode: "restore-mode"}}}
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "agent-x"})
	agent := &recordingAgent{}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})

	if _, err := m.RestoreWithMode(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if agent.lastConfig.Mode != "restore-mode" || agent.lastConfig.Model != "" || agent.lastConfig.Effort != "" {
		t.Fatalf("restore config = %+v, want project mode with provider-default model and effort", agent.lastConfig)
	}
}

// TestRestore_RefreshesModelFromSessionMetadata is the regression for the
// ChatUI ↔ TUI model-persistence bug (#4893). When TUI is rebuilt (from an
// interface transition or a daemon restore), the harness restore command must
// carry the model the user last selected in ChatUI — refreshed from the
// session's durable metadata — rather than silently reverting to the project
// configured default. It must also still resume the SAME conversation, never
// minting a new session.
func TestRestore_RefreshesModelFromSessionMetadata(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	// The project default would otherwise win; the session's own persisted model
	// (the ChatUI choice) must take precedence for the rebuild.
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{AgentConfig: domain.AgentConfig{Model: "project-default-model"}}}
	seedTerminal(st, "mer-1", domain.SessionMetadata{
		WorkspacePath:  "/ws/mer-1",
		Branch:         "b",
		AgentSessionID: "agent-x",
		Model:          "5.6-luna",
	})
	agent := &recordingAgent{}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})

	res, err := m.RestoreWithMode(ctx, "mer-1")
	if err != nil {
		t.Fatalf("RestoreWithMode: %v", err)
	}
	// The ChatUI model, not the project default, reaches the harness restore cmd.
	if agent.lastConfig.Model != "5.6-luna" {
		t.Fatalf("restore config model = %q, want the session's ChatUI choice 5.6-luna", agent.lastConfig.Model)
	}
	// History is preserved: the restore resumed the existing native conversation
	// (agent-x) rather than spawning a new session.
	if !reflect.DeepEqual(agent.lastRestore.Session.Metadata[ports.MetadataKeyAgentSessionID], "agent-x") {
		t.Fatalf("resume identity = %q, want agent-x (conversation must be preserved)", agent.lastRestore.Session.Metadata[ports.MetadataKeyAgentSessionID])
	}
	if res.Mode != RestoreModeNative {
		t.Fatalf("restore mode = %q, want native (no new session)", res.Mode)
	}
}

// TestRestore_RefreshesEffortFromSessionMetadata is the effort sibling of the
// ChatUI ↔ TUI persistence bug above: a per-spawn or ChatUI-selected reasoning
// effort recorded on the session's durable metadata must reach the harness
// restore command for every effort-capable harness, not silently revert to the
// project default for everyone except Claude Code.
func TestRestore_RefreshesEffortFromSessionMetadata(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	// The project default would otherwise win; the session's own persisted
	// effort must take precedence for the rebuild.
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{
		AgentConfig: domain.AgentConfig{Model: "project-default-model", Effort: "low"},
	}}
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Harness: domain.HarnessCodex, IsTerminated: true,
		Activity: domain.Activity{State: domain.ActivityExited},
		Metadata: domain.SessionMetadata{
			WorkspacePath:  "/ws/mer-1",
			Branch:         "b",
			AgentSessionID: "codex-native-1",
			Effort:         "high",
		},
	}
	agent := &recordingAgent{}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})

	if _, err := m.RestoreWithMode(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	// The persisted effort, not the project default, reaches the harness restore cmd.
	if agent.lastConfig.Effort != "high" {
		t.Fatalf("restore config effort = %q, want the session's persisted effort high", agent.lastConfig.Effort)
	}
	// History is preserved: the restore resumed the existing native conversation
	// rather than spawning a new session.
	if !reflect.DeepEqual(agent.lastRestore.Session.Metadata[ports.MetadataKeyAgentSessionID], "codex-native-1") {
		t.Fatalf("resume identity = %q, want codex-native-1 (conversation must be preserved)", agent.lastRestore.Session.Metadata[ports.MetadataKeyAgentSessionID])
	}
}

// TestPersistChatModel is the regression for the ChatUI-side half of #4893. The
// manager must record a model the user picked in ChatUI onto the session's
// durable metadata before the next prompt routes, and a model-only write must
// never disturb the conversation identity or terminating state.
func TestPersistChatModel(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newManager()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{
			WorkspacePath:   "/ws/mer-1",
			RuntimeHandleID: "h1",
			AgentSessionID:  "claude-native-1",
			Branch:          "tp/mer-1",
		},
		Activity: domain.Activity{State: domain.ActivityActive},
	}

	if err := m.PersistChatModel(ctx, "mer-1", "5.6-luna"); err != nil {
		t.Fatalf("PersistChatModel: %v", err)
	}
	rec := st.sessions["mer-1"]
	if rec.Metadata.Model != "5.6-luna" {
		t.Fatalf("persisted model = %q, want 5.6-luna", rec.Metadata.Model)
	}
	// The conversation/history and terminating state are untouched.
	if rec.Metadata.AgentSessionID != "claude-native-1" {
		t.Fatalf("resume identity changed to %q, want claude-native-1 preserved", rec.Metadata.AgentSessionID)
	}
	if rec.Metadata.RuntimeHandleID != "h1" {
		t.Fatalf("runtime handle changed to %q, want h1 preserved", rec.Metadata.RuntimeHandleID)
	}
	if rec.Activity.State != domain.ActivityActive || rec.IsTerminated {
		t.Fatalf("session state changed, want active and non-terminated: %+v", rec)
	}

	// Clearing the override lets a later TUI rebuild follow provider defaults.
	if err := m.PersistChatModel(ctx, "mer-1", ""); err != nil {
		t.Fatalf("PersistChatModel(empty): %v", err)
	}
	if st.sessions["mer-1"].Metadata.Model != "" {
		t.Fatalf("cleared model = %q, want provider default", st.sessions["mer-1"].Metadata.Model)
	}
}

func TestRestore_ForwardsManagerDataDir(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "agent-x"})
	agent := &recordingAgent{}
	dataDir := t.TempDir()
	m := New(Deps{
		Runtime:   &fakeRuntime{},
		Agents:    singleAgent{agent: agent},
		Workspace: &fakeWorkspace{},
		Store:     st,
		Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: st},
		DataDir:   dataDir,
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})

	if _, err := m.RestoreWithMode(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if agent.lastRestore.DataDir != dataDir {
		t.Fatalf("restore config data dir = %q, want manager data dir %q", agent.lastRestore.DataDir, dataDir)
	}
}

func TestRestore_RefusesLiveSession(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newManager()
	st.sessions["mer-1"] = mkLive("mer-1")
	if _, err := m.RestoreWithMode(ctx, "mer-1"); !errors.Is(err, ErrNotRestorable) {
		t.Fatalf("want ErrNotRestorable, got %v", err)
	}
}
func TestRestore_ForwardsResolvedAgentConfigPermissions(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{
		AgentConfig: domain.AgentConfig{Permissions: domain.PermissionModeBypassPermissions},
	}}
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:           "mer-1",
		ProjectID:    "mer",
		Kind:         domain.KindWorker,
		IsTerminated: true,
		Metadata:     domain.SessionMetadata{Branch: "tp/mer-1", WorkspacePath: "/tmp/ws", AgentSessionID: "native-1"},
	}
	agent := &recordingAgent{}
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: func(string) (string, error) { return "/bin/true", nil }})

	_, err := m.RestoreWithMode(ctx, "mer-1")
	if err != nil {
		t.Fatal(err)
	}

	if agent.lastRestore.Config.Permissions != domain.PermissionModeBypassPermissions {
		t.Fatalf("restore config permissions = %q, want bypass", agent.lastRestore.Config.Permissions)
	}
	if agent.lastRestore.Permissions != domain.PermissionModeBypassPermissions {
		t.Fatalf("restore permissions = %q, want bypass", agent.lastRestore.Permissions)
	}
}

func TestRestore_WorkerKeepsArtifactOutputPrompt(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, IsTerminated: true,
		RequestedOutput: domain.RequestedOutputArtifact,
		Metadata:        domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "agent-x"},
	}
	agent := &recordingAgent{}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, DataDir: t.TempDir(), LookPath: lookPath})

	if _, err := m.RestoreWithMode(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(agent.lastRestore.SystemPrompt, "## Artifact Output") {
		t.Fatalf("restore system prompt missing artifact output section:\n%s", agent.lastRestore.SystemPrompt)
	}
}

// TestRestore_OrchestratorRederivesSystemPrompt: the system prompt is derived,
// not persisted, so a restored orchestrator must get its role instructions
// recomputed and handed to the agent's native resume command.
func TestRestore_OrchestratorRederivesSystemPrompt(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	cfg := testRoleAgents()
	cfg.OrchestratorRules = "Use workers for implementation."
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: cfg}
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator, IsTerminated: true,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "agent-x"},
	}
	agent := &recordingAgent{}
	dataDir := t.TempDir()
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, DataDir: dataDir, LookPath: lookPath})

	if _, err := m.RestoreWithMode(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(agent.lastRestore.SystemPrompt, "You are the human-facing orchestrator for project mer") {
		t.Fatalf("restore system prompt missing coordinator role:\n%s", agent.lastRestore.SystemPrompt)
	}
	if !strings.Contains(agent.lastRestore.SystemPrompt, "Use workers for implementation.") {
		t.Fatalf("restore system prompt missing project rules:\n%s", agent.lastRestore.SystemPrompt)
	}
	wantPath := filepath.Join(dataDir, "prompts", "mer-1", "system.md")
	if agent.lastRestore.SystemPromptFile != wantPath {
		t.Fatalf("restore system prompt file = %q, want %q", agent.lastRestore.SystemPromptFile, wantPath)
	}
}

func TestRestore_FallsBackToInlineWhenPromptFileUnavailable(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator, Harness: domain.HarnessClaudeCode, IsTerminated: true,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "agent-x"},
	}
	agent := &recordingAgent{}
	dataDir := blockedDataDir(t)
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{
		Runtime:   &fakeRuntime{},
		Agents:    singleAgent{agent: agent},
		Workspace: &fakeWorkspace{},
		Store:     st,
		Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: st},
		DataDir:   dataDir,
		LookPath:  lookPath,
		Logger:    slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
	})

	if _, err := m.RestoreWithMode(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if agent.lastRestore.SystemPrompt == "" {
		t.Fatal("SystemPrompt is empty, want inline prompt fallback")
	}
	if agent.lastRestore.SystemPromptFile != "" {
		t.Fatalf("SystemPromptFile = %q, want empty after write failure", agent.lastRestore.SystemPromptFile)
	}
}

func TestRestore_PromptFileFailureBlocksFileOnlyHarness(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessKiro, IsTerminated: true,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "agent-x", Prompt: "do it"},
	}
	agent := &recordingAgent{}
	dataDir := blockedDataDir(t)
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{
		Runtime:   &fakeRuntime{},
		Agents:    singleAgent{agent: agent},
		Workspace: &fakeWorkspace{},
		Store:     st,
		Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: st},
		DataDir:   dataDir,
		LookPath:  lookPath,
	})

	_, err := m.RestoreWithMode(ctx, "mer-1")
	if err == nil {
		t.Fatal("Restore succeeded, want prompt-file error for file-only harness")
	}
	if !strings.Contains(err.Error(), "system prompt file") {
		t.Fatalf("Restore err = %v, want system prompt file error", err)
	}
}

// TestRestore_FallbackLaunchCarriesSystemPrompt: when the agent has no native
// session to resume, the fresh-launch fallback must carry the re-derived
// system prompt alongside the persisted task prompt.
func TestRestore_FallbackLaunchCarriesSystemPrompt(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator, IsTerminated: true,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", Prompt: "kick off"},
	}
	agent := &recordingAgent{}
	dataDir := t.TempDir()
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, DataDir: dataDir, LookPath: lookPath})

	if _, err := m.RestoreWithMode(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(agent.lastLaunch.SystemPrompt, "You are the human-facing orchestrator for project mer") {
		t.Fatalf("fallback launch system prompt missing coordinator role:\n%s", agent.lastLaunch.SystemPrompt)
	}
	wantPath := filepath.Join(dataDir, "prompts", "mer-1", "system.md")
	if agent.lastLaunch.SystemPromptFile != wantPath {
		t.Fatalf("fallback launch system prompt file = %q, want %q", agent.lastLaunch.SystemPromptFile, wantPath)
	}
	if agent.lastLaunch.Prompt != "kick off" {
		t.Fatalf("fallback launch prompt = %q, want persisted task prompt", agent.lastLaunch.Prompt)
	}
}

func TestRestore_FallbackLaunchDeliversPromptAfterStartWhenAgentRequestsIt(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, IsTerminated: true,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", Prompt: "continue the task"},
	}
	rt := &fakeRuntime{}
	msg := &fakeMessenger{}
	recording := &recordingAgent{}
	agent := &composedAfterStartAgent{afterStartAgent: afterStartAgent{recordingAgent: recording}}
	m := New(Deps{
		Runtime:   rt,
		Agents:    singleAgent{agent: agent},
		Workspace: &fakeWorkspace{},
		Store:     st,
		Messenger: msg,
		Lifecycle: &fakeLCM{store: st},
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})

	if _, err := m.RestoreWithMode(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if agent.buildCalls != 1 {
		t.Fatalf("BuildAfterStartPrompt calls = %d, want 1", agent.buildCalls)
	}
	if recording.lastLaunch.Prompt != "" {
		t.Fatalf("fallback launch prompt = %q, want empty for after-start delivery", recording.lastLaunch.Prompt)
	}
	if len(msg.msgs) != 1 || !strings.HasPrefix(msg.msgs[0], "STANDING:\n") ||
		!strings.Contains(msg.msgs[0], "## Tallpine Worker Role") ||
		!strings.HasSuffix(msg.msgs[0], "TASK:\ncontinue the task") {
		t.Fatalf("delivered prompts = %#v, want one combined fallback turn", msg.msgs)
	}
	if rt.created != 1 {
		t.Fatalf("runtime.Create = %d, want 1", rt.created)
	}
}

func TestRestore_NativeAfterStartAgentResumesPassively(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, IsTerminated: true,
		Metadata: domain.SessionMetadata{
			WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "native-1", Prompt: "continue the task",
		},
	}
	rt := &fakeRuntime{}
	msg := &fakeMessenger{}
	recording := &recordingAgent{}
	agent := &composedAfterStartAgent{afterStartAgent: afterStartAgent{recordingAgent: recording}}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st,
		Messenger: msg, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})

	res, err := m.RestoreWithMode(ctx, "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != RestoreModeNative {
		t.Fatalf("restore mode = %q, want %q", res.Mode, RestoreModeNative)
	}
	if recording.restoreCalls != 1 || recording.launchCalls != 0 {
		t.Fatalf("adapter calls = restore %d launch %d, want restore 1 launch 0", recording.restoreCalls, recording.launchCalls)
	}
	if agent.buildCalls != 0 {
		t.Fatalf("BuildAfterStartPrompt calls = %d, want 0 for passive native resume", agent.buildCalls)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("delivered prompts = %#v, want no new user turn for passive native resume", msg.msgs)
	}
}

func TestRestore_CodexWithoutAgentSessionIDFallsBackToSavedPrompt(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex, IsTerminated: true,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", Prompt: "continue the task"},
	}
	rt := &fakeRuntime{}
	agent := &recordingAgent{}
	m := New(Deps{
		Runtime:   rt,
		Agents:    singleAgent{agent: agent},
		Workspace: &fakeWorkspace{},
		Store:     st,
		Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: st},
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})

	res, err := m.RestoreWithMode(ctx, "mer-1")
	if err != nil {
		t.Fatalf("Restore err = %v, want fallback launch", err)
	}
	if res.Mode != RestoreModeSavedPrompt {
		t.Fatalf("restore mode = %q, want %q", res.Mode, RestoreModeSavedPrompt)
	}
	if agent.restoreCalls != 1 {
		t.Fatalf("GetRestoreCommand calls = %d, want 1", agent.restoreCalls)
	}
	if agent.launchCalls != 1 {
		t.Fatalf("GetLaunchCommand calls = %d, want 1", agent.launchCalls)
	}
	if agent.lastLaunch.Prompt != "continue the task" {
		t.Fatalf("fallback launch prompt = %q, want saved prompt", agent.lastLaunch.Prompt)
	}
	if rt.created != 1 {
		t.Fatalf("runtime.Create = %d, want 1", rt.created)
	}
	if st.sessions["mer-1"].IsTerminated {
		t.Fatal("session must be live after fallback launch")
	}
}

func TestRestore_ClaudeCodeWithoutRestoreCommandFallsBackToSavedPrompt(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, IsTerminated: true,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", Prompt: "continue the task"},
	}
	rt := &fakeRuntime{}
	agent := &recordingAgent{}
	m := New(Deps{
		Runtime:   rt,
		Agents:    singleAgent{agent: agent},
		Workspace: &fakeWorkspace{},
		Store:     st,
		Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: st},
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})

	if _, err := m.RestoreWithMode(ctx, "mer-1"); err != nil {
		t.Fatalf("Restore err = %v, want fallback launch", err)
	}
	if agent.restoreCalls != 1 {
		t.Fatalf("GetRestoreCommand calls = %d, want 1", agent.restoreCalls)
	}
	if agent.launchCalls != 1 {
		t.Fatalf("GetLaunchCommand calls = %d, want 1", agent.launchCalls)
	}
	if agent.lastLaunch.Prompt != "continue the task" {
		t.Fatalf("fallback launch prompt = %q, want saved prompt", agent.lastLaunch.Prompt)
	}
	if rt.created != 1 {
		t.Fatalf("runtime.Create = %d, want 1", rt.created)
	}
	if st.sessions["mer-1"].IsTerminated {
		t.Fatal("session must be live after fallback launch")
	}
}

// TestRestore_PromptlessOrchestratorResumesViaAdapter locks the orchestrator
// fix: a promptless session with no captured agentSessionId is still restorable
// when the adapter can resume it (Claude pins a deterministic --session-id).
// Before the fix the metadata-only guard rejected it with ErrNotResumable, so
// every boot abandoned the orchestrator and spawned a fresh one.
func TestRestore_PromptlessOrchestratorResumesViaAdapter(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator, IsTerminated: true,
		// No AgentSessionID, no Prompt: exactly how orchestrators are persisted.
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-orchestrator"},
		Activity: domain.Activity{State: domain.ActivityExited},
	}
	rt := &fakeRuntime{}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: rt, Agents: singleAgent{agent: alwaysResumeAgent{}}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})

	if _, err := m.RestoreWithMode(ctx, "mer-1"); err != nil {
		t.Fatalf("promptless orchestrator must restore via adapter resume, got err = %v", err)
	}
	if rt.created != 1 {
		t.Fatalf("runtime.Create = %d, want 1 (resumed)", rt.created)
	}
	if st.sessions["mer-1"].IsTerminated {
		t.Error("orchestrator must be live after restore")
	}
}

// TestRestore_PromptlessUnresumableRelaunchesFresh covers the genuine-reboot
// case: a promptless session whose adapter cannot resume (no native session id,
// no captured AgentSessionID) must be relaunched fresh via GetLaunchCommand
// in the SAME id. The orchestrator is the canonical example: after a reboot
// where tmux is truly gone, RestoreAll must recover it in place rather than
// abandon it and mint a new one (which caused the id-increment bug).
func TestRestore_PromptlessUnresumableRelaunchesFresh(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator, IsTerminated: true,
		// No AgentSessionID, no Prompt: exactly how an orchestrator is persisted.
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-orchestrator"},
		Activity: domain.Activity{State: domain.ActivityExited},
	}
	rt := &fakeRuntime{}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	// fakeAgents resolves to fakeAgent, whose GetRestoreCommand returns ok=false
	// without an agentSessionId, and GetLaunchCommand returns a valid argv.
	m := New(Deps{Runtime: rt, Agents: fakeAgents{}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})

	if _, err := m.RestoreWithMode(ctx, "mer-1"); err != nil {
		t.Fatalf("promptless unresumable session must relaunch fresh, got err = %v", err)
	}
	if rt.created != 1 {
		t.Fatalf("runtime.Create = %d, want 1 (fresh launch)", rt.created)
	}
	if st.sessions["mer-1"].IsTerminated {
		t.Error("session must be live after fresh relaunch")
	}
}

// TestRestore_PromptlessWorkerNotResumable is the RED test for the promptless-worker
// fix: a KindWorker session with no prompt and no captured AgentSessionID (so the
// adapter returns ok=false) must NOT be blank-relaunched. The session had no task
// to replay and no native id to resume from, so relaunching fresh would silently
// drop its work. Restore must return ErrNotResumable and leave the session terminated
// (runtime.Create must NOT be called).
func TestRestore_PromptlessWorkerNotResumable(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, IsTerminated: true,
		// No AgentSessionID, no Prompt: promptless worker with no resume handle.
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1/root"},
		Activity: domain.Activity{State: domain.ActivityExited},
	}
	rt := &fakeRuntime{}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	// fakeAgents resolves to fakeAgent, whose GetRestoreCommand returns ok=false
	// when there is no AgentSessionID. With a KindWorker and empty Prompt, this
	// must produce ErrNotResumable instead of a blank relaunch.
	m := New(Deps{Runtime: rt, Agents: fakeAgents{}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})

	_, err := m.RestoreWithMode(ctx, "mer-1")
	if !errors.Is(err, ErrNotResumable) {
		t.Fatalf("promptless unresumable worker must return ErrNotResumable, got %v", err)
	}
	if rt.created != 0 {
		t.Fatalf("runtime.Create = %d, want 0 (must not relaunch a promptless worker)", rt.created)
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Error("session must remain terminated after ErrNotResumable")
	}
}

// A reserved-but-empty conversation id must not gate restore. Resuming it fails
// with "No conversation found" on every attempt, while the session's workspace
// still holds its branch and commits — so the session relaunches fresh into that
// workspace instead of being stranded.
func TestRestore_LostNativeConversationRelaunchesFresh(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, IsTerminated: true,
		// An id was reserved, but no prompt was saved: without the probe this
		// resumes into a conversation that does not exist.
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1/root", AgentSessionID: "reserved-but-empty"},
		Activity: domain.Activity{State: domain.ActivityExited},
	}
	rec := &recordingAgent{}
	agent := lostConversationAgent{recordingAgent: rec}
	rt := &fakeRuntime{}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: rt, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})

	res, err := m.RestoreWithMode(ctx, "mer-1")
	if err != nil {
		t.Fatalf("restore with a lost conversation: %v", err)
	}
	if res.Mode == RestoreModeNative {
		t.Errorf("restore mode = %q, want a fresh relaunch rather than a doomed --resume", res.Mode)
	}
	if rt.created != 1 {
		t.Errorf("runtime.Create = %d, want 1: the workspace holds real work", rt.created)
	}
}

func TestRestore_PromptlessLostNativeConversationDeliversStandingInstructions(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, IsTerminated: true,
		Metadata: domain.SessionMetadata{
			WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1/root", AgentSessionID: "reserved-but-empty",
		},
		Activity: domain.Activity{State: domain.ActivityExited},
	}
	recording := &recordingAgent{}
	composed := &composedAfterStartAgent{afterStartAgent: afterStartAgent{recordingAgent: recording}}
	agent := lostComposedAfterStartAgent{composedAfterStartAgent: composed}
	msg := &fakeMessenger{}
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st,
		Messenger: msg, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})

	res, err := m.RestoreWithMode(ctx, "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != RestoreModeFresh {
		t.Fatalf("restore mode = %q, want %q", res.Mode, RestoreModeFresh)
	}
	if composed.buildCalls != 1 {
		t.Fatalf("BuildAfterStartPrompt calls = %d, want 1", composed.buildCalls)
	}
	if recording.lastLaunch.Prompt != "" {
		t.Fatalf("fresh launch prompt = %q, want empty for after-start delivery", recording.lastLaunch.Prompt)
	}
	if len(msg.msgs) != 1 || !strings.HasPrefix(msg.msgs[0], "STANDING:\n") ||
		!strings.Contains(msg.msgs[0], "## Tallpine Worker Role") ||
		!strings.HasSuffix(msg.msgs[0], "TASK:\n") {
		t.Fatalf("delivered prompts = %#v, want one instruction-only bootstrap turn", msg.msgs)
	}
}

func TestRestore_LostDerivedNativeConversationRelaunchesFresh(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, IsTerminated: true,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "tp/mer-1/root"},
		Activity: domain.Activity{State: domain.ActivityExited},
	}
	agent := &lostDerivedConversationAgent{}
	rt := &fakeRuntime{}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: rt, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})

	res, err := m.RestoreWithMode(ctx, "mer-1")
	if err != nil {
		t.Fatalf("restore with a lost derived conversation: %v", err)
	}
	if res.Mode == RestoreModeNative {
		t.Errorf("restore mode = %q, want a fresh relaunch rather than a doomed --resume", res.Mode)
	}
	if agent.probedID != "derived-but-empty" {
		t.Errorf("probed conversation id = %q, want derived-but-empty", agent.probedID)
	}
	if rt.created != 1 {
		t.Errorf("runtime.Create = %d, want 1: the workspace holds real work", rt.created)
	}
}

// TestRestore_WorkerPointsAtCurrentOrchestrator: a restored worker's
// coordination hint must reference the orchestrator active at restore time,
// not the one from its original spawn.
func TestRestore_WorkerPointsAtCurrentOrchestrator(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-9"] = domain.SessionRecord{ID: "mer-9", ProjectID: "mer", Kind: domain.KindOrchestrator}
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, IsTerminated: true,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "agent-x"},
	}
	agent := &recordingAgent{}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})

	if _, err := m.RestoreWithMode(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(agent.lastRestore.SystemPrompt, `tallpine send --session mer-9`) {
		t.Fatalf("restore system prompt missing current orchestrator contact:\n%s", agent.lastRestore.SystemPrompt)
	}
}

// TestRestore_RefusesIncompleteHandle covers Bug 2: a terminated row whose
// spawn failed before the workspace landed (no WorkspacePath, no Branch) must
// fail Restore with ErrIncompleteHandle — the same typed sentinel Kill returns
// for the same shape — so the HTTP layer surfaces a typed 409 instead of an
// opaque 500.
func TestRestore_RefusesIncompleteHandle(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newManager()
	// Seed a terminated row with no workspace and no branch (the post-failure
	// shape of a Spawn that died before workspace.Create succeeded).
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:           "mer-1",
		ProjectID:    "mer",
		IsTerminated: true,
		Metadata:     domain.SessionMetadata{Prompt: "do it"},
	}
	if _, err := m.RestoreWithMode(ctx, "mer-1"); !errors.Is(err, ErrIncompleteHandle) {
		t.Fatalf("want ErrIncompleteHandle, got %v", err)
	}
}

func TestRestoreRetainsSpawnPermissionsAfterProjectChange(t *testing.T) {
	t.Parallel()

	for _, permission := range []domain.PermissionMode{"", domain.PermissionModeDefault} {
		st := newFakeStore()
		st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
		agent := &recordingAgent{}
		m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: func(string) (string, error) { return "/bin/true", nil }})
		rec, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, AgentConfig: ports.AgentConfig{Permissions: permission}})
		if err != nil {
			t.Fatal(err)
		}
		want := permission
		if want == "" {
			want = domain.PermissionModeAuto
		}
		if rec.Metadata.Permissions != want {
			t.Fatalf("spawn pin=%q want %q", rec.Metadata.Permissions, want)
		}
		project := st.projects["mer"]
		project.Config.AgentConfig.Permissions = domain.PermissionModeBypassPermissions
		st.projects["mer"] = project
		rec.Metadata.AgentSessionID = "native-1"
		seedTerminal(st, rec.ID, rec.Metadata)
		if _, err := m.RestoreWithMode(ctx, rec.ID); err != nil {
			t.Fatal(err)
		}
		if agent.lastRestore.Permissions != want || agent.lastRestore.Config.Permissions != want {
			t.Fatalf("restore=%#v want %q", agent.lastRestore, want)
		}
	}
}
