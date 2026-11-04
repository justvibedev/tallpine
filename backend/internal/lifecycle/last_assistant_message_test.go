package lifecycle

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type lastMessageStore struct {
	*fakeStore
	got []string
}

func (s *lastMessageStore) RecordLastAssistantMessage(_ context.Context, _ domain.SessionID, message string, _ time.Time) error {
	s.got = append(s.got, message)
	return nil
}

func TestLastAssistantMessageIsRecordedFromMainTurnEnds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rec     domain.SessionRecord
		signals []ports.ActivitySignal
		want    []string
	}{
		{
			name: "terminal stop, every harness",
			rec: domain.SessionRecord{ID: "s-1", Kind: domain.KindOrchestrator, Harness: domain.HarnessKiro, Mode: domain.SessionModeTUI,
				Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch"}},
			signals: []ports.ActivitySignal{
				{Event: "user-prompt-submit", TurnUserRequest: "status?", LaunchID: "launch"},
				{Event: "stop", SubagentID: "helper", FinalAssistantMessage: "helper done", LaunchID: "launch"},
				{Event: "stop", FinalAssistantMessage: "all three merged", LaunchID: "launch"},
				{Event: "stop", LaunchID: "launch"}, // no text: keeps the last one
				{Event: "stop", FinalAssistantMessage: "stale launch", LaunchID: "old"},
			},
			want: []string{"all three merged"},
		},
		{
			name: "chat turn completion",
			rec: domain.SessionRecord{ID: "s-1", Kind: domain.KindOrchestrator, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat,
				Metadata: domain.SessionMetadata{ControllerGeneration: "g1"}},
			signals: []ports.ActivitySignal{
				{Event: "chat.turn.completed", FinalAssistantMessage: "EVAL-COMPLETE", ControllerGeneration: "g1"},
				{Event: "chat.turn.completed", FinalAssistantMessage: "old controller", ControllerGeneration: "g0"},
				{Event: "stop", FinalAssistantMessage: "inherited hook"},
			},
			want: []string{"EVAL-COMPLETE"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &lastMessageStore{fakeStore: newFakeStore()}
			store.sessions["s-1"] = tc.rec
			manager := New(store, nil)
			for _, signal := range tc.signals {
				signal.Valid, signal.State = true, domain.ActivityIdle
				if err := manager.ApplyActivitySignal(context.Background(), "s-1", signal); err != nil {
					t.Fatal(err)
				}
			}
			if strings.Join(store.got, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("recorded %q, want %q", store.got, tc.want)
			}
		})
	}
}

func TestLastAssistantMessageIsBounded(t *testing.T) {
	store := &lastMessageStore{fakeStore: newFakeStore()}
	store.sessions["s-1"] = domain.SessionRecord{ID: "s-1", Harness: domain.HarnessCodex, Mode: domain.SessionModeTUI,
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch"}}
	long := strings.Repeat("é", MaxLastAssistantMessageBytes) // two bytes per rune
	if err := New(store, nil).ApplyActivitySignal(context.Background(), "s-1", ports.ActivitySignal{
		Valid: true, State: domain.ActivityIdle, Event: "stop", FinalAssistantMessage: long, LaunchID: "launch",
	}); err != nil {
		t.Fatal(err)
	}
	if len(store.got) != 1 || len(store.got[0]) > MaxLastAssistantMessageBytes || !utf8.ValidString(store.got[0]) {
		t.Fatalf("recorded %d bytes (valid=%v)", len(store.got[0]), utf8.ValidString(store.got[0]))
	}
}
