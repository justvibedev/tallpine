package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
)

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

func laneStore(t *testing.T) *fakeStore {
	t.Helper()
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator}
	st.sessions["mer-2"] = domain.SessionRecord{ID: "mer-2", ProjectID: "mer", Kind: domain.KindOrchestrator}
	st.lanes = map[domain.SessionID]domain.SessionLane{"mer-2": {LaneItemID: "lane-a"}}
	return st
}

// A clean start in one lane replaces only that lane's orchestrator; the other
// lanes' chats keep running.
func TestSpawnLaneOrchestratorCleanRetiresOnlyThatLane(t *testing.T) {
	t.Parallel()
	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: laneStore(t)}
	if _, err := svc.SpawnLaneOrchestrator(context.Background(), "mer", true, "", "", "lane-a"); err != nil {
		t.Fatal(err)
	}
	if len(fc.retired) != 1 || fc.retired[0] != "mer-2" {
		t.Fatalf("retired = %v, want only the lane-a orchestrator mer-2", fc.retired)
	}
	if fc.spawnedCfg.LaneItemID != "lane-a" {
		t.Fatalf("spawned lane = %q, want lane-a", fc.spawnedCfg.LaneItemID)
	}
}

// Ensuring the default orchestrator must not hand back a lane's orchestrator,
// and ensuring a lane starts one when only the default runs.
func TestSpawnLaneOrchestratorEnsureIsPerLane(t *testing.T) {
	t.Parallel()
	st := laneStore(t)
	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}
	got, err := svc.SpawnLaneOrchestrator(context.Background(), "mer", false, "", "", "")
	if err != nil || got.ID != "mer-1" || fc.spawned {
		t.Fatalf("default ensure = %s spawned=%v err=%v, want the running default mer-1", got.ID, fc.spawned, err)
	}
	if got, err = svc.SpawnLaneOrchestrator(context.Background(), "mer", false, "", "", "lane-a"); err != nil || got.ID != "mer-2" || fc.spawned {
		t.Fatalf("lane ensure = %s spawned=%v err=%v, want the running lane orchestrator mer-2", got.ID, fc.spawned, err)
	}
	if _, err = svc.SpawnLaneOrchestrator(context.Background(), "mer", false, "", "", "lane-b"); err != nil || !fc.spawned || fc.spawnedCfg.LaneItemID != "lane-b" {
		t.Fatalf("new lane must spawn a new orchestrator: spawned=%v cfg=%+v err=%v", fc.spawned, fc.spawnedCfg, err)
	}
}

// laneBacklogStore serves lane refs from items and counts the lane reads.
type laneBacklogStore struct {
	*fakeStore
	items     map[string]domain.BacklogItem
	laneLists int
	refCalls  int
}

func (s *laneBacklogStore) ListSessionLanes(ctx context.Context, p domain.ProjectID) (map[domain.SessionID]string, error) {
	s.laneLists++
	return s.fakeStore.ListSessionLanes(ctx, p)
}

func (s *laneBacklogStore) BacklogRefsByID(_ context.Context, ids []string) (map[string]string, error) {
	s.refCalls++
	out := map[string]string{}
	for _, id := range ids {
		if item, ok := s.items[id]; ok {
			out[id] = item.Ref()
		}
	}
	return out, nil
}

// The session read model labels a lane orchestrator with its backlog ref; the
// default orchestrator and workers get none, and an orchestrator whose lane
// item was deleted is still shown as laned, never as the default one.
func TestLaneLabelIsTheBacklogRefOfALaneOrchestrator(t *testing.T) {
	t.Parallel()
	st := laneStore(t)
	st.sessions["mer-3"] = domain.SessionRecord{ID: "mer-3", ProjectID: "mer", Kind: domain.KindWorker}
	st.lanes["mer-3"] = domain.SessionLane{LaneItemID: "bl-1"}
	st.sessions["mer-4"] = domain.SessionRecord{ID: "mer-4", ProjectID: "mer", Kind: domain.KindOrchestrator}
	st.lanes["mer-4"] = domain.SessionLane{LaneItemID: "gone"}
	st.lanes["mer-2"] = domain.SessionLane{LaneItemID: "bl-1"}
	svc := &Service{store: &laneBacklogStore{fakeStore: st, items: map[string]domain.BacklogItem{"bl-1": {ID: "bl-1", Seq: 12}}}}
	var recs []domain.SessionRecord
	for _, id := range []domain.SessionID{"mer-1", "mer-2", "mer-3", "mer-4"} {
		recs = append(recs, st.sessions[id])
	}
	st.sessions["mer-5"] = domain.SessionRecord{ID: "mer-5", ProjectID: "mer", Kind: domain.KindOrchestrator}
	st.lanes["mer-5"] = domain.SessionLane{LaneItemID: "gone-too"}
	recs = append(recs, st.sessions["mer-5"])
	batch := svc.loadLaneLabels(context.Background(), recs)
	if batch.label("mer-4") == batch.label("mer-5") {
		t.Errorf("two deleted lanes share label %q", batch.label("mer-4"))
	}
	for id, want := range map[domain.SessionID]string{"mer-2": "B12", "mer-1": "", "mer-3": "", "mer-4": deletedLaneLabel("gone")} {
		if got := batch.label(id); got != want {
			t.Errorf("label(%s) = %q, want %q", id, got, want)
		}
	}
}

