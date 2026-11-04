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

	"tallpine.eu/tallpine/backend/internal/adapters/workspace/scratch"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestSpawn_PreparesProviderRuntimeAfterAssigningGeneration(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rt := &fakeRuntime{}
	agent := &runtimePreparingAgent{launchArgvAgent: launchArgvAgent{argv: []string{"agent"}}}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, DataDir: "/tallpine-data", RunFilePath: "/tallpine-data/running.json",
		LookPath: func(string) (string, error) { return "/bin/true", nil }, NewLaunchID: func() string { return "launch-7" },
	})
	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex}); err != nil {
		t.Fatal(err)
	}
	if got := agent.got.Env[EnvRuntimeLaunchID]; got != "launch-7" {
		t.Fatalf("preparer launch id = %q, want launch-7", got)
	}
	if agent.got.DataDir != "/tallpine-data" || agent.got.SessionID != "mer-1" {
		t.Fatalf("preparer config = %+v", agent.got)
	}
	if rt.lastCfg.Env["PROVIDER_RUNTIME_PREPARED"] != "1" {
		t.Fatalf("runtime did not receive preparer environment: %+v", rt.lastCfg.Env)
	}
}

func TestSpawn_RejectsMissingRoleHarness(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: fakeAgents{}, Workspace: &fakeWorkspace{}, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})

	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); !errors.Is(err, ErrMissingHarness) {
		t.Fatalf("worker err = %v, want ErrMissingHarness", err)
	}
	if len(st.sessions) != 0 {
		t.Fatalf("missing worker harness must not create a session row, got %d", len(st.sessions))
	}
	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator}); errors.Is(err, ErrMissingHarness) {
		// B239: an orchestrator with no configured agent uses the daemon default.
		t.Fatalf("orchestrator err = %v, want the default-harness fallback", err)
	}
}

func TestSpawnAutomationRejectsIncompletePriorLaunch(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newManager()
	runID := domain.AutomationRunID("run-1")
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", AutomationRunID: &runID}
	_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, AutomationRunID: &runID})
	if err == nil || !strings.Contains(err.Error(), "incomplete prior launch") {
		t.Fatalf("Spawn error = %v, want incomplete prior launch", err)
	}
	if rt.created != 0 || ws.lastCfg.SessionID != "" {
		t.Fatalf("runtime creates=%d workspace session=%q, want no duplicate launch", rt.created, ws.lastCfg.SessionID)
	}
}

func TestSpawnAutomationProvisionFailureDeletesPrelaunchRow(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{
		Symlinks: []string{"../outside"},
		Worker:   domain.RoleOverride{Harness: domain.HarnessClaudeCode},
	}}
	ws.path = t.TempDir()
	runID := domain.AutomationRunID("run-1")

	_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Prompt: "do it", AutomationRunID: &runID})
	if err == nil || !strings.Contains(err.Error(), "provision") {
		t.Fatalf("Spawn error = %v, want provisioning failure", err)
	}
	for id, rec := range st.sessions {
		if rec.AutomationRunID != nil && *rec.AutomationRunID == runID {
			t.Fatalf("automation prelaunch row %s survived failed provisioning: %#v", id, rec)
		}
	}
}

func TestSpawnWorkspaceRecordFailurePreservesDirtyWorkspace(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	project := st.projects["mer"]
	project.Kind = domain.ProjectKindWorkspace
	project.Path = t.TempDir()
	st.projects["mer"] = project
	path := t.TempDir()
	ws.projectCreateInfo = ports.WorkspaceProjectInfo{
		Root:      ports.WorkspaceInfo{Path: path, Branch: "tp/task", SessionID: "mer-1", ProjectID: "mer"},
		Worktrees: []ports.WorkspaceRepoInfo{{RepoName: domain.RootWorkspaceRepoName, Path: path, Branch: "tp/task", SessionID: "mer-1", ProjectID: "mer", RepoPath: project.Path}},
	}
	st.upsertWTErr = errors.New("record worktree failed")
	ws.destroyErr = ports.ErrWorkspaceDirty
	_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode})
	if !errors.Is(err, ErrWorkspaceCreate) {
		t.Fatalf("spawn = %v, want workspace creation error", err)
	}
	rec, ok := st.sessions["mer-1"]
	if !ok || rec.Metadata.WorkspacePath != path || !rec.IsTerminated {
		t.Fatalf("dirty worktree lost its session record: found=%v session=%+v", ok, rec)
	}

}

func TestSpawn_RollbackGivesEachCleanupStepAFreshDeadline(t *testing.T) {
	previousBudget := spawnRollbackBudget
	spawnRollbackBudget = 10 * time.Millisecond
	t.Cleanup(func() { spawnRollbackBudget = previousBudget })

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rt := &deadlineConsumingRuntime{fakeRuntime: &fakeRuntime{}}
	ws := &fakeWorkspace{}
	agent := &recordingAgent{}
	spawnCtx, cancel := context.WithCancel(context.Background())
	m := New(Deps{
		Runtime:   rt,
		Agents:    singleAgent{agent: afterStartAgent{recordingAgent: agent}},
		Workspace: ws,
		Store:     st,
		Messenger: &cancelOnDeliverMessenger{cancel: cancel},
		Lifecycle: &fakeLCM{store: st},
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})

	_, _, _, err := m.Spawn(spawnCtx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "fix the button"})
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrSpawnDeliverPrompt) {
		t.Fatalf("Spawn err = %v, want prompt delivery deadline", err)
	}
	if ws.destroyed != 1 {
		t.Fatalf("workspace destroyed = %d, want 1", ws.destroyed)
	}
	if ws.destroyCtxErr != nil {
		t.Fatalf("workspace cleanup inherited exhausted runtime deadline: %v", ws.destroyCtxErr)
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Fatal("session row was not terminated after an earlier cleanup exhausted its deadline")
	}
}

