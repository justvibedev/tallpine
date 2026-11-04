package sessionmanager

import (
	"os"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/adapters/workspace/gitworktree"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func newGitWorktreeManager(t *testing.T) (*Manager, *fakeStore) {
	t.Helper()
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
	m := New(Deps{
		Runtime:   &fakeRuntime{},
		Agents:    fakeAgents{},
		Workspace: gw,
		Store:     st,
		Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: st},
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})
	return m, st
}

// A lane orchestrator gets its own branch and worktree; killing it must leave
// the default orchestrator's worktree on disk (B88 data-loss regression).
func TestLaneOrchestratorGetsOwnWorkspaceAndKillKeepsDefaults(t *testing.T) {
	t.Parallel()
	m, _ := newGitWorktreeManager(t)

	def, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator})
	if err != nil {
		t.Fatal(err)
	}
	lane, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator, LaneItemID: "B108"})
	if err != nil {
		t.Fatal(err)
	}
	if def.Metadata.WorkspacePath == lane.Metadata.WorkspacePath {
		t.Fatalf("lane orchestrator shares worktree %q", lane.Metadata.WorkspacePath)
	}
	if def.Metadata.Branch == lane.Metadata.Branch {
		t.Fatalf("lane orchestrator shares branch %q", lane.Metadata.Branch)
	}
	if _, err := m.Kill(ctx, lane.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(def.Metadata.WorkspacePath); err != nil {
		t.Fatalf("default orchestrator worktree gone after killing the lane: %v", err)
	}
	if _, err := os.Stat(lane.Metadata.WorkspacePath); err == nil {
		t.Fatal("lane worktree should be removed on its own kill")
	}
}

// Kill never removes a worktree path another live session still uses.
func TestKillSkipsWorkspaceUsedByLiveSession(t *testing.T) {
	t.Parallel()
	m, st := newGitWorktreeManager(t)

	a, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator})
	if err != nil {
		t.Fatal(err)
	}
	b, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator, LaneItemID: "B1"})
	if err != nil {
		t.Fatal(err)
	}
	// Force both sessions onto one path, as legacy data can.
	rec := st.sessions[b.ID]
	rec.Metadata.WorkspacePath = a.Metadata.WorkspacePath
	st.sessions[b.ID] = rec

	if _, err := m.Kill(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.Metadata.WorkspacePath); err != nil {
		t.Fatalf("shared worktree removed while another session is live: %v", err)
	}
	if !st.sessions[b.ID].IsTerminated {
		t.Fatal("killed session should still be terminated")
	}
}

// The async chat retry must keep a lane orchestrator on its own branch and
// worktree, not fall back to the default orchestrator's.
func TestRetryLaneOrchestratorKeepsOwnWorkspace(t *testing.T) {
	t.Parallel()
	m, st, _, _ := newGitTaskPreparationManager(t)
	m.dataDir = t.TempDir()
	m.chat = &taskNamingLauncher{}
	spawn := func(lane string) domain.SessionRecord {
		cfg := asyncChatSpawnConfig("")
		cfg.Kind, cfg.LaneItemID = domain.KindOrchestrator, lane
		rec, _, _, err := m.Spawn(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		return st.sessions[rec.ID]
	}
	def := spawn("")
	lane := spawn("B108")
	if def.Metadata.WorkspacePath == lane.Metadata.WorkspacePath {
		t.Fatalf("lane shares the default worktree %q", lane.Metadata.WorkspacePath)
	}
	failed := lane
	failed.ProvisionState, failed.Metadata.ProviderConversationID = domain.SessionProvisionFailed, ""
	failed.Metadata.Branch, failed.Metadata.WorkspacePath = "", ""
	failed.ProvisionSteps = []domain.SessionProvisionStep{{ID: domain.SessionProvisionStepAgent, Status: domain.SessionProvisionStepRunning}}
	st.sessions[lane.ID] = failed
	// The retry reuses the row; it must leave the lane binding untouched.
	bound := domain.SessionLane{LaneItemID: "B108", ParentSessionID: def.ID}
	st.lanes[lane.ID] = bound
	if _, err := m.ResumeAgentWithMode(ctx, lane.ID); err != nil {
		t.Fatal(err)
	}
	if st.lanes[lane.ID] != bound {
		t.Fatalf("lane binding after retry = %+v, want %+v", st.lanes[lane.ID], bound)
	}
	got := st.sessions[lane.ID].Metadata
	if got.WorkspacePath == def.Metadata.WorkspacePath || got.Branch == def.Metadata.Branch || got.Branch == "" {
		t.Fatalf("retry landed on default workspace: %+v (default %+v)", got, def.Metadata)
	}
}

// Workspace-kind projects must also give a lane orchestrator its own root
// path; the managed path derives from SessionPrefix alone.
func TestLaneOrchestratorWorkspaceProjectUsesLanePrefix(t *testing.T) {
	t.Parallel()
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Kind: domain.ProjectKindWorkspace, Config: testRoleAgents()}
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{Name: "api", RelativePath: "api"}}
	ws := &fakeWorkspace{}
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: fakeAgents{}, Workspace: ws, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator}); err != nil {
		t.Fatal(err)
	}
	def := ws.lastProjectCfg.SessionPrefix
	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator, LaneItemID: "B108"}); err != nil {
		t.Fatal(err)
	}
	if got := ws.lastProjectCfg.SessionPrefix; got == def || !strings.HasSuffix(got, "-b108") {
		t.Fatalf("lane prefix = %q, default %q; want distinct lane-suffixed prefix", got, def)
	}
}
