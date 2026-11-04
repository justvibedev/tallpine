package domain

import (
	"testing"
	"time"
)

func TestPickOrchestratorPreference(t *testing.T) {
	base := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	orch := func(id string, age int) SessionRecord {
		return SessionRecord{ID: SessionID(id), Kind: KindOrchestrator, CreatedAt: base.Add(time.Duration(age) * time.Minute)}
	}
	dead := orch("dead-a", 9)
	dead.IsTerminated = true
	recs := []SessionRecord{orch("def", 1), orch("a-old", 2), orch("a-new", 3), orch("b", 4), dead}
	lanes := map[SessionID]string{"a-old": "A", "a-new": "A", "b": "B", "dead-a": "A"}
	for lane, want := range map[string]SessionID{"A": "a-new", "B": "b", "": "def", "gone": "def"} {
		if got, ok := PickOrchestrator(recs, lanes, lane); !ok || got.ID != want {
			t.Errorf("lane %q -> %s ok=%v, want %s", lane, got.ID, ok, want)
		}
	}
	// Only laned orchestrators run: un-laned work must not park.
	if got, ok := PickOrchestrator([]SessionRecord{orch("a", 1), orch("b", 2)}, map[SessionID]string{"a": "A", "b": "B"}, ""); !ok || got.ID != "b" {
		t.Errorf("laned-only project -> %s ok=%v, want newest b", got.ID, ok)
	}
	// Laned work never crosses to another lane's orchestrator: with no live
	// orchestrator of its lane and no default, it is held.
	laneOnly := []SessionRecord{orch("a", 1), orch("b", 2)}
	laneOnlyLanes := map[SessionID]string{"a": "A", "b": "B"}
	if got, ok := PickOrchestrator(laneOnly, laneOnlyLanes, "C"); ok {
		t.Errorf("lane C with no default -> %s, want none", got.ID)
	}
	if got, ok := PickOrchestrator(laneOnly, laneOnlyLanes, "A"); !ok || got.ID != "a" {
		t.Errorf("lane A -> %s ok=%v, want its own orchestrator a", got.ID, ok)
	}
	if _, ok := PickOrchestrator([]SessionRecord{dead}, nil, ""); ok {
		t.Error("a terminated orchestrator must not be picked")
	}
}