func TestSpawn_WorkspaceCreationTimeoutRollsBackSeedWithDetachedContext(t *testing.T) {
	t.Parallel()

	base := newFakeStore()
	base.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	st := &contextAwareDeleteStore{fakeStore: base}
	spawnCtx, cancel := context.WithCancel(context.Background())
	ws := &cancelingCreateWorkspace{fakeWorkspace: &fakeWorkspace{}, cancel: cancel}
	m := New(Deps{
		Runtime:   &fakeRuntime{},
		Agents:    singleAgent{agent: &recordingAgent{}},
		Workspace: ws,
		Store:     st,
		Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: base},
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})

	_, _, _, err := m.Spawn(spawnCtx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrWorkspaceCreate) {
		t.Fatalf("Spawn err = %v, want workspace creation deadline", err)
	}
	if st.deleteContextErr != nil {
		t.Fatalf("seed rollback inherited expired request context: %v", st.deleteContextErr)
	}
	if _, ok := base.sessions["mer-1"]; ok {
		t.Fatal("seed session remained after workspace creation timeout")
	}
}

func TestWrapSpawnStagePreservesInnerSentinel(t *testing.T) {
	t.Parallel()

	err := wrapSpawnStage("mer-1", ErrWorkspaceCreate, ports.ErrWorkspaceBranchNotFetched)
	if !errors.Is(err, ErrWorkspaceCreate) {
		t.Fatalf("err = %v, want ErrWorkspaceCreate", err)
	}
	if !errors.Is(err, ports.ErrWorkspaceBranchNotFetched) {
		t.Fatalf("err = %v, want ErrWorkspaceBranchNotFetched", err)
	}
	if got, want := err.Error(), "spawn mer-1: workspace: workspace: branch is not fetched"; got != want {
		t.Fatalf("err.Error() = %q, want %q", got, want)
	}
}

func TestSpawn_RuntimeFailureCleansAgentWorkspaceAfterDestroy(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rt := &fakeRuntime{createErr: errors.New("boom")}
	var sharedLog []string
	ws := &loggingDestroyWorkspace{
		fakeWorkspace: fakeWorkspace{path: "/ws/mer-1"},
		sharedLog:     &sharedLog,
	}
	agent := &cleaningAgent{sharedLog: &sharedLog}
	m := New(Deps{
		Runtime:   rt,
		Agents:    singleAgent{agent: agent},
		Workspace: ws,
		Store:     st,
		Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: st},
		DataDir:   "/tallpine/data",
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})

	_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if err == nil || !strings.Contains(err.Error(), "runtime") {
		t.Fatalf("Spawn err = %v, want runtime failure", err)
	}
	if !errors.Is(err, ErrRuntimeCreate) {
		t.Fatalf("Spawn err = %v, want ErrRuntimeCreate sentinel", err)
	}
	if ws.destroyed != 1 {
		t.Fatalf("workspace destroy calls = %d, want 1", ws.destroyed)
	}
	if agent.cleanupCalls != 1 {
		t.Fatalf("agent cleanup calls = %d, want 1", agent.cleanupCalls)
	}
	if got := agent.cleanupConfigs[0].WorkspacePath; got != "/ws/mer-1" {
		t.Fatalf("cleanup workspace path = %q, want /ws/mer-1", got)
	}
	want := []string{"Destroy:/ws/mer-1", "CleanupWorkspace:/ws/mer-1"}
	if strings.Join(sharedLog, ",") != strings.Join(want, ",") {
		t.Fatalf("rollback order = %v, want %v", sharedLog, want)
	}
	if rec, present := st.sessions["mer-1"]; present {
		t.Fatalf("seed row must be deleted after cleanup, got %+v", rec)
	}
}

// A create that times out client-side may still have started the runtime; the
// rollback must destroy that possible handle and leave no session row behind.
func TestSpawn_RuntimeCreateTimeoutDestroysPossibleRuntime(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name          string
		effect        ports.RuntimeEffectOutcome
		cleanup       ports.RuntimeCleanupOutcome
		wantDestroyed []string
	}{
		{"timeout with unknown effect", ports.RuntimeEffectPossible, ports.RuntimeCleanupNotAttempted, []string{"mer-1"}},
		{"cleanup failed", ports.RuntimeEffectPossible, ports.RuntimeCleanupFailed, []string{"mer-1"}},
		{"runtime already cleaned up", ports.RuntimeEffectPossible, ports.RuntimeCleanupSucceeded, nil},
		{"no effect", ports.RuntimeEffectNone, ports.RuntimeCleanupNotAttempted, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
			rt := &fakeRuntime{createErr: possibleRuntimeEffect{
				err:    context.DeadlineExceeded,
				handle: ports.RuntimeHandle{ID: "mer-1"}, effect: tc.effect, cleanup: tc.cleanup,
			}}
			ws := &fakeWorkspace{path: "/ws/mer-1"}
			m := New(Deps{
				Runtime: rt, Agents: singleAgent{agent: &cleaningAgent{sharedLog: new([]string)}}, Workspace: ws, Store: st,
				Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, DataDir: "/tallpine/data",
				LookPath: func(string) (string, error) { return "/bin/true", nil },
			})

			_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
			if !errors.Is(err, ErrRuntimeCreate) || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Spawn err = %v, want runtime create wrapping the deadline", err)
			}
			if strings.Join(rt.destroyedIDs, ",") != strings.Join(tc.wantDestroyed, ",") {
				t.Fatalf("destroyed runtimes = %v, want %v", rt.destroyedIDs, tc.wantDestroyed)
			}
			if ws.destroyed != 1 {
				t.Fatalf("workspace destroy calls = %d, want 1", ws.destroyed)
			}
			if rec, present := st.sessions["mer-1"]; present {
				t.Fatalf("session row left behind after timeout: %+v", rec)
			}
		})
	}
}

