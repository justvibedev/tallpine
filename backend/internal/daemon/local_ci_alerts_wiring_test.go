package daemon

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type fakeAlertSessions map[domain.SessionID]domain.SessionRecord

func (f fakeAlertSessions) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	rec, ok := f[id]
	return rec, ok, nil
}

// B680: the notify-target orchestrator gets local-CI alerts while it lives;
// once it ended, its project's main orchestrator does; an unknown id or a
// worker named there gets nothing of its own.
func TestLocalCIAlertTarget(t *testing.T) {
	store := fakeAlertSessions{
		"orch-live":  {ID: "orch-live", ProjectID: "p", Kind: domain.KindOrchestrator},
		"orch-ended": {ID: "orch-ended", ProjectID: "p", Kind: domain.KindOrchestrator, IsTerminated: true},
		"orch-exit":  {ID: "orch-exit", ProjectID: "p", Kind: domain.KindOrchestrator, Activity: domain.Activity{State: domain.ActivityExited}},
		"worker":     {ID: "worker", ProjectID: "p", Kind: domain.KindWorker},
	}
	var picked []domain.ProjectID
	pick := func(_ context.Context, project domain.ProjectID, lane string) (domain.SessionID, bool) {
		picked = append(picked, project)
		return "orch-main", lane == ""
	}
	target := localCIAlertTarget(store, pick)
	for id, want := range map[domain.SessionID]domain.SessionID{
		"orch-live": "orch-live", "orch-ended": "orch-main", "orch-exit": "orch-main", "worker": "orch-main", "missing": "",
	} {
		got, ok := target(context.Background(), id)
		if got != want || ok != (want != "") {
			t.Errorf("target(%s) = %q, %v; want %q", id, got, ok, want)
		}
	}
	if len(picked) != 3 {
		t.Errorf("picker called %d times, want 3", len(picked))
	}
}
