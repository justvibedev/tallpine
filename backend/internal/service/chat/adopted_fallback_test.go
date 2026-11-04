package chat_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

func fallbackTestDB(t *testing.T, st *store.Store) *sql.DB {
	t.Helper()
	p, _, err := st.GetProject(context.Background(), string(testProject))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(p.Path, "ao.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func fallbackDispositions(t *testing.T, st *store.Store) map[string]string {
	t.Helper()
	db := fallbackTestDB(t, st)
	rows, err := db.Query("SELECT turn_id, disposition FROM turn_fallbacks")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var id, state string
		if err = rows.Scan(&id, &state); err != nil {
			t.Fatal(err)
		}
		result[id] = state
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func makeFallbackWorker(t *testing.T, st *store.Store) {
	t.Helper()
	rec, _, err := st.GetSession(context.Background(), testSession)
	if err != nil {
		t.Fatal(err)
	}
	rec.Kind = domain.KindWorker
	if err = st.UpdateSession(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
}

func awaitFallbackReceipt(t *testing.T, acks <-chan string, wanted string) {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case id := <-acks:
			if id == wanted {
				return
			}
		case <-timer.C:
			t.Fatalf("missing receipt %s", wanted)
		}
	}
}

// Use the real failed-bind/reconnect path: the original Tallpine user turn remains
// unbound, and journal replay adopts a different row without a user message.
func TestAdoptedPrimaryReportSuppressionAfterBindFailure(t *testing.T) {
	for _, state := range []domain.ReportState{domain.ReportDone, domain.ReportNeedsInput, domain.ReportStuck, domain.ReportCheckpoint} {
		t.Run(string(state), func(t *testing.T) {
			ctx := context.Background()
			first := newFakeConversation()
			h := newHarnessWithConversationAndStore(t, &terminatingConversation{fakeConversation: first}, func(st *store.Store) chatsvc.Store { return &enospcBindStore{Store: st} })
			makeFallbackWorker(t, h.st)
			if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "inspect this", ClientMessageID: "unbound", Origin: domain.MessageOriginHuman}); !errors.Is(err, syscall.ENOSPC) {
				t.Fatalf("bind error=%v", err)
			}
			before, err := h.st.LoadConversationSnapshot(ctx, h.ctrl.ConversationID())
			if err != nil || len(before.Turns) != 1 || before.Turns[0].ProviderTurnID != "" {
				t.Fatalf("unbound fixture: %+v %v", before.Turns, err)
			}
			h.svc.StopAll(ctx)
			h.ctrl.Wait()
			acks := make(chan string, 16)
			provider := &reconnectedReceiptConversation{receiptConversation: &receiptConversation{terminatingConversation: &terminatingConversation{fakeConversation: newFakeConversation()}, ack: func(id string) error { acks <- id; return nil }}}
			var ids atomic.Int64
			svc := chatsvc.New(chatsvc.Options{Store: h.st, Sessions: h.st, Reader: fullSnapshotReader(h.st), Drivers: fakeRegistry{driver: fakeDriver{conv: provider}}, Log: slog.New(slog.DiscardHandler), NewID: func() string { return fmt.Sprintf("adopt-%d", ids.Add(1)) }, Now: h.now})
			t.Cleanup(func() { svc.StopAll(ctx) })
			ctrl, err := svc.Start(ctx, chatsvc.StartConfig{SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1"})
			if err != nil {
				t.Fatal(err)
			}
			start := ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1", ProviderEventID: "adopted-start"}
			provider.emit(start)
			awaitFallbackReceipt(t, acks, start.ProviderEventID)
			snapshot, err := h.st.LoadConversationSnapshot(ctx, ctrl.ConversationID())
			if err != nil {
				t.Fatal(err)
			}
			var adopted domain.ConversationTurn
			for _, turn := range snapshot.Turns {
				if turn.ProviderTurnID == start.ProviderTurnID {
					adopted = turn
				}
			}
			if adopted.ID == "" || adopted.ID == before.Turns[0].ID {
				t.Fatal("journal did not adopt a distinct turn")
			}
			for _, msg := range snapshot.Messages {
				if msg.TurnID == adopted.ID && msg.Role == domain.MessageRoleUser {
					t.Fatal("adopted fixture unexpectedly has user correlation")
				}
			}
			reports := reportsvc.New(reportsvc.Deps{Store: h.st, Now: h.now})
			if _, err = reports.Create(ctx, reportsvc.CreateInput{SessionID: testSession, State: state, Note: "explicit result"}); err != nil {
				t.Fatal(err)
			}
			// Duplicate journal delivery and auxiliary starts must not replace the
			// accepted primary's durable suppression owner.
			provider.emit(start,
				ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "child", ProviderConversationID: "child-thread", ProviderEventID: "nested"},
				ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "auxiliary", ProviderEventID: "auxiliary"},
				ports.ChatEvent{Kind: ports.ChatEventControllerState, ControllerState: ports.ChatControllerBusy, ProviderEventID: "barrier"})
			awaitFallbackReceipt(t, acks, "barrier")
			dispositions := fallbackDispositions(t, h.st)
			want := "suppressed"
			if state == domain.ReportCheckpoint {
				want = "active"
			}
			if len(dispositions) != 1 || dispositions[adopted.ID] != want {
				t.Fatalf("ownership=%v want only %s=%s", dispositions, adopted.ID, want)
			}
			end := ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: start.ProviderTurnID, ProviderEventID: "completed", TurnState: domain.TurnStateCompleted}
			provider.emit(end)
			awaitFallbackReceipt(t, acks, end.ProviderEventID)
			provider.emit(end)
			awaitFallbackReceipt(t, acks, end.ProviderEventID)
			svc.StopAll(ctx)
			ctrl.Wait()
			project, _, err := h.st.GetProject(ctx, string(testProject))
			if err != nil {
				t.Fatal(err)
			}
			if err = h.st.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := sqlite.OpenPreMigrated(project.Path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			h.advance(domain.TurnFallbackDelay)
			reports = reportsvc.New(reportsvc.Deps{Store: reopened, Now: h.now})
			id := (domain.TurnFallback{SessionID: testSession, Mode: domain.SessionModeChat, TurnID: adopted.ID}).StableID()
			_, err = reports.Create(ctx, reportsvc.CreateInput{ID: id, TurnFallbackID: id, SessionID: testSession, Synthesized: true, Message: "fallback"})
			if state == domain.ReportCheckpoint {
				if err != nil {
					t.Fatalf("progress hid final: %v", err)
				}
			} else if !errors.Is(err, domain.ErrTurnFallbackInactive) {
				t.Fatalf("explicit result did not suppress due fallback: %v", err)
			}
			if _, err = reports.Create(ctx, reportsvc.CreateInput{ID: id, TurnFallbackID: id, SessionID: testSession, Synthesized: true, Message: "fallback"}); err == nil {
				t.Fatal("replayed delivery duplicated report")
			}
		})
	}
}