// When the destroy after a create timeout fails too, the runtime may still be
// running in the workspace: keep the workspace and a terminated session row
// carrying the runtime handle so the reconcile reap pass can retry the destroy.
func TestSpawn_RuntimeCreateTimeoutKeepsRecordWhenDestroyFails(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rt := &fakeRuntime{
		createErr: possibleRuntimeEffect{
			err:    context.DeadlineExceeded,
			handle: ports.RuntimeHandle{ID: "mer-1"}, effect: ports.RuntimeEffectPossible, cleanup: ports.RuntimeCleanupNotAttempted,
		},
		destroyErr: errors.New("tmux busy"),
	}
	ws := &fakeWorkspace{path: "/ws/mer-1"}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: &cleaningAgent{sharedLog: new([]string)}}, Workspace: ws, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, DataDir: "/tallpine/data",
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})

	_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if !errors.Is(err, ErrRuntimeCreate) {
		t.Fatalf("Spawn err = %v, want runtime create failure", err)
	}
	if ws.destroyed != 0 {
		t.Fatalf("workspace destroy calls = %d, want 0 while the runtime may be alive", ws.destroyed)
	}
	rec, present := st.sessions["mer-1"]
	if !present {
		t.Fatal("session row deleted although the runtime could not be destroyed")
	}
	if rec.Metadata.RuntimeHandleID != "mer-1" || rec.Metadata.WorkspacePath != "/ws/mer-1" {
		t.Fatalf("metadata = %+v, want runtime handle and workspace recorded", rec.Metadata)
	}
	if !rec.IsTerminated {
		t.Fatal("session must be parked terminated so the reaper picks it up")
	}
}

func TestSpawn_PrepareFailureCleansAgentWorkspaceState(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	ws := &fakeWorkspace{path: "/ws/mer-1"}
	agent := &hookErrorCleaningAgent{hookErr: errors.New("hooks failed")}
	m := New(Deps{
		Runtime:    &fakeRuntime{},
		Agents:     singleAgent{agent: agent},
		Workspace:  ws,
		Store:      st,
		Messenger:  &fakeMessenger{},
		Lifecycle:  &fakeLCM{store: st},
		DataDir:    "/tallpine/data",
		LookPath:   func(string) (string, error) { return "/bin/true", nil },
		Executable: func() (string, error) { return "/daemon/tallpine", nil },
	})

	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err == nil || !strings.Contains(err.Error(), "install hooks") {
		t.Fatalf("Spawn err = %v, want install hooks failure", err)
	}
	if agent.cleanupCalls != 1 {
		t.Fatalf("agent cleanup calls = %d, want 1", agent.cleanupCalls)
	}
	cleanup := agent.cleanupConfigs[0]
	if cleanup.WorkspacePath != "/ws/mer-1" {
		t.Fatalf("cleanup workspace path = %q, want /ws/mer-1", cleanup.WorkspacePath)
	}
	if cleanup.DataDir != "/tallpine/data" {
		t.Fatalf("cleanup data dir = %q, want /tallpine/data", cleanup.DataDir)
	}
	if ws.destroyed != 1 {
		t.Fatalf("workspace destroy calls = %d, want 1", ws.destroyed)
	}
	if rec, present := st.sessions["mer-1"]; present {
		t.Fatalf("seed row must be deleted after cleanup, got %+v", rec)
	}
}

// TestSpawn_DeletesSeedRowOnWorkspaceFailure covers the failed-spawn cleanup:
// when workspace materialization fails (e.g. gitworktree refuses a branch
// checked out elsewhere), nothing observable was built, so the seed row is
// deleted outright rather than parked as a terminated orphan that clutters
// session lists.
func TestSpawn_DeletesSeedRowOnWorkspaceFailure(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newManager()
	ws.createErr = ports.ErrWorkspaceBranchCheckedOutElsewhere
	_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if !errors.Is(err, ports.ErrWorkspaceBranchCheckedOutElsewhere) {
		t.Fatalf("err = %v, want ports.ErrWorkspaceBranchCheckedOutElsewhere", err)
	}
	if rec, present := st.sessions["mer-1"]; present {
		t.Fatalf("seed row must be deleted, got %+v", rec)
	}
	if rt.created != 0 {
		t.Fatal("runtime.Create must not run when workspace materialization fails")
	}
}

