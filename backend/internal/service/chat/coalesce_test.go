package chat_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

func commandSnapshot(item string, kind ports.ChatEventKind, status domain.ActivityStatus, output string) ports.ChatEvent {
	detail := map[string]any{"command": "go test ./...", "terminalId": item}
	if output != "" {
		detail["output"] = output
	}
	raw, _ := json.Marshal(detail)
	return ports.ChatEvent{
		Kind: kind, ProviderTurnID: "provider-turn-1", ProviderItemID: item,
		ActivityKind: domain.ActivityKindCommand, ActivityStatus: status,
		Summary: "go test ./...", Detail: raw,
	}
}

// runHeartbeatTurn streams one command that the provider re-sends unchanged
// five times while it runs, then settles the turn.
func runHeartbeatTurn(t *testing.T, h *harness) (store.ConversationSnapshot, []string) {
	t.Helper()
	ctx := context.Background()
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "go", ClientMessageID: "c1"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	events := []ports.ChatEvent{{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"}}
	for i := range 6 {
		snapshot := commandSnapshot("tool-1", ports.ChatEventActivityStarted, domain.ActivityStatusRunning, "")
		snapshot.ProviderEventID = fmt.Sprintf("host:%d", i)
		events = append(events, snapshot)
	}
	events = append(events, ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted})
	h.conv.emit(events...)
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State.Terminal()
	})
	archive, err := h.st.ProviderEventsSince(ctx, h.ctrl.ConversationID(), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var started []string
	for _, event := range archive {
		if event.Method == string(ports.ChatEventActivityStarted) {
			started = append(started, event.PayloadJson)
		}
	}
	return snapshot, started
}

func commandActivity(t *testing.T, snapshot store.ConversationSnapshot, item string) domain.ConversationActivity {
	t.Helper()
	for _, activity := range snapshot.Activities {
		if activity.ProviderItemID == item {
			return activity
		}
	}
	t.Fatalf("no activity for %s in %+v", item, snapshot.Activities)
	return domain.ConversationActivity{}
}

// A heartbeat that re-sends a running command unchanged is archived as a
// marker (identity and arrival time only) and does not touch the activity row,
// so it adds no revision and no change_log row.
func TestHeartbeatSnapshotsAreArchivedAsMarkers(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	snapshot, started := runHeartbeatTurn(t, h)
	if len(started) != 6 {
		t.Fatalf("archived %d activity.started rows, want all 6 identities", len(started))
	}
	if strings.Contains(started[0], `"unchanged"`) || !strings.Contains(started[0], `go test ./...`) {
		t.Fatalf("first snapshot must be archived whole: %s", started[0])
	}
	for _, payload := range started[1:] {
		var marker map[string]any
		if err := json.Unmarshal([]byte(payload), &marker); err != nil {
			t.Fatal(err)
		}
		if marker["unchanged"] != true || marker["providerItemId"] != "tool-1" || marker["providerEventId"] == "" ||
			strings.Contains(payload, "go test") {
			t.Fatalf("repeat archived as %s, want an unchanged marker", payload)
		}
	}
	// The one revision is the turn completion settling the running command.
	if activity := commandActivity(t, snapshot, "tool-1"); activity.Revision != 1 {
		t.Fatalf("revision = %d, want only the turn completion's (1)", activity.Revision)
	}
}

// With storage compaction off every heartbeat is stored and written in full,
// as before.
func TestHeartbeatSnapshotsAreStoredWholeWithCompactionOff(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.st.SetCoalesceWrites(false)
	snapshot, started := runHeartbeatTurn(t, h)
	for _, payload := range started {
		if strings.Contains(payload, `"unchanged"`) || !strings.Contains(payload, "go test ./...") {
			t.Fatalf("compaction off archived %s", payload)
		}
	}
	if activity := commandActivity(t, snapshot, "tool-1"); activity.Revision != 6 {
		t.Fatalf("revision = %d, want one per heartbeat (5) plus the turn completion's", activity.Revision)
	}
}

// ACP sends every output chunk as a delta plus a snapshot carrying all output
// so far. Once output streams into command_output, running snapshots drop their
// copy of it, so they stop rewriting the row and archive as markers; the client
// still reads the full output, and the completing snapshot keeps its own.
func TestStreamedCommandOutputIsNotRewrittenPerChunk(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "go", ClientMessageID: "c1"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	events := []ports.ChatEvent{
		{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"},
		commandSnapshot("tool-1", ports.ChatEventActivityStarted, domain.ActivityStatusRunning, ""),
	}
	var output strings.Builder
	const chunks = 40
	for i := range chunks {
		line := fmt.Sprintf("ok  pkg/%02d\n", i)
		output.WriteString(line)
		events = append(events,
			ports.ChatEvent{Kind: ports.ChatEventCommandOutputDelta, ProviderTurnID: "provider-turn-1", ProviderItemID: "tool-1", Delta: line},
			commandSnapshot("tool-1", ports.ChatEventActivityStarted, domain.ActivityStatusRunning, output.String()),
		)
	}
	events = append(events,
		commandSnapshot("tool-1", ports.ChatEventActivityCompleted, domain.ActivityStatusCompleted, output.String()),
		ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted},
	)
	h.conv.emit(events...)
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State.Terminal()
	})

	activity := commandActivity(t, snapshot, "tool-1")
	if activity.CommandOutput != output.String() || activity.Status != domain.ActivityStatusCompleted {
		t.Fatalf("activity = status %s output %q, want completed with every chunk", activity.Status, activity.CommandOutput)
	}
	var detail map[string]any
	if err := json.Unmarshal(activity.Detail, &detail); err != nil || detail["output"] != output.String() {
		t.Fatalf("completed detail = %s err=%v, want the provider's full output", activity.Detail, err)
	}
	// Created once, then one revision per command_output write and one for the
	// completion: far fewer than the 2 per chunk the old path wrote.
	if activity.Revision >= chunks {
		t.Fatalf("revision = %d for %d chunks, want the snapshots to stop rewriting the row", activity.Revision, chunks)
	}

	archive, err := h.st.ProviderEventsSince(ctx, h.ctrl.ConversationID(), 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var started, withOutput int
	for _, event := range archive {
		if event.Method != string(ports.ChatEventActivityStarted) {
			continue
		}
		started++
		if strings.Contains(event.PayloadJson, `"output"`) {
			withOutput++
		}
	}
	if started != chunks+1 {
		t.Fatalf("archived %d snapshots, want %d", started, chunks+1)
	}
	// Only a snapshot projected before its item's first output delta landed can
	// still carry output.
	if withOutput > 1 {
		t.Fatalf("%d running snapshots archived their output copy, want at most 1", withOutput)
	}
}
