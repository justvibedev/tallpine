package lifecycle

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// newSubagentStore returns a fake store holding one idle TUI session "tp-1" on
// launch-1 for the given harness, last active at start.
func newSubagentStore(harness domain.AgentHarness, start time.Time) *fakeStore {
	store := newFakeStore()
	store.sessions["tp-1"] = domain.SessionRecord{
		ID: "tp-1", Harness: harness, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: start},
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch-1"},
	}
	return store
}

// applyLaunch1Signals applies each signal to tp-1 on launch-1 in order.
func applyLaunch1Signals(t *testing.T, m *Manager, sigs []ports.ActivitySignal) {
	t.Helper()
	for _, sig := range sigs {
		sig.LaunchID = "launch-1"
		if err := m.ApplyActivitySignal(context.Background(), "tp-1", sig); err != nil {
			t.Fatal(err)
		}
	}
}