// TestSpawn_ParksRowTerminatedWhenSeedDeleteFails asserts the fallback: if the
// seed-row delete itself fails, the failed spawn still parks the row as
// terminated so it never looks live.
func TestSpawn_ParksRowTerminatedWhenSeedDeleteFails(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	ws.createErr = ports.ErrWorkspaceBranchNotFetched
	st.deleteErr = errors.New("db locked")
	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); !errors.Is(err, ports.ErrWorkspaceBranchNotFetched) {
		t.Fatalf("err = %v, want ports.ErrWorkspaceBranchNotFetched", err)
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Fatal("row must fall back to terminated when the seed delete fails")
	}
}

func TestSpawn_WorkspaceProjectRecordsRootAndChildWorktrees(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	projectPath := filepath.Join(string(filepath.Separator), "repo", "mer")
	managedPath := filepath.Join(string(filepath.Separator), "managed", "mer-1")
	st.projects["mer"] = domain.ProjectRecord{
		ID:     "mer",
		Path:   projectPath,
		Kind:   domain.ProjectKindWorkspace,
		Config: testRoleAgents(),
	}
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{
		{Name: "api", RelativePath: "services/api", DefaultBranch: "dev"},
		{Name: "web", RelativePath: "apps/web", DefaultBranch: "main"},
	}
	rt := &fakeRuntime{}
	ws := &fakeWorkspace{path: managedPath}
	m := New(Deps{
		Runtime: rt, Agents: fakeAgents{}, Workspace: ws, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})

	rec, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Metadata.WorkspacePath != managedPath {
		t.Fatalf("workspace path = %q, want root worktree path", rec.Metadata.WorkspacePath)
	}
	if rec.Metadata.Branch != "tp/mer-1" {
		t.Fatalf("workspace branch = %q, want tp/mer-1", rec.Metadata.Branch)
	}
	if got := ws.lastProjectCfg.RootRepoPath; got != projectPath {
		t.Fatalf("root repo path = %q, want %q", got, projectPath)
	}
	if got := ws.lastProjectCfg.BaseBranch; got != "" {
		t.Fatalf("root base branch = %q, want empty so adapter infers the repo default", got)
	}
	if len(ws.lastProjectCfg.Repos) != 2 {
		t.Fatalf("child repo configs = %d, want 2", len(ws.lastProjectCfg.Repos))
	}
	if want := filepath.Join(projectPath, "services", "api"); ws.lastProjectCfg.Repos[0].RepoPath != want {
		t.Fatalf("api repo path = %q, want %q", ws.lastProjectCfg.Repos[0].RepoPath, want)
	}
	if want := filepath.Join(projectPath, "apps", "web"); ws.lastProjectCfg.Repos[1].RepoPath != want {
		t.Fatalf("web repo path = %q, want %q", ws.lastProjectCfg.Repos[1].RepoPath, want)
	}
	if got := ws.lastProjectCfg.Repos[0].BaseBranch; got != "" {
		t.Fatalf("api base branch = %q, want empty so the adapter verifies the repo default", got)
	}
	if got := ws.lastProjectCfg.Repos[1].BaseBranch; got != "" {
		t.Fatalf("web base branch = %q, want empty so the adapter verifies the repo default", got)
	}
	rows := st.worktrees["mer-1"]
	if len(rows) != 3 {
		t.Fatalf("session worktree rows = %d, want 3: %#v", len(rows), rows)
	}
	want := map[string]string{
		domain.RootWorkspaceRepoName: managedPath,
		"api":                        filepath.Join(managedPath, "services", "api"),
		"web":                        filepath.Join(managedPath, "apps", "web"),
	}
	for _, row := range rows {
		if row.Branch != rec.Metadata.Branch {
			t.Fatalf("row %s branch = %q, want %q", row.RepoName, row.Branch, rec.Metadata.Branch)
		}
		if want[row.RepoName] != row.WorktreePath {
			t.Fatalf("row %s path = %q, want %q", row.RepoName, row.WorktreePath, want[row.RepoName])
		}
		if row.BaseSHA == "" {
			t.Fatalf("row %s missing base sha", row.RepoName)
		}
		if row.BaseRef == "" {
			t.Fatalf("row %s missing resolved base ref", row.RepoName)
		}
	}
	if rt.created != 1 {
		t.Fatal("runtime should be created")
	}
	if ws.destroyed != 0 || ws.projectDestroyed != 0 {
		t.Fatal("successful spawn should not destroy workspaces")
	}
}

func TestSpawn_WorkspaceProjectRollsBackAllWorktreesOnRuntimeFailure(t *testing.T) {
	t.Parallel()

	m, st, _, ws := newManager()
	st.projects["mer"] = domain.ProjectRecord{
		ID:     "mer",
		Path:   "/repo/mer",
		Kind:   domain.ProjectKindWorkspace,
		Config: testRoleAgents(),
	}
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{Name: "api", RelativePath: "api"}}
	m.runtime = &fakeRuntime{createErr: errors.New("boom")}
	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err == nil {
		t.Fatal("expected failure")
	}
	if ws.projectDestroyed != 1 {
		t.Fatalf("workspace project destroy calls = %d, want 1", ws.projectDestroyed)
	}
	if ws.destroyed != 0 {
		t.Fatalf("single-workspace destroy calls = %d, want 0", ws.destroyed)
	}
	if _, present := st.sessions["mer-1"]; present {
		t.Fatal("seed row should be deleted after runtime creation failure")
	}
}

