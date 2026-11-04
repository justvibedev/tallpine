package sessionmanager

import (
	"context"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

func TestRecordLaneInheritsFromTheSpawningSession(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "p", Path: "/tmp/p"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "q", Path: "/tmp/q"}); err != nil {
		t.Fatal(err)
	}
	mk := func(project domain.ProjectID, kind domain.SessionKind) domain.SessionID {
		rec, err := st.CreateSession(ctx, domain.SessionRecord{ProjectID: project, Kind: kind, Harness: domain.HarnessClaudeCode})
		if err != nil {
			t.Fatal(err)
		}
		return rec.ID
	}
	laned := mk("p", domain.KindOrchestrator)
	unlaned := mk("p", domain.KindOrchestrator)
	foreign := mk("q", domain.KindOrchestrator)
	if err := st.SetSessionLane(ctx, laned, domain.SessionLane{LaneItemID: "lane-a"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSessionLane(ctx, foreign, domain.SessionLane{LaneItemID: "lane-q"}); err != nil {
		t.Fatal(err)
	}
	m := &Manager{store: st}

	cases := []struct {
		name       string
		cfg        ports.SpawnConfig
		wantLane   string
		wantParent domain.SessionID
	}{
		{"worker inherits the spawning orchestrator's lane over the backlog fallback",
			ports.SpawnConfig{Kind: domain.KindWorker, ParentSessionID: laned, LaneItemID: "other"}, "lane-a", laned},
		{"worker of an un-laned orchestrator takes the backlog parent lane",
			ports.SpawnConfig{Kind: domain.KindWorker, ParentSessionID: unlaned, LaneItemID: "bl-parent"}, "bl-parent", unlaned},
		{"parent from another project is ignored, not inherited",
			ports.SpawnConfig{Kind: domain.KindWorker, ParentSessionID: foreign}, "", ""},
		{"orchestrator binds the lane it was created for",
			ports.SpawnConfig{Kind: domain.KindOrchestrator, LaneItemID: "lane-b"}, "lane-b", ""},
		{"un-laned, parentless spawn records nothing",
			ports.SpawnConfig{Kind: domain.KindWorker}, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := mk("p", tc.cfg.Kind)
			if err := m.recordLane(ctx, id, "p", tc.cfg); err != nil {
				t.Fatal(err)
			}
			got, err := st.SessionLane(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if got.LaneItemID != tc.wantLane || got.ParentSessionID != tc.wantParent {
				t.Fatalf("lane = %+v, want lane %q parent %q", got, tc.wantLane, tc.wantParent)
			}
		})
	}
}

func TestOrchestratorForSessionPicksTheWorkersLane(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "p", Path: "/tmp/p"}); err != nil {
		t.Fatal(err)
	}
	mk := func(kind domain.SessionKind, lane string) domain.SessionID {
		rec, err := st.CreateSession(ctx, domain.SessionRecord{ProjectID: "p", Kind: kind, Harness: domain.HarnessClaudeCode})
		if err != nil {
			t.Fatal(err)
		}
		if lane != "" {
			if err := st.SetSessionLane(ctx, rec.ID, domain.SessionLane{LaneItemID: lane}); err != nil {
				t.Fatal(err)
			}
		}
		return rec.ID
	}
	def := mk(domain.KindOrchestrator, "")
	orchA := mk(domain.KindOrchestrator, "lane-a")
	m := &Manager{store: st}
	for worker, want := range map[domain.SessionID]domain.SessionID{mk(domain.KindWorker, "lane-a"): orchA, mk(domain.KindWorker, ""): def} {
		got, ok, err := m.orchestratorForSession(ctx, "p", worker)
		if err != nil || !ok || got != want {
			t.Errorf("worker %s -> %s ok=%v err=%v, want %s", worker, got, ok, err, want)
		}
	}
}

func (f *fakeStore) SetSessionLane(_ context.Context, id domain.SessionID, l domain.SessionLane) error {
	if f.lanes == nil {
		f.lanes = map[domain.SessionID]domain.SessionLane{}
	}
	f.lanes[id] = l
	return nil
}

func (f *fakeStore) SessionLane(_ context.Context, id domain.SessionID) (domain.SessionLane, error) {
	return f.lanes[id], nil
}

func (f *fakeStore) ListSessionLanes(_ context.Context, p domain.ProjectID) (map[domain.SessionID]string, error) {
	out := map[domain.SessionID]string{}
	for id, l := range f.lanes {
		if f.sessions[id].ProjectID == p {
			out[id] = l.LaneItemID
		}
	}
	return out, nil
}

// The real spawn path must persist the lane before the worker's system prompt
// is built, so the prompt names the lane's orchestrator, not the default one.
func TestSpawnWorker_InheritsLaneAndPointsAtItsOrchestrator(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	st.num = 2
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator}
	st.sessions["mer-2"] = domain.SessionRecord{ID: "mer-2", ProjectID: "mer", Kind: domain.KindOrchestrator}
	st.lanes = map[domain.SessionID]domain.SessionLane{"mer-2": {LaneItemID: "lane-a"}}
	agent := &recordingAgent{}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})

	s, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Prompt: "do it", ParentSessionID: "mer-2"})
	if err != nil {
		t.Fatal(err)
	}
	if got := st.lanes[s.ID]; got.LaneItemID != "lane-a" || got.ParentSessionID != "mer-2" {
		t.Fatalf("recorded lane = %+v, want lane-a spawned by mer-2", got)
	}
	if sp := agent.lastLaunch.SystemPrompt; !strings.Contains(sp, `tallpine send --session mer-2 `) || strings.Contains(sp, `tallpine send --session mer-1 `) {
		t.Fatalf("system prompt must point at the lane orchestrator mer-2:\n%s", sp)
	}
}

func TestCreateSessionWritesTheLaneInTheSameStep(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "p", Path: "/tmp/p"}); err != nil {
		t.Fatal(err)
	}
	rec, err := st.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "p", Kind: domain.KindOrchestrator, Harness: domain.HarnessClaudeCode,
		Lane: domain.SessionLane{LaneItemID: "lane-z"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.SessionLane(ctx, rec.ID)
	if err != nil || got.LaneItemID != "lane-z" {
		t.Fatalf("lane = %+v, err %v: the create must carry the lane", got, err)
	}
}
