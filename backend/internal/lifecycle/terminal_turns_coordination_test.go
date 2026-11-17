package lifecycle

import (
	"context"
	"reflect"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// The answer to a handoff or continuation prompt (coordination, no text) is
// handed to the recorder, which keeps it under a neutral placeholder request.
func TestStopAfterATextlessCoordinationPromptReachesTheRecorder(t *testing.T) {
	store := &turnRecordingStore{fakeStore: newFakeStore()}
	store.sessions["s-1"] = domain.SessionRecord{ID: "s-1", Harness: domain.HarnessCodex, Mode: domain.SessionModeTUI,
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch", AgentSessionID: "thread-1", AgentSessionIDLaunchID: "launch"}}
	manager := New(store, nil)
	for _, signal := range []ports.ActivitySignal{
		{Event: "user-prompt-submit", ConversationCheckpointOrigin: domain.ConversationCheckpointOriginCoordination},
		{Event: "stop", FinalAssistantMessage: "continuing the task"},
	} {
		manager.recordTerminalTurn(context.Background(), store.sessions["s-1"], signal, time.Now())
	}
	if want := []string{"response:", "response:continuing the task"}; !reflect.DeepEqual(store.calls, want) {
		t.Fatalf("calls = %v, want %v", store.calls, want)
	}
}