func TestSpawn_WorkspaceProjectRollsBackWhenWorktreeRowsFail(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newManager()
	st.projects["mer"] = domain.ProjectRecord{
		ID:     "mer",
		Path:   "/repo/mer",
		Kind:   domain.ProjectKindWorkspace,
		Config: testRoleAgents(),
	}
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{Name: "api", RelativePath: "api"}}
	st.upsertWTErr = errors.New("db locked")
	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err == nil || !strings.Contains(err.Error(), "record workspace worktree") {
		t.Fatalf("err = %v, want worktree row failure", err)
	}
	if ws.projectDestroyed != 1 {
		t.Fatalf("workspace project destroy calls = %d, want 1", ws.projectDestroyed)
	}
	if _, present := st.sessions["mer-1"]; present {
		t.Fatal("seed row should be deleted after workspace row failure")
	}
	if rt.created != 0 {
		t.Fatal("runtime.Create must not run when worktree row recording fails")
	}
}

// TestRollbackSpawn_DeletesSeedRow covers Bug 4: a session row in seed state
// (no workspace, no runtime, no agent session id, not terminated) is deleted
// outright by RollbackSpawn so the user never sees an orphan terminated row.
func TestRollbackSpawn_DeletesSeedRow(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newManager()
	dataDir := t.TempDir()
	m.dataDir = dataDir
	// Seed row matches what CreateSession produces — no Metadata at all.
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Activity:  domain.Activity{State: domain.ActivityIdle},
	}
	if _, err := m.writeSystemPromptFile("mer-1", "system prompt"); err != nil {
		t.Fatal(err)
	}
	deleted, killed, err := m.RollbackSpawn(ctx, "mer-1")
	if err != nil {
		t.Fatalf("rollback err = %v", err)
	}
	if !deleted || killed {
		t.Fatalf("deleted=%v killed=%v, want deleted=true killed=false", deleted, killed)
	}
	if _, present := st.sessions["mer-1"]; present {
		t.Fatal("seed row must be removed from the store, not left as terminated")
	}
	requireNoPromptDir(t, dataDir, "mer-1")
}

// TestRollbackSpawn_FallsBackToKillForLiveRow asserts the no-resurrection
// guarantee from Bug 4's RCA: once a row has observable spawn output (workspace
// + runtime handle), DeleteSession is a no-op and rollback falls back to Kill
// so the runtime + workspace are torn down rather than abandoned.
func TestRollbackSpawn_FallsBackToKillForLiveRow(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newManager()
	st.sessions["mer-1"] = mkLive("mer-1")
	deleted, killed, err := m.RollbackSpawn(ctx, "mer-1")
	if err != nil {
		t.Fatalf("rollback err = %v", err)
	}
	if deleted || !killed {
		t.Fatalf("deleted=%v killed=%v, want deleted=false killed=true", deleted, killed)
	}
	if rt.destroyed != 1 || ws.destroyed != 1 {
		t.Fatalf("kill teardown not invoked: rt=%d ws=%d", rt.destroyed, ws.destroyed)
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Fatal("live row should be marked terminated after kill-fallback")
	}
}

// TestSpawn_RejectsMissingAgentBinary covers Bug 6: when the agent adapter
// returns an argv whose binary is not on PATH, Manager.Spawn must abort BEFORE
// runtime.Create rather than launching into an empty tmux pane that the
// reaper later mistakes for a live session.
func TestSpawn_RejectsMissingAgentBinary(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rt := &fakeRuntime{}
	ws := &fakeWorkspace{}
	dataDir := t.TempDir()
	notFound := func(name string) (string, error) {
		if name == "tmux" {
			return "/bin/tmux", nil
		}
		return "", fmt.Errorf("exec: %q: not found", name)
	}
	m := New(Deps{Runtime: rt, Agents: fakeAgents{}, Workspace: ws, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, DataDir: dataDir, LookPath: notFound})

	_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if !errors.Is(err, ports.ErrAgentBinaryNotFound) {
		t.Fatalf("err = %v, want ports.ErrAgentBinaryNotFound", err)
	}
	if rt.created != 0 {
		t.Fatal("runtime.Create must NOT run when the agent binary is missing")
	}
	if ws.destroyed != 1 {
		t.Fatal("workspace must be torn down when the pre-launch binary check fails")
	}
	if rec, present := st.sessions["mer-1"]; present {
		t.Fatalf("seed row must be deleted before a runtime handle is live, got %+v", rec)
	}
	requireNoPromptDir(t, dataDir, "mer-1")
}