// A worker spawned in a lane carries that lane's backlog ref as WorkerLane (not
// as Lane); orchestrators and un-laned workers carry none.
func TestWorkerLaneIsTheBacklogRefOfItsLane(t *testing.T) {
	t.Parallel()
	st := laneStore(t)
	st.sessions["mer-3"] = domain.SessionRecord{ID: "mer-3", ProjectID: "mer", Kind: domain.KindWorker}
	st.sessions["mer-4"] = domain.SessionRecord{ID: "mer-4", ProjectID: "mer", Kind: domain.KindWorker}
	st.lanes["mer-3"] = domain.SessionLane{LaneItemID: "bl-1", ParentSessionID: "mer-2"}
	st.lanes["mer-2"] = domain.SessionLane{LaneItemID: "bl-1"}
	svc := &Service{store: &laneBacklogStore{fakeStore: st, items: map[string]domain.BacklogItem{"bl-1": {ID: "bl-1", Seq: 12}}}}
	var recs []domain.SessionRecord
	for _, id := range []domain.SessionID{"mer-1", "mer-2", "mer-3", "mer-4"} {
		recs = append(recs, st.sessions[id])
	}
	batch := svc.loadLaneLabels(context.Background(), recs)
	for id, want := range map[domain.SessionID]string{"mer-3": "B12", "mer-4": "", "mer-2": "", "mer-1": ""} {
		if got := batch.workerLabel(id); got != want {
			t.Errorf("workerLabel(%s) = %q, want %q", id, got, want)
		}
	}
	if got := batch.label("mer-3"); got != "" {
		t.Errorf("a worker must not carry Lane, got %q", got)
	}
}

// The session read model (what GET /api/v1/sessions serializes) exposes the
// worker's lane as workerLane, and an orchestrator's as lane only.
func TestSessionReadModelCarriesWorkerLane(t *testing.T) {
	t.Parallel()
	st := laneStore(t)
	st.sessions["mer-3"] = domain.SessionRecord{ID: "mer-3", ProjectID: "mer", Kind: domain.KindWorker}
	st.lanes["mer-3"] = domain.SessionLane{LaneItemID: "bl-1", ParentSessionID: "mer-2"}
	st.lanes["mer-2"] = domain.SessionLane{LaneItemID: "bl-1"}
	svc := &Service{store: &laneBacklogStore{fakeStore: st, items: map[string]domain.BacklogItem{"bl-1": {ID: "bl-1", Seq: 12}}}}
	worker, err := svc.Get(context.Background(), "mer-3")
	if err != nil || worker.WorkerLane != "B12" || worker.Lane != "" {
		t.Fatalf("worker WorkerLane=%q Lane=%q err=%v, want B12 and empty", worker.WorkerLane, worker.Lane, err)
	}
	orch, err := svc.Get(context.Background(), "mer-2")
	if err != nil || orch.Lane != "B12" || orch.WorkerLane != "" {
		t.Fatalf("orchestrator Lane=%q WorkerLane=%q err=%v, want B12 and empty", orch.Lane, orch.WorkerLane, err)
	}
	b, _ := json.Marshal(worker)
	if !strings.Contains(string(b), `"workerLane":"B12"`) {
		t.Fatalf("wire JSON lacks workerLane: %s", b)
	}
}

// Enriching a list reads lanes once per project and refs once, however many
// orchestrators it holds.
func TestLaneLabelQueriesDoNotScaleWithOrchestrators(t *testing.T) {
	t.Parallel()
	st := laneStore(t)
	store := &laneBacklogStore{fakeStore: st, items: map[string]domain.BacklogItem{}}
	var recs []domain.SessionRecord
	for i := 0; i < 8; i++ {
		id := domain.SessionID(fmt.Sprintf("orch-%d", i))
		lane := fmt.Sprintf("bl-%d", i)
		st.sessions[id] = domain.SessionRecord{ID: id, ProjectID: "mer", Kind: domain.KindOrchestrator}
		st.lanes[id] = domain.SessionLane{LaneItemID: lane}
		store.items[lane] = domain.BacklogItem{ID: lane, Seq: int64(i + 1)}
		recs = append(recs, st.sessions[id])
	}
	batch := (&Service{store: store}).loadLaneLabels(context.Background(), recs)
	if store.laneLists != 1 || store.refCalls != 1 {
		t.Fatalf("lane list calls = %d, ref calls = %d for 8 orchestrators, want 1 and 1", store.laneLists, store.refCalls)
	}
	if got := batch.label("orch-3"); got != "B4" {
		t.Fatalf("label = %q, want B4", got)
	}
}

