package store_test

import (
	"context"
	"encoding/json"
	"testing"

	"tallpine.eu/tallpine/backend/internal/cdc"
)

// B562: the background tasks a chat lists live in daemon memory, so a change
// must still reach clients as an id-only session_updated for that session
// (frontend cdc-event.ts idOnlySessionUpdate re-reads just that session).
func TestNotifySessionRuntimeChangedAppendsIDOnlySessionUpdate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "tp")
	created, err := s.CreateSession(ctx, sampleRecord("tp"))
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.LatestSeq(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.NotifySessionRuntimeChanged(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	events, err := s.EventsAfter(ctx, before, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != cdc.EventSessionUpdated || events[0].SessionID != string(created.ID) {
		t.Fatalf("events = %+v, want one session_updated for the session", events)
	}
	var payload map[string]any
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil || len(payload) != 1 || payload["id"] != string(created.ID) {
		t.Fatalf("payload = %s, want only the id", events[0].Payload)
	}

	rec, _, err := s.GetSession(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	rec.IsTerminated = true
	if err := s.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	before, _ = s.LatestSeq(ctx)
	if err := s.NotifySessionRuntimeChanged(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if events, _ := s.EventsAfter(ctx, before, 10); len(events) != 0 {
		t.Fatalf("terminated session announced: %+v", events)
	}
}