func TestSpawn_MissingBinaryPreservesNonEmptyScratchWorkspaceForRetry(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["scratch"] = domain.ProjectRecord{
		ID:     "scratch",
		Kind:   domain.ProjectKindScratch,
		Config: testRoleAgents(),
	}
	store := &maxSessionNumStore{fakeStore: st}
	workspace, err := scratch.New(scratch.Options{ManagedRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	notFound := func(name string) (string, error) {
		if name == "tmux" {
			return "/bin/tmux", nil
		}
		return "", fmt.Errorf("exec: %q: not found", name)
	}
	m := New(Deps{
		Runtime:   &fakeRuntime{},
		Agents:    singleAgent{agent: &scratchHookAgent{}},
		Workspace: workspace,
		Store:     store,
		Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: st},
		DataDir:   t.TempDir(),
		LookPath:  notFound,
	})

	_, _, _, err = m.Spawn(ctx, ports.SpawnConfig{ProjectID: "scratch", Kind: domain.KindOrchestrator})
	if !errors.Is(err, ports.ErrAgentBinaryNotFound) {
		t.Fatalf("first spawn err = %v, want ErrAgentBinaryNotFound", err)
	}
	failed, ok := st.sessions["scratch-1"]
	if !ok {
		t.Fatal("failed scratch spawn row was deleted")
	}
	if !failed.IsTerminated {
		t.Fatal("failed scratch spawn row must be terminated")
	}
	if failed.Metadata.WorkspacePath == "" {
		t.Fatal("failed scratch spawn must retain its preserved workspace path")
	}
	if _, err := os.Stat(filepath.Join(failed.Metadata.WorkspacePath, ".claude", "settings.local.json")); err != nil {
		t.Fatalf("preserved hook file: %v", err)
	}

	_, _, _, err = m.Spawn(ctx, ports.SpawnConfig{ProjectID: "scratch", Kind: domain.KindOrchestrator})
	if !errors.Is(err, ports.ErrAgentBinaryNotFound) {
		t.Fatalf("retry err = %v, want ErrAgentBinaryNotFound", err)
	}
	if errors.Is(err, ports.ErrWorkspaceDirty) {
		t.Fatalf("retry reused preserved workspace: %v", err)
	}
	if _, ok := st.sessions["scratch-2"]; !ok {
		t.Fatal("retry did not allocate a fresh scratch session")
	}
}

func TestSpawn_EarlyFailurePreservesNonEmptyScratchWorkspace(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	config := testRoleAgents()
	config.Symlinks = []string{"../invalid"}
	st.projects["scratch"] = domain.ProjectRecord{
		ID:     "scratch",
		Kind:   domain.ProjectKindScratch,
		Config: config,
	}
	store := &maxSessionNumStore{fakeStore: st}
	baseWorkspace, err := scratch.New(scratch.Options{ManagedRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	workspace := &nonEmptyWorkspace{Workspace: baseWorkspace}
	m := New(Deps{
		Runtime:   &fakeRuntime{},
		Agents:    fakeAgents{},
		Workspace: workspace,
		Store:     store,
		Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: st},
		DataDir:   t.TempDir(),
	})

	_, _, _, err = m.Spawn(ctx, ports.SpawnConfig{ProjectID: "scratch", Kind: domain.KindOrchestrator})
	if err == nil || !strings.Contains(err.Error(), "provision") {
		t.Fatalf("Spawn err = %v, want provisioning failure", err)
	}
	failed, ok := st.sessions["scratch-1"]
	if !ok {
		t.Fatal("failed early scratch spawn row was deleted")
	}
	if !failed.IsTerminated {
		t.Fatal("failed early scratch spawn row must be terminated")
	}
	if failed.Metadata.WorkspacePath == "" {
		t.Fatal("failed early scratch spawn must retain its preserved workspace path")
	}
	if _, err := os.Stat(filepath.Join(failed.Metadata.WorkspacePath, "provisioned.txt")); err != nil {
		t.Fatalf("preserved workspace file: %v", err)
	}
}

func TestSpawn_ValidatesBinaryAfterEnvPrefix(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rt := &fakeRuntime{}
	ws := &fakeWorkspace{}
	lookedUp := []string{}
	lookPath := func(name string) (string, error) {
		lookedUp = append(lookedUp, name)
		switch name {
		case "tmux":
			return "/bin/tmux", nil
		case "opencode":
			return "/usr/local/bin/opencode", nil
		default:
			return "", fmt.Errorf("exec: %q: not found", name)
		}
	}
	agent := launchArgvAgent{argv: []string{"env", "OPENCODE_CONFIG=/tmp/tallpine/opencode.json", "opencode", "--agent", "tallpine-mer-1"}}
	m := New(Deps{Runtime: rt, Agents: singleAgent{agent: agent}, Workspace: ws, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})

	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	wantLookups := []string{"opencode"}
	if runtime.GOOS == "darwin" {
		wantLookups = []string{"tmux", "opencode"}
	}
	if !reflect.DeepEqual(lookedUp, wantLookups) {
		t.Fatalf("lookups = %#v, want %#v", lookedUp, wantLookups)
	}
	if rt.created != 1 {
		t.Fatalf("runtime.Create calls = %d, want 1", rt.created)
	}
	if !reflect.DeepEqual(rt.lastCfg.Argv, agent.argv) {
		t.Fatalf("runtime argv = %#v, want original argv %#v", rt.lastCfg.Argv, agent.argv)
	}
}

func TestSpawn_RejectsMissingBinaryAfterEnvPrefix(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rt := &fakeRuntime{}
	ws := &fakeWorkspace{}
	lookedUp := []string{}
	lookPath := func(name string) (string, error) {
		lookedUp = append(lookedUp, name)
		if name == "tmux" {
			return "/bin/tmux", nil
		}
		return "", fmt.Errorf("exec: %q: not found", name)
	}
	agent := launchArgvAgent{argv: []string{"env", "OPENCODE_CONFIG=/tmp/tallpine/opencode.json", "opencode", "--agent", "tallpine-mer-1"}}
	m := New(Deps{Runtime: rt, Agents: singleAgent{agent: agent}, Workspace: ws, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})

	_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if !errors.Is(err, ports.ErrAgentBinaryNotFound) {
		t.Fatalf("err = %v, want ports.ErrAgentBinaryNotFound", err)
	}
	wantLookups := []string{"opencode"}
	if runtime.GOOS == "darwin" {
		wantLookups = []string{"tmux", "opencode"}
	}
	if !reflect.DeepEqual(lookedUp, wantLookups) {
		t.Fatalf("lookups = %#v, want %#v", lookedUp, wantLookups)
	}
	if rt.created != 0 {
		t.Fatal("runtime.Create must NOT run when the env-prefixed agent binary is missing")
	}
	if ws.destroyed != 1 {
		t.Fatal("workspace must be torn down when the pre-launch binary check fails")
	}
	if rec, present := st.sessions["mer-1"]; present {
		t.Fatalf("seed row must be deleted before a runtime handle is live, got %+v", rec)
	}
}

func TestSpawn_RejectsEnvPrefixWithoutBinary(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rt := &fakeRuntime{}
	ws := &fakeWorkspace{}
	agent := launchArgvAgent{argv: []string{"env", "OPENCODE_CONFIG=/tmp/tallpine/opencode.json"}}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: agent}, Workspace: ws, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(name string) (string, error) {
			if name == "tmux" {
				return "/bin/tmux", nil
			}
			return "/bin/" + name, nil
		},
	})

	_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if !errors.Is(err, ports.ErrAgentBinaryNotFound) {
		t.Fatalf("err = %v, want ports.ErrAgentBinaryNotFound", err)
	}
	if rt.created != 0 {
		t.Fatal("runtime.Create must NOT run when env-prefixed argv has no binary")
	}
	if ws.destroyed != 1 {
		t.Fatal("workspace must be torn down when env-prefixed argv has no binary")
	}
}

