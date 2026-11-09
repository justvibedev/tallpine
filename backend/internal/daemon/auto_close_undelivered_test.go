package daemon

import (
	"context"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/service/turnnotify"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

type closingApplier func(context.Context, domain.SessionID)

func (f closingApplier) ApplyActivitySignal(ctx context.Context, id domain.SessionID, _ ports.ActivitySignal) error {
	f(ctx, id)
	return nil
}

type recordSessions map[domain.SessionID]domain.SessionRecord

func (r recordSessions) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	rec, ok := r[id]
	return rec, ok, nil
}

// B557: a worker that answers in turn text and is closed by that same Stop
// signal still reaches the orchestrator, even the one that merged its PR
// (which B560 otherwise spares the notice).
func TestAutoCloseNotice_CarriesAnswerFromTheClosingStop(t *testing.T) {
	rec := domain.SessionRecord{ID: "w-1", ProjectID: "p1", Kind: domain.KindWorker}
	var sent []string
	var hooks autoCloseHooks
	observer := turnnotify.New(turnnotify.Deps{
		Sessions: recordSessions{rec.ID: rec},
		Next: closingApplier(func(ctx context.Context, _ domain.SessionID) {
			hooks.NotifyClosed(ctx, rec, "orch-1", hooks.UndeliveredTurnResult(ctx, rec))
		}),
	})
	hooks = autoCloseHooks{
		store:        sqlitetest.MustOpen(t),
		turns:        observer,
		orchestrator: func(context.Context, domain.ProjectID, string) (domain.SessionID, bool) { return "orch-1", true },
		send: func(_ context.Context, _ domain.SessionID, msg string) error {
			sent = append(sent, msg)
			return nil
		},
	}
	if err := observer.ApplyActivitySignal(context.Background(), rec.ID, ports.ActivitySignal{Event: "stop", FinalAssistantMessage: "the read-only answer"}); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || !strings.HasPrefix(sent[0], "[auto] session_closed:") || !strings.Contains(sent[0], "the read-only answer") {
		t.Fatalf("notices = %q", sent)
	}
}

func TestFinishedClosedNotice_CarriesUndeliveredResult(t *testing.T) {
	var sent []string
	h := autoCloseHooks{
		store:        sqlitetest.MustOpen(t),
		orchestrator: func(context.Context, domain.ProjectID, string) (domain.SessionID, bool) { return "orch-1", true },
		send: func(_ context.Context, _ domain.SessionID, msg string) error {
			sent = append(sent, msg)
			return nil
		},
	}
	h.NotifyFinishedClosed(context.Background(), domain.SessionRecord{ID: "w-1", ProjectID: "p1"}, "[auto] turn_ended: x")
	if len(sent) != 1 || !strings.Contains(sent[0], "[auto] turn_ended: x") {
		t.Fatalf("notices = %q", sent)
	}
}