func TestPrimaryBindOwnershipRollsBackAtomically(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	makeFallbackWorker(t, h.st)
	db := fallbackTestDB(t, h.st)
	if _, err := db.Exec(`CREATE TRIGGER reject_start_owner BEFORE INSERT ON turn_fallbacks BEGIN SELECT RAISE(ABORT, 'active ownership failed'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "bound work", ClientMessageID: "bind-rollback", Origin: domain.MessageOriginHuman}); err == nil {
		t.Fatal("owner failure accepted bind")
	}
	rows, err := h.st.LoadConversationSnapshot(ctx, h.ctrl.ConversationID())
	if err != nil || len(rows.Turns) != 1 || rows.Turns[0].ProviderTurnID != "" {
		t.Fatalf("bind leaked: %+v %v", rows.Turns, err)
	}
	if len(fallbackDispositions(t, h.st)) != 0 {
		t.Fatal("failed bind leaked owner")
	}
	if _, err = db.Exec(`DROP TRIGGER reject_start_owner`); err != nil {
		t.Fatal(err)
	}
	rec, _, err := h.st.GetSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.st.BindPrimaryChatTurnToProvider(ctx, rows.Turns[0].ID, "provider-turn-1", "stale-generation", h.now()); err == nil {
		t.Fatal("stale controller claimed primary ownership")
	}
	if err = h.st.BindPrimaryChatTurnToProvider(ctx, rows.Turns[0].ID, "provider-turn-1", rec.Metadata.ControllerGeneration, h.now()); err != nil {
		t.Fatal(err)
	}
	if got := fallbackDispositions(t, h.st)[rows.Turns[0].ID]; got != "active" {
		t.Fatalf("bind owner=%q", got)
	}
}

func TestAdoptedStartOwnershipRollsBackWithArchive(t *testing.T) {
	acks := make(chan string, 8)
	provider := &receiptConversation{terminatingConversation: &terminatingConversation{fakeConversation: newFakeConversation()}, ack: func(id string) error { acks <- id; return nil }}
	h := newHarnessWithConversation(t, provider)
	ctx := context.Background()
	makeFallbackWorker(t, h.st)
	db := fallbackTestDB(t, h.st)
	if _, err := db.Exec(`CREATE TRIGGER reject_start_owner BEFORE INSERT ON turn_fallbacks BEGIN SELECT RAISE(ABORT, 'active ownership failed'); END`); err != nil {
		t.Fatal(err)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "adopted", ProviderEventID: "failed-start"})
	done := make(chan struct{})
	go func() { h.ctrl.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("failed persistent projection did not stop")
	}
	select {
	case id := <-acks:
		t.Fatalf("failed event acknowledged: %s", id)
	default:
	}
	rows, err := h.st.LoadConversationSnapshot(ctx, h.ctrl.ConversationID())
	if err != nil || len(rows.Turns) != 0 {
		t.Fatalf("failed adoption leaked: %+v %v", rows.Turns, err)
	}
	if len(fallbackDispositions(t, h.st)) != 0 {
		t.Fatal("failed projection leaked ownership")
	}
	if _, err = db.Exec(`DROP TRIGGER reject_start_owner`); err != nil {
		t.Fatal(err)
	}
	// Retry the exact archived event identity: rollback must have removed it.
	rec, _, err := h.st.GetSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	projected, err := h.st.ProjectProviderEvent(ctx, h.ctrl.ConversationID(), testSession, rec.Metadata.ControllerGeneration, "failed-start", string(ports.ChatEventTurnStarted), "{}", h.now(), func(txCtx context.Context) error {
		if err := h.st.AdoptProviderTurn(txCtx, h.ctrl.ConversationID(), testSession, rec.Metadata.ControllerGeneration, "retry-turn", "adopted", h.now()); err != nil {
			return err
		}
		return h.st.RecordActiveChatTurnFallback(txCtx, h.ctrl.ConversationID(), "adopted")
	})
	if err != nil || !projected || fallbackDispositions(t, h.st)["retry-turn"] != "active" {
		t.Fatalf("retry=%v err=%v", projected, err)
	}
}

func TestLaterAdoptedPrimaryDoesNotInheritPreviousSuppression(t *testing.T) {
	acks := make(chan string, 16)
	provider := &receiptConversation{terminatingConversation: &terminatingConversation{fakeConversation: newFakeConversation()}, ack: func(id string) error { acks <- id; return nil }}
	h := newHarnessWithConversation(t, provider)
	ctx := context.Background()
	makeFallbackWorker(t, h.st)
	first := ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "first", ProviderEventID: "first-start"}
	provider.emit(first)
	awaitFallbackReceipt(t, acks, "first-start")
	reports := reportsvc.New(reportsvc.Deps{Store: h.st, Now: h.now})
	if _, err := reports.Create(ctx, reportsvc.CreateInput{SessionID: testSession, State: domain.ReportDone, Note: "first result"}); err != nil {
		t.Fatal(err)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "first", ProviderEventID: "first-end", TurnState: domain.TurnStateCompleted})
	awaitFallbackReceipt(t, acks, "first-end")
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "later", ProviderEventID: "later-start"})
	awaitFallbackReceipt(t, acks, "later-start")
	provider.emit(first) // Old receipt replay cannot steal the later primary.
	awaitFallbackReceipt(t, acks, "first-start")
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: "later", ProviderEventID: "later-answer", ProviderItemID: "later-answer", Text: "later result"},
		ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "later", ProviderEventID: "later-end", TurnState: domain.TurnStateCompleted})
	awaitFallbackReceipt(t, acks, "later-end")
	pending, err := h.st.ListPendingTurnFallbacks(ctx)
	if err != nil || len(pending) != 1 || pending[0].Excerpt != "later result" {
		t.Fatalf("later result suppressed: %+v %v", pending, err)
	}
}
