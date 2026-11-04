package lifecycle

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type turnFallbackStore struct {
	*fakeStore
	fallbackWrites int
}

func (s *turnFallbackStore) UpdateSessionWithTurnFallback(_ context.Context, rec domain.SessionRecord, expected int64, _ domain.TurnFallback, _ bool) (bool, error) {
	s.fallbackWrites++
	return s.UpdateSessionFromActivitySignal(context.Background(), rec, expected)
}

// A projection must see metadata folded into rec after it was built: the
// turn-fallback staleness check reads the folded (reset) checkpoint, not the
// pre-fold row, so a Codex stop whose checkpoint was just reset still writes
// its fallback.
func TestActivityProjectionSeesMetadataFoldedAfterConstruction(t *testing.T) {
	st := &turnFallbackStore{fakeStore: newFakeStore()}
	m := New(st, &fakeMessenger{})
	rec := domain.SessionRecord{
		ID: "s1", Kind: domain.KindWorker, Mode: domain.SessionModeTUI, Harness: domain.HarnessCodex,
		Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: time.Now()},
	}
	rec.Metadata.ConversationCheckpointState = domain.ConversationCheckpointPrompt
	rec.Metadata.ConversationCheckpointTurnID = "old-turn"
	st.sessions[rec.ID] = rec
	signal := ports.ActivitySignal{Event: "stop", ProviderTurnID: "new-turn", LaunchID: "l1", AgentSessionID: "a1"}
	proj := activityProjection{id: rec.ID, rec: &rec, signal: signal, event: "stop", now: time.Now(), attempts: new(int)}
	// The reducer resets the checkpoint after the projection is built.
	rec.Metadata.ConversationCheckpointState = domain.ConversationCheckpointEmpty
	rec.Metadata.ConversationCheckpointTurnID = ""
	next := rec
	next.Activity.State = domain.ActivityIdle
	if _, _, err := m.project(context.Background(), proj, next); err != nil {
		t.Fatal(err)
	}
	if st.fallbackWrites != 1 {
		t.Fatalf("turn fallback writes = %d, want 1 (projection read a stale pre-fold row)", st.fallbackWrites)
	}
}