func TestSpawn_RejectsMissingTmuxBeforeSessionRow(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "linux" {
		t.Skip("Windows and Linux use native PTY host, not tmux")
	}
	t.Setenv("TALLPINE_TMUX_BINARY", "")
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rt := &fakeRuntime{}
	ws := &fakeWorkspace{}
	lookPath := func(name string) (string, error) {
		if name == "tmux" {
			return "", fmt.Errorf("exec: %q: not found", name)
		}
		return "/bin/true", nil
	}
	m := New(Deps{Runtime: rt, Agents: fakeAgents{}, Workspace: ws, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})

	_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if !errors.Is(err, ports.ErrRuntimePrerequisite) || !strings.Contains(err.Error(), "tmux required") {
		t.Fatalf("err = %v, want missing tmux prerequisite", err)
	}
	if len(st.sessions) != 0 {
		t.Fatalf("no session row should be created before runtime prerequisites pass, got %d", len(st.sessions))
	}
	if ws.lastCfg.SessionID != "" || ws.destroyed != 0 {
		t.Fatal("workspace must not be created when tmux is missing")
	}
	if rt.created != 0 {
		t.Fatal("runtime must not be created when tmux is missing")
	}
}

func TestValidateRuntimePrerequisites_AllowsConfiguredBundledTmux(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "linux" {
		t.Skip("Windows and Linux use native PTY host, not tmux")
	}
	bundled := filepath.Join(t.TempDir(), "resources", "tmux", "bin", "tmux")
	t.Setenv("TALLPINE_TMUX_BINARY", bundled)
	m := &Manager{
		executable: func() (string, error) { return "", errors.New("unexpected executable lookup") },
		lookPath: func(name string) (string, error) {
			if name == bundled {
				return bundled, nil
			}
			return "", fmt.Errorf("exec: %q: not found", name)
		},
	}
	if err := m.validateRuntimePrerequisites(); err != nil {
		t.Fatalf("validateRuntimePrerequisites() = %v, want configured bundled tmux accepted", err)
	}
}

func TestSpawn_RejectsUnknownHarness(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	rt := &fakeRuntime{}
	ws := &fakeWorkspace{}
	m := New(Deps{Runtime: rt, Agents: missingAgents{}, Workspace: ws, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: func(string) (string, error) { return "/bin/true", nil }})

	_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: "bogus"})
	if !errors.Is(err, ErrUnknownHarness) {
		t.Fatalf("err = %v, want ErrUnknownHarness", err)
	}
	// The harness is rejected before any durable state is created — no seed row,
	// no worktree — so an unknown harness never leaves an orphan behind.
	if len(st.sessions) != 0 {
		t.Fatalf("no session row should be created, got %d", len(st.sessions))
	}
	if ws.lastCfg.SessionID != "" || ws.destroyed != 0 {
		t.Fatal("workspace must not be created for an unknown harness")
	}
	if rt.created != 0 {
		t.Fatal("runtime must not be created for an unknown harness")
	}
}

func TestSpawn_RejectsUnsupportedClaudeTUIEffortBeforeSessionRow(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newManager()
	m.SetModelCatalog(tuningCatalog{catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{
		{ID: "sonnet", IsDefault: true, Efforts: []string{"low", "high"}},
	}}})

	_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{
		ProjectID:     "mer",
		Kind:          domain.KindWorker,
		Harness:       domain.HarnessClaudeCode,
		RequestedMode: domain.SessionModeTUI,
		AgentConfig: ports.AgentConfig{
			Model: "sonnet", Effort: "max",
		},
		EffortOverride: true,
	})
	if !errors.Is(err, ports.ErrUnsupportedEffort) {
		t.Fatalf("err = %v, want ErrUnsupportedEffort", err)
	}
	if len(st.sessions) != 0 {
		t.Fatalf("no session row should be created, got %d", len(st.sessions))
	}
	if ws.lastCfg.SessionID != "" || ws.destroyed != 0 {
		t.Fatal("workspace must not be created for an unsupported Claude effort")
	}
	if rt.created != 0 {
		t.Fatal("runtime must not be created for an unsupported Claude effort")
	}
}

