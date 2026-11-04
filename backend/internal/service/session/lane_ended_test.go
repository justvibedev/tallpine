package session

import (
	"context"
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

// endedStore names ended lane orchestrators the way the SQL store's
// ended_lane_orchestrators table does.
type endedStore struct {
	*endStore
	endedLanes map[domain.SessionID]bool
}

func (s *endedStore) EndedLaneOrchestrators(context.Context) (map[domain.SessionID]bool, error) {
	return s.endedLanes, nil
}

// The orchestrator list leaves out an ended lane's orchestrator; the session
// list (history) keeps it.
func TestListOrchestratorsLeavesOutEndedLanes(t *testing.T) {
	t.Parallel()
	f := newEndFixture(t)
	st := &endedStore{endStore: f.st, endedLanes: map[domain.SessionID]bool{"mer-3": true}}
	st.sessions["mer-3"] = domain.SessionRecord{ID: "mer-3", ProjectID: "mer", Kind: domain.KindOrchestrator, IsTerminated: true}
	f.svc.store = st

	orchs, err := f.svc.List(context.Background(), ListFilter{OrchestratorOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[domain.SessionID]bool{}
	for _, s := range orchs {
		ids[s.ID] = true
	}
	if ids["mer-3"] || !ids["mer-1"] || !ids["mer-2"] {
		t.Fatalf("orchestrators = %v, want mer-1 and mer-2 without the ended mer-3", ids)
	}
	// mer-1 and mer-2 share the default lane here: exactly one is the current one.
	marked := []domain.SessionID{}
	for _, s := range orchs {
		if s.CurrentOrchestrator {
			marked = append(marked, s.ID)
		}
	}
	if len(marked) != 1 {
		t.Fatalf("marked current = %v, want exactly one of the two live default-lane orchestrators", marked)
	}
	all, err := f.svc.List(context.Background(), ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range all {
		found = found || s.ID == "mer-3"
	}
	if !found {
		t.Fatal("the ended lane orchestrator left session history")
	}
}

// Killing the orchestrator ends its lane in the store (the termination
// trigger), so EndLane must read the workers to notify before the kill.
func TestEndLaneNotifiesWorkersWhenTheKillEndsTheLane(t *testing.T) {
	t.Parallel()
	f := newEndFixture(t)
	kill := f.cmd.killFunc
	f.cmd.killFunc = func(id domain.SessionID) {
		kill(id)
		for w, l := range f.st.lanes {
			if l.LaneItemID == "lane-a" {
				delete(f.st.lanes, w)
			}
		}
	}
	out, err := f.svc.EndLane(context.Background(), "mer-2", false)
	if err != nil {
		t.Fatalf("EndLane: %v", err)
	}
	if len(out.Workers) != 2 || len(f.cmd.sent) != 2 {
		t.Fatalf("workers=%v notified=%v, want the two live lane-a workers", out.Workers, f.cmd.sent)
	}
}

// holdStore records lane holds around the steps of a clean replacement.
type holdStore struct {
	*fakeStore
	events *[]string
}

func (s *holdStore) HoldLane(_ context.Context, _ domain.ProjectID, lane string) error {
	*s.events = append(*s.events, "hold "+lane)
	return nil
}

func (s *holdStore) ReleaseLane(_ context.Context, _ domain.ProjectID, lane string) error {
	*s.events = append(*s.events, "release "+lane)
	return nil
}

// A clean replacement holds the lane from before the old orchestrator retires
// until the new one is spawned, so the retirement does not end the lane.
func TestCleanLaneReplacementHoldsTheLane(t *testing.T) {
	t.Parallel()
	var events []string
	fc := &fakeCommander{}
	fc.retireFunc = func(_ context.Context, id domain.SessionID) error {
		events = append(events, "retire "+string(id))
		return nil
	}
	svc := &Service{manager: fc, store: &holdStore{fakeStore: laneStore(t), events: &events}}
	if _, err := svc.SpawnLaneOrchestrator(context.Background(), "mer", true, "", "", "lane-a"); err != nil {
		t.Fatal(err)
	}
	if fc.spawnedCfg.LaneItemID != "lane-a" {
		t.Fatalf("spawned lane = %q", fc.spawnedCfg.LaneItemID)
	}
	want := "hold lane-a,retire mer-2,release lane-a"
	got := ""
	for i, e := range events {
		if i > 0 {
			got += ","
		}
		got += e
	}
	if got != want {
		t.Fatalf("events = %s, want %s", got, want)
	}

	// The default lane is not a lane: nothing to hold.
	events = nil
	if _, err := svc.SpawnLaneOrchestrator(context.Background(), "mer", true, "", "", ""); err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e != "retire mer-1" {
			t.Fatalf("default replacement events = %v", events)
		}
	}
}

// An ended lane's orchestrator cannot be restored: it would come back as a
// second default orchestrator.
func TestRestoreRefusesAnEndedLaneOrchestrator(t *testing.T) {
	t.Parallel()
	f := newEndFixture(t)
	f.svc.store = &endedStore{endStore: f.st, endedLanes: map[domain.SessionID]bool{"mer-3": true}}
	_, err := f.svc.Restore(context.Background(), "mer-3")
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Kind != apierr.KindConflict || ae.Code != "LANE_ENDED" {
		t.Fatalf("restore err = %v, want a LANE_ENDED conflict", err)
	}
}

type failingHoldStore struct{ *holdStore }

func (s *failingHoldStore) HoldLane(context.Context, domain.ProjectID, string) error {
	return errors.New("disk full")
}

// Without the hold, retiring would end the live lane: the replacement fails
// and retires nothing.
func TestCleanLaneReplacementFailsWhenTheHoldFails(t *testing.T) {
	t.Parallel()
	var events []string
	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: &failingHoldStore{&holdStore{fakeStore: laneStore(t), events: &events}}}
	if _, err := svc.SpawnLaneOrchestrator(context.Background(), "mer", true, "", "", "lane-a"); err == nil {
		t.Fatal("replacement succeeded without a lane hold")
	}
	if len(fc.retired) != 0 || fc.spawnedCfg.LaneItemID != "" {
		t.Fatalf("retired=%v spawned=%q, want nothing", fc.retired, fc.spawnedCfg.LaneItemID)
	}
}
