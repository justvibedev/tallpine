package store_test

import (
	"context"
	"testing"
	"time"
)

func TestLastAssistantMessageKeepsTheNewestTurnEnd(t *testing.T) {
	s, session := newTerminalTurnSession(t, "last-message")
	ctx := context.Background()
	if _, _, found, err := s.LastAssistantMessage(ctx, session.ID); err != nil || found {
		t.Fatalf("before any turn: found=%v err=%v", found, err)
	}
	at := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	for _, step := range []struct {
		message string
		at      time.Time
	}{
		{"first answer", at},
		{"second answer", at.Add(time.Minute)},
		{"delayed older answer", at.Add(30 * time.Second)},
		{"   ", at.Add(2 * time.Minute)},
	} {
		if err := s.RecordLastAssistantMessage(ctx, session.ID, step.message, step.at); err != nil {
			t.Fatalf("record %q: %v", step.message, err)
		}
	}
	message, got, found, err := s.LastAssistantMessage(ctx, session.ID)
	if err != nil || !found || message != "second answer" || !got.Equal(at.Add(time.Minute)) {
		t.Fatalf("last = %q at %v (found=%v err=%v), want the second answer", message, got, found, err)
	}
}