func TestSpawn_RejectsExplicitEffortForUnsupportedHarnessBeforeSessionRow(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newManager()
	m.SetModelCatalog(tuningCatalog{catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{
		{ID: "gemini-2.5-pro", IsDefault: true, Efforts: []string{"low", "high"}},
	}}})

	// TUI is the default session mode: an explicit --effort for a harness that
	// cannot honor it must fail before any durable state exists, not ride
	// along unused while the CLI reports success.
	_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{
		ProjectID:     "mer",
		Kind:          domain.KindWorker,
		Harness:       domain.AgentHarness("droid"),
		RequestedMode: domain.SessionModeTUI,
		AgentConfig: ports.AgentConfig{
			Model: "gemini-2.5-pro", Effort: "high",
		},
		EffortOverride: true,
	})
	if !errors.Is(err, ports.ErrUnsupportedEffort) {
		t.Fatalf("err = %v, want ErrUnsupportedEffort", err)
	}
	if len(st.sessions) != 0 {
		t.Fatalf("no session row should be created, got %d", len(st.sessions))
	}
	if ws.lastCfg.SessionID != "" || ws.destroyed != 0 {
		t.Fatal("workspace must not be created for an unsupported harness effort")
	}
	if rt.created != 0 {
		t.Fatal("runtime must not be created for an unsupported harness effort")
	}
}

func TestSpawn_RejectsFreshClaudeAuthInWorkspaceLaunchContext(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newManager()
	project := st.projects["mer"]
	project.Config.Env = map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1", "AWS_PROFILE": "project-profile"}
	st.projects["mer"] = project
	agent := &launchAuthAgent{recordingAgent: &recordingAgent{}, status: ports.AgentAuthStatusUnauthorized}
	m.agents = singleAgent{agent: agent}

	_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		RequestedMode: domain.SessionModeTUI,
	})
	if !errors.Is(err, ports.ErrAgentAuthRequired) {
		t.Fatalf("err = %v, want ErrAgentAuthRequired", err)
	}
	if agent.workingDir != "/ws/mer-1" {
		t.Fatalf("auth cwd = %q, want session workspace", agent.workingDir)
	}
	if agent.env["CLAUDE_CODE_USE_BEDROCK"] != "1" || agent.env["AWS_PROFILE"] != "project-profile" {
		t.Fatalf("auth env = %#v, want merged project launch environment", agent.env)
	}
	if rt.created != 0 {
		t.Fatal("runtime was created after a rejected fresh credential")
	}
	if len(st.sessions) != 0 || ws.destroyed != 1 {
		t.Fatalf("rejected launch cleanup: sessions=%d destroyed=%d, want 0/1", len(st.sessions), ws.destroyed)
	}
}

// PreLaunch prepares the provider config the launched process will read, so
// it gets the same launch environment (for example a project
// CLAUDE_CONFIG_DIR) the runtime starts the agent with.
func TestSpawn_PreLaunchReceivesTheLaunchEnvironment(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newManager()
	project := st.projects["mer"]
	project.Config.Env = map[string]string{"CLAUDE_CONFIG_DIR": "/profiles/work"}
	st.projects["mer"] = project
	agent := &preLaunchRecordingAgent{recordingAgent: &recordingAgent{}}
	m.agents = singleAgent{agent: agent}

	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		RequestedMode: domain.SessionModeTUI,
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if agent.preLaunch.WorkspacePath != "/ws/mer-1" || agent.preLaunch.Env["CLAUDE_CONFIG_DIR"] != "/profiles/work" ||
		agent.preLaunch.Env[EnvSessionID] != "mer-1" {
		t.Fatalf("PreLaunch config = %#v, want the session workspace and launch environment", agent.preLaunch)
	}
}

// A spawn that fails after the workspace gate is taken, without going through
// rollback, must still release the gate (the defer backstop in Spawn). Spawns
// share the gate as readers, so a leak only shows when the exclusive holders
// (cleanup, restore, reconcile) try to take it; the next spawn would not notice.
func TestSpawnCommitFailureReleasesWorkspaceGate(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newManager()
	st.commitClientRequestErr = errors.New("commit failed")
	cfg := ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Prompt: "do it", ClientRequestID: "draft-1", ClientRequestHash: "v1:first"}
	if _, _, _, err := m.Spawn(ctx, cfg); !errors.Is(err, ErrSpawnCommit) {
		t.Fatalf("first spawn err = %v, want ErrSpawnCommit", err)
	}
	acquired := make(chan struct{})
	go func() {
		m.acquireWorkspaceGate("mer")()
		close(acquired)
	}()
	select {
	case <-acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("exclusive workspace gate blocked: failed spawn leaked its gate")
	}
	st.commitClientRequestErr = nil
	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Prompt: "again"}); err != nil {
		t.Fatalf("next spawn: %v", err)
	}
}
