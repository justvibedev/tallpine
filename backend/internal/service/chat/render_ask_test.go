package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

func askRows(s store.ConversationSnapshot) []domain.ConversationActivity {
	var rows []domain.ConversationActivity
	for _, a := range s.Activities {
		var d struct{ Event string }
		if a.Kind == domain.ActivityKindSystem && json.Unmarshal(a.Detail, &d) == nil && d.Event == "ask" {
			rows = append(rows, a)
		}
	}
	return rows
}

// An ask lands on the running turn as an activity with event "ask" and the ask's
// id and ref, so the strip can find it and the transcript never folds it away.
func TestRecordAskLandsOnTheRunningTurn(t *testing.T) {
	t.Parallel()
	h, _ := steerHarness(t)

	turn, activity, err := h.svc.RecordAsk(context.Background(), testSession, domain.OwnerAsk{ID: "ask-1", Ref: "Q3", Text: "Ship it?"})
	if err != nil || turn != "provider-turn-1" || activity == "" {
		t.Fatalf("RecordAsk = %q %q %v", turn, activity, err)
	}
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(askRows(s)) == 1 })
	row := askRows(snapshot)[0]
	var d struct {
		Ask struct{ ID, Ref string } `json:"ask"`
	}
	_ = json.Unmarshal(row.Detail, &d)
	if row.ID != activity || d.Ask.ID != "ask-1" || d.Ask.Ref != "Q3" || row.Summary != "Q3: Ship it?" {
		t.Fatalf("row = %+v detail %+v", row, d)
	}
}

func TestRecordAskWithoutARunningTurnHasNoAnchor(t *testing.T) {
	t.Parallel()
	h := newHarnessForHarness(t, domain.HarnessCodex)
	_, _, err := h.svc.RecordAsk(context.Background(), testSession, domain.OwnerAsk{ID: "ask-1", Ref: "Q1", Text: "x"})
	if !errors.Is(err, chatsvc.ErrNoActiveTurn) {
		t.Fatalf("err = %v, want ErrNoActiveTurn", err)
	}
}