// A lane orchestrator's branch carries the lane slug; replacement verification
// must expect it, not fail a running orchestrator (review of B88 data-loss fix).
func TestSpawnLaneOrchestratorAcceptsLaneBranch(t *testing.T) {
	t.Parallel()
	fc := &fakeCommander{spawnFunc: func(cfg ports.SpawnConfig) domain.SessionRecord {
		rec := domain.SessionRecord{ID: "mer-9", ProjectID: cfg.ProjectID, Kind: cfg.Kind, Harness: cfg.Harness}
		rec.Metadata.Branch = sessionmanager.DefaultOrchestratorBranch("mer", "") + "-lane-b"
		return rec
	}}
	svc := &Service{manager: fc, store: laneStore(t)}
	if _, err := svc.SpawnLaneOrchestrator(context.Background(), "mer", false, "", "", "lane-b"); err != nil {
		t.Fatalf("lane orchestrator with a lane-suffixed branch rejected: %v", err)
	}
}

// The retire notice names the lane being replaced, by its backlog ref; the
// project's default orchestrator keeps the project wording.
func TestRetireNoticeNamesTheLane(t *testing.T) {
	t.Parallel()
	st := &laneBacklogStore{fakeStore: laneStore(t), items: map[string]domain.BacklogItem{
		"lane-a": {ID: "lane-a", ProjectID: "mer", Title: "Auth", Seq: 7},
	}}
	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}
	want := st.items["lane-a"].Ref()
	if _, err := svc.SpawnLaneOrchestrator(context.Background(), "mer", true, "", "", "lane-a"); err != nil {
		t.Fatal(err)
	}
	if len(fc.sentMessages) != 1 || !strings.Contains(fc.sentMessages[0], "lane "+want) || strings.Contains(fc.sentMessages[0], "project orchestrator") {
		t.Fatalf("lane retire notice = %q, want it to name lane %s", fc.sentMessages, want)
	}
	fc.sentMessages = nil
	if _, err := svc.SpawnLaneOrchestrator(context.Background(), "mer", true, "", "", ""); err != nil {
		t.Fatal(err)
	}
	if len(fc.sentMessages) != 1 || !strings.Contains(fc.sentMessages[0], "project orchestrator") {
		t.Fatalf("default retire notice = %q, want the project wording", fc.sentMessages)
	}
}

// A terminated worker keeps its session_lane row but must not advertise the
// lane: clients that list ended sessions would otherwise show a lane nobody runs.
func TestTerminatedWorkerCarriesNoWorkerLane(t *testing.T) {
	t.Parallel()
	st := laneStore(t)
	st.sessions["mer-3"] = domain.SessionRecord{ID: "mer-3", ProjectID: "mer", Kind: domain.KindWorker, IsTerminated: true}
	st.sessions["mer-4"] = domain.SessionRecord{ID: "mer-4", ProjectID: "mer", Kind: domain.KindWorker}
	st.lanes["mer-3"] = domain.SessionLane{LaneItemID: "bl-1"}
	st.lanes["mer-4"] = domain.SessionLane{LaneItemID: "bl-1"}
	svc := &Service{store: &laneBacklogStore{fakeStore: st, items: map[string]domain.BacklogItem{"bl-1": {ID: "bl-1", Seq: 12}}}}
	batch := svc.loadLaneLabels(context.Background(), []domain.SessionRecord{st.sessions["mer-3"], st.sessions["mer-4"]})
	if got := batch.workerLabel("mer-3"); got != "" {
		t.Errorf("terminated worker workerLane = %q, want empty", got)
	}
	if got := batch.workerLabel("mer-4"); got != "B12" {
		t.Errorf("active worker workerLane = %q, want B12", got)
	}
}

// The brief rides the spawn config (the queued first turn the async spawn drains
// once the chat controller is live); a running orchestrator is returned without
// delivering it.
func TestSpawnLaneOrchestratorBriefRidesTheSpawn(t *testing.T) {
	t.Parallel()
	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: laneStore(t)}
	_, created, err := svc.SpawnLaneOrchestratorBrief(context.Background(), "mer", false, "", "", "lane-b", "do the work")
	if err != nil || !created || fc.spawnedCfg.Prompt != "do the work" {
		t.Fatalf("created=%v err=%v prompt=%q", created, err, fc.spawnedCfg.Prompt)
	}
	fc2 := &fakeCommander{}
	svc = &Service{manager: fc2, store: laneStore(t)}
	_, created, err = svc.SpawnLaneOrchestratorBrief(context.Background(), "mer", false, "", "", "", "do the work")
	if err != nil || created || fc2.spawned {
		t.Fatalf("running orchestrator: created=%v spawned=%v err=%v", created, fc2.spawned, err)
	}
}
