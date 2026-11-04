package turnnotify

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/lifecycle"
	"tallpine.eu/tallpine/backend/internal/ports"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

type durableHarness struct {
	t       *testing.T
	dir     string
	st      *sqlite.Store
	rec     domain.SessionRecord
	now     time.Time
	o       *Observer
	reports *reportsvc.Service
	turn    string
}

func newDurableHarness(t *testing.T, mode domain.SessionMode) *durableHarness {
	t.Helper()
	h := &durableHarness{t: t, dir: t.TempDir(), now: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC), turn: "turn-1"}
	var err error
	h.st, err = sqlitetest.Open(h.dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.o.Close(); _ = h.st.Close() })
	ctx := context.Background()
	if err = h.st.UpsertProject(ctx, domain.ProjectRecord{ID: "fallback", Path: t.TempDir(), RegisteredAt: h.now}); err != nil {
		t.Fatal(err)
	}
	h.rec, err = h.st.CreateSession(ctx, domain.SessionRecord{ProjectID: "fallback", Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: mode, CreatedAt: h.now, UpdatedAt: h.now, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: h.now}, Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch", AgentSessionID: "native", AgentSessionIDLaunchID: "launch", ControllerGeneration: "g1"}})
	if err != nil {
		t.Fatal(err)
	}
	if mode == domain.SessionModeChat {
		if _, err = h.st.CreateConversation(ctx, "conv", domain.ConversationScopeSession, h.rec.ProjectID, h.rec.ID, h.now); err != nil {
			t.Fatal(err)
		}
	}
	h.bind()
	h.start("turn-1")
	return h
}
func (h *durableHarness) bind() {
	h.reports = reportsvc.New(reportsvc.Deps{Store: h.st, Now: func() time.Time { return h.now }})
	h.o = New(Deps{Next: lifecycle.New(h.st, nil), Sessions: h.st, Fallbacks: h.st, Reports: h.reports, Now: func() time.Time { return h.now }})
}
func (h *durableHarness) restart() {
	h.t.Helper()
	h.o.Close()
	if err := h.st.Close(); err != nil {
		h.t.Fatal(err)
	}
	var err error
	h.st, err = sqlite.OpenPreMigrated(h.dir)
	if err != nil {
		h.t.Fatal(err)
	}
	h.bind()
}
func (h *durableHarness) signal(event, text string) {
	h.t.Helper()
	state := domain.ActivityActive
	if event == "stop" {
		state = domain.ActivityIdle
	}
	err := h.o.ApplyActivitySignal(context.Background(), h.rec.ID, ports.ActivitySignal{Valid: true, State: state, Event: event, Timestamp: h.now, LaunchID: "launch", AgentSessionID: "native", ProviderTurnID: h.turn, LatestUserPrompt: text, FinalAssistantMessage: text})
	if err != nil {
		h.t.Fatal(err)
	}
}
func (h *durableHarness) start(turn string) {
	h.t.Helper()
	h.turn = turn
	if h.rec.Mode == domain.SessionModeTUI {
		h.signal("user-prompt-submit", "request "+turn)
		return
	}
	ctx := context.Background()
	rec, _, err := h.st.GetSession(ctx, h.rec.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err := h.st.AppendUserMessage(ctx, "conv", h.rec.ID, rec.Metadata.ControllerGeneration, domain.ConversationMessage{ID: "message-" + turn, Role: domain.MessageRoleUser, Origin: domain.MessageOriginHuman, Text: "request " + turn, ClientMessageID: turn}, turn, h.now); err != nil {
		h.t.Fatal(err)
	}
	if err := h.st.BindPrimaryChatTurnToProvider(ctx, turn, "provider-"+turn, rec.Metadata.ControllerGeneration, h.now); err != nil {
		h.t.Fatal(err)
	}
}
func (h *durableHarness) complete(text string) {
	h.t.Helper()
	if h.rec.Mode == domain.SessionModeTUI {
		h.signal("stop", text)
		return
	}
	rec, _, err := h.st.GetSession(context.Background(), h.rec.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	_, err = h.st.ProjectProviderEvent(context.Background(), "conv", h.rec.ID, rec.Metadata.ControllerGeneration, "complete-"+h.turn, "turn.completed", "{}", h.now, func(ctx context.Context) error {
		if err := h.st.SettleTurn(ctx, "conv", "provider-"+h.turn, domain.TurnStateCompleted, "", h.now); err != nil {
			return err
		}
		return h.st.RecordChatTurnFallback(ctx, "conv", "provider-"+h.turn, text, h.now)
	})
	if err != nil {
		h.t.Fatal(err)
	}
}
func (h *durableHarness) report(state domain.ReportState) {
	h.t.Helper()
	_, err := h.reports.Create(context.Background(), reportsvc.CreateInput{SessionID: h.rec.ID, State: state, Note: "explicit report"})
	if err != nil {
		h.t.Fatal(err)
	}
}
func (h *durableHarness) pending() []domain.TurnFallback {
	h.t.Helper()
	rows, err := h.st.ListPendingTurnFallbacks(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	return rows
}
func (h *durableHarness) autoReports() []domain.ReportRecord {
	h.t.Helper()
	rows, err := h.st.ListReportsByProject(context.Background(), h.rec.ProjectID)
	if err != nil {
		h.t.Fatal(err)
	}
	var result []domain.ReportRecord
	for _, r := range rows {
		if strings.HasPrefix(r.ID, "auto-turn-ended:") {
			result = append(result, r)
		}
	}
	return result
}
func (h *durableHarness) execSQL(statement string) {
	h.t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(h.dir, "ao.db"))
	if err != nil {
		h.t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(statement); err != nil {
		h.t.Fatal(err)
	}
}

func TestPendingFinalSurvivesObserverRecreation(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.SessionModeChat, domain.SessionModeTUI} {
		t.Run(string(mode), func(t *testing.T) {
			h := newDurableHarness(t, mode)
			h.complete(strings.Repeat("界", 600))
			delay := DefaultIdleDelay
			if mode == domain.SessionModeChat {
				delay = domain.ChatTurnFallbackDelay()
			}
			due := h.now.Add(delay)
			h.now = h.now.Add(time.Second)
			h.restart()
			p := h.pending()
			if len(p) != 1 || !p[0].DueAt.Equal(due) || len([]rune(p[0].Excerpt)) != 500 {
				t.Fatalf("pending=%+v", p)
			}
			h.o.deliverPending(context.Background())
			if len(h.autoReports()) != 0 {
				t.Fatal("delivered before original deadline")
			}
			h.now = due
			h.o.deliverPending(context.Background())
			h.restart()
			h.o.deliverPending(context.Background())
			if rows := h.autoReports(); len(rows) != 1 {
				t.Fatalf("reports=%d, want 1", len(rows))
			}
			if len(h.pending()) != 0 {
				t.Fatal("emitted completion still pending")
			}
		})
	}
}
func TestFallbackSuppressionSurvivesRestart(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.SessionModeChat, domain.SessionModeTUI} {
		for _, state := range []domain.ReportState{domain.ReportDone, domain.ReportNeedsInput, domain.ReportStuck, domain.ReportCheckpoint} {
			for _, before := range []bool{true, false} {
				name := string(mode) + "/" + string(state)
				if before {
					name += "/before"
				} else {
					name += "/after"
				}
				t.Run(name, func(t *testing.T) {
					h := newDurableHarness(t, mode)
					if before {
						h.report(state)
						h.restart()
					}
					h.complete("final result")
					if !before {
						h.report(state)
					}
					h.restart()
					h.now = h.now.Add(time.Minute)
					h.o.deliverPending(context.Background())
					want := 0
					if state == domain.ReportCheckpoint {
						want = 1
					}
					if got := len(h.autoReports()); got != want {
						t.Fatalf("reports=%d want=%d", got, want)
					}
				})
			}
		}
	}
}
func TestFallbackReportWriteFailureRetriesAtomically(t *testing.T) {
	h := newDurableHarness(t, domain.SessionModeChat)
	h.complete("final")
	h.now = h.now.Add(time.Minute)
	h.execSQL("CREATE TRIGGER reject_fallback BEFORE INSERT ON reports BEGIN SELECT RAISE(ABORT, 'injected report failure'); END")
	h.o.deliverPending(context.Background())
	if len(h.pending()) != 1 {
		t.Fatal("failed report consumed intent")
	}
	h.restart()
	h.execSQL("DROP TRIGGER reject_fallback")
	h.o.deliverPending(context.Background())
	h.restart()
	h.o.deliverPending(context.Background())
	if len(h.autoReports()) != 1 {
		t.Fatal("retry did not create exactly one report")
	}
}
func TestFallbackCompletionTransactionRollsBack(t *testing.T) {
	h := newDurableHarness(t, domain.SessionModeChat)
	ctx := context.Background()
	injected := errors.New("crash before commit")
	_, err := h.st.ProjectProviderEvent(ctx, "conv", h.rec.ID, "g1", "completion", "turn.completed", "{}", h.now, func(tx context.Context) error {
		if err := h.st.SettleTurn(tx, "conv", "provider-turn-1", domain.TurnStateCompleted, "", h.now); err != nil {
			return err
		}
		if err := h.st.RecordChatTurnFallback(tx, "conv", "provider-turn-1", "final", h.now); err != nil {
			return err
		}
		return injected
	})
	if !errors.Is(err, injected) {
		t.Fatal(err)
	}
	h.restart()
	if len(h.pending()) != 0 {
		t.Fatal("uncommitted intent survived")
	}
	h.complete("final")
	h.restart()
	if len(h.pending()) != 1 {
		t.Fatal("committed completion lost its intent")
	}
}
func TestNewWorkFencesRecoveredFallback(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.SessionModeChat, domain.SessionModeTUI} {
		t.Run(string(mode), func(t *testing.T) {
			h := newDurableHarness(t, mode)
			h.complete("old answer")
			h.now = h.now.Add(time.Second)
			h.start("turn-2")
			h.restart()
			h.now = h.now.Add(time.Minute)
			h.o.deliverPending(context.Background())
			if len(h.autoReports()) != 0 {
				t.Fatal("old result passed newer work fence")
			}
			h.report(domain.ReportCheckpoint)
			h.complete("new final")
			h.restart()
			h.now = h.now.Add(time.Minute)
			h.o.deliverPending(context.Background())
			rows := h.autoReports()
			if len(rows) != 1 || !strings.Contains(rows[0].Message, "new final") {
				t.Fatalf("reports=%+v", rows)
			}
		})
	}
}
func TestChatReattachmentPreservesCommittedFallback(t *testing.T) {
	h := newDurableHarness(t, domain.SessionModeChat)
	h.complete("committed final")
	h.restart()
	if err := h.st.ClaimLiveChatControllerGeneration(context.Background(), h.rec.ID, "replacement-attachment", ""); err != nil {
		t.Fatal(err)
	}
	h.now = h.now.Add(time.Minute)
	h.o.deliverPending(context.Background())
	if len(h.autoReports()) != 1 {
		t.Fatal("reattachment dropped completion")
	}
}

func TestDuplicateTerminalCompletionKeepsOriginalIntent(t *testing.T) {
	h := newDurableHarness(t, domain.SessionModeTUI)
	h.complete("original final")
	first := h.pending()[0]
	h.now = h.now.Add(10 * time.Second)
	h.complete("duplicate must not replace text")
	h.restart()
	pending := h.pending()
	if len(pending) != 1 || pending[0].ID != first.ID || !pending[0].DueAt.Equal(first.DueAt) || pending[0].Excerpt != "original final" {
		t.Fatalf("duplicate changed intent: %+v", pending)
	}
}

func TestTerminalFallbackActivityCASAndIntentAreAtomic(t *testing.T) {
	h := newDurableHarness(t, domain.SessionModeTUI)
	h.execSQL("CREATE TRIGGER reject_completion BEFORE UPDATE OF completed_at ON turn_fallbacks BEGIN SELECT RAISE(ABORT, 'injected completion failure'); END")
	err := h.o.ApplyActivitySignal(context.Background(), h.rec.ID, ports.ActivitySignal{Valid: true, State: domain.ActivityIdle, Event: "stop", Timestamp: h.now, LaunchID: "launch", AgentSessionID: "native", ProviderTurnID: h.turn, FinalAssistantMessage: "final"})
	if err == nil {
		t.Fatal("failed intent write accepted completion")
	}
	h.restart()
	rec, _, err := h.st.GetSession(context.Background(), h.rec.ID)
	if err != nil || rec.Activity.State != domain.ActivityActive || len(h.pending()) != 0 {
		t.Fatalf("partial completion: %+v err=%v", rec.Activity, err)
	}
	h.execSQL("DROP TRIGGER reject_completion")
	h.complete("final")
	if len(h.pending()) != 1 {
		t.Fatal("retry lost completion")
	}
}

func TestRetiredTerminalOwnerAndPaneWorkFenceFallback(t *testing.T) {
	for _, scenario := range []string{"launch", "thread", "pane-work", "terminated"} {
		t.Run(scenario, func(t *testing.T) {
			h := newDurableHarness(t, domain.SessionModeTUI)
			h.complete("old final")
			ctx := context.Background()
			rec, _, err := h.st.GetSession(ctx, h.rec.ID)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "launch":
				rec.Metadata.RuntimeLaunchID = "new-launch"
			case "thread":
				rec.Metadata.AgentSessionID = "new-thread"
			case "terminated":
				rec.IsTerminated = true
			case "pane-work":
				if _, err = h.st.RecordSessionLatestUserPrompt(ctx, h.rec.ID, "new work", rec.UpdatedAt.Add(time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			if scenario != "pane-work" {
				if err = h.st.UpdateSession(ctx, rec); err != nil {
					t.Fatal(err)
				}
			}
			h.restart()
			h.now = h.now.Add(time.Minute)
			h.o.deliverPending(ctx)
			if len(h.autoReports()) != 0 {
				t.Fatal("stale completion emitted")
			}
		})
	}
}

func TestIntentionalRetirementDoesNotInventExitOrEraseCommittedFinal(t *testing.T) {
	h := newDurableHarness(t, domain.SessionModeTUI)
	h.complete("committed final")
	if err := h.o.ApplyActivitySignal(context.Background(), h.rec.ID, ports.ActivitySignal{Valid: true, State: domain.ActivityExited, Event: "session-end", Timestamp: h.now, LaunchID: "launch", AgentSessionID: "native", IntentionalStop: true}); err != nil {
		t.Fatal(err)
	}
	h.restart()
	h.now = h.now.Add(time.Minute)
	h.o.deliverPending(context.Background())
	reports, err := h.st.ListReportsByProject(context.Background(), h.rec.ProjectID)
	if err != nil || len(reports) != 1 || !strings.HasPrefix(reports[0].Message, "[auto] turn_ended") {
		t.Fatalf("reports=%+v err=%v", reports, err)
	}
}

func TestAmbiguousClaudeQueueDoesNotCreateGuessedDurableIdentity(t *testing.T) {
	h := newDurableHarness(t, domain.SessionModeTUI)
	ctx := context.Background()
	rec, _, err := h.st.GetSession(ctx, h.rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	rec.Harness = domain.HarnessClaudeCode
	if err = h.st.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	for _, submission := range []string{"submission-1", "submission-2"} {
		if err = h.o.ApplyActivitySignal(ctx, h.rec.ID, ports.ActivitySignal{Valid: true, State: domain.ActivityActive, Event: "user-prompt-submit", Timestamp: h.now, LaunchID: "launch", AgentSessionID: "native", ProviderTurnID: "reused-anchor", SubmissionID: submission, LatestUserPrompt: submission}); err != nil {
			t.Fatal(err)
		}
	}
	h.report(domain.ReportDone)
	for _, text := range []string{"first answer", "later final"} {
		if err = h.o.ApplyActivitySignal(ctx, h.rec.ID, ports.ActivitySignal{Valid: true, State: domain.ActivityIdle, Event: "stop", Timestamp: h.now, LaunchID: "launch", AgentSessionID: "native", ProviderTurnID: "reused-anchor", FinalAssistantMessage: text}); err != nil {
			t.Fatal(err)
		}
	}
	// This path is explicitly unsupported for durable recovery. Reused anchors
	// must not become one ledger row which overwrites or suppresses a later final.
	if len(h.pending()) != 0 {
		t.Fatal("ambiguous completion manufactured a durable identity")
	}
}

func TestNewChatOwnerFencesCommittedFallback(t *testing.T) {
	h := newDurableHarness(t, domain.SessionModeChat)
	h.complete("old owner final")
	if err := h.st.ClaimChatControllerGeneration(context.Background(), h.rec.ID, "new-provider"); err != nil {
		t.Fatal(err)
	}
	h.restart()
	h.now = h.now.Add(time.Minute)
	h.o.deliverPending(context.Background())
	if len(h.autoReports()) != 0 {
		t.Fatal("old generation survived genuine replacement")
	}
}

func (h *durableHarness) exit() {
	h.t.Helper()
	rec, _, err := h.st.GetSession(context.Background(), h.rec.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	signal := ports.ActivitySignal{Valid: true, State: domain.ActivityExited, Event: "session-end", Timestamp: h.now}
	if rec.Mode == domain.SessionModeChat {
		signal.ControllerGeneration = rec.Metadata.ControllerGeneration
	} else {
		signal.LaunchID, signal.AgentSessionID = rec.Metadata.RuntimeLaunchID, rec.Metadata.AgentSessionID
	}
	if err := h.o.ApplyActivitySignal(context.Background(), h.rec.ID, signal); err != nil {
		h.t.Fatal(err)
	}
}

func TestDurableCompletionAndExitReportOnce(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.SessionModeChat, domain.SessionModeTUI} {
		for _, order := range []string{"completion-first", "exit-first"} {
			for _, restart := range []bool{false, true} {
				name := string(mode) + "/" + order
				if restart {
					name += "/restart"
				}
				t.Run(name, func(t *testing.T) {
					h := newDurableHarness(t, mode)
					h.complete("the committed final result")
					intent := h.pending()[0]
					if order == "completion-first" {
						h.now = intent.DueAt
						h.o.deliverPending(context.Background())
						if restart {
							h.restart()
						}
						h.exit()
					} else {
						h.exit()
						if restart {
							h.restart()
						}
						h.now = intent.DueAt
						h.o.deliverPending(context.Background())
					}
					h.restart()
					h.exit() // a repeated end-of-stream after restart is also harmless
					h.o.deliverPending(context.Background())
					rows, err := h.st.ListReportsByProject(context.Background(), h.rec.ProjectID)
					if err != nil || len(rows) != 1 || rows[0].ID != intent.ID || !strings.Contains(rows[0].Message, "the committed final result") {
						t.Fatalf("completion/exit reports=%+v err=%v", rows, err)
					}
					if !rows[0].AvailableAt.After(rows[0].CreatedAt) {
						t.Fatalf("durable turn_ended is due at once (%v); it must wait the quiet window (B192)", rows[0].AvailableAt)
					}
				})
			}
		}
	}
}

func TestOldDurableCompletionDoesNotHideLaterExit(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.SessionModeChat, domain.SessionModeTUI} {
		for _, next := range []string{"turn", "generation", "pane-work"} {
			if mode == domain.SessionModeChat && next == "pane-work" {
				continue
			}
			t.Run(string(mode)+"/"+next, func(t *testing.T) {
				h := newDurableHarness(t, mode)
				h.complete("old final")
				h.now = h.now.Add(time.Minute)
				h.o.deliverPending(context.Background())
				h.now = h.now.Add(time.Second)
				ctx := context.Background()
				switch next {
				case "turn":
					h.start("new-turn")
				case "pane-work":
					rec, _, err := h.st.GetSession(ctx, h.rec.ID)
					if err != nil {
						t.Fatal(err)
					}
					if applied, err := h.st.RecordSessionLatestUserPrompt(ctx, h.rec.ID, "new work", rec.UpdatedAt.Add(time.Second)); err != nil || !applied {
						t.Fatalf("new pane work: applied=%v err=%v", applied, err)
					}
				case "generation":
					rec, _, err := h.st.GetSession(ctx, h.rec.ID)
					if err != nil {
						t.Fatal(err)
					}
					rec.Metadata.ControllerGeneration, rec.Metadata.RuntimeLaunchID = "new-controller", "new-launch"
					if err := h.st.UpdateSession(ctx, rec); err != nil {
						t.Fatal(err)
					}
				}
				h.restart()
				h.exit()
				rows, err := h.st.ListReportsByProject(ctx, h.rec.ProjectID)
				if err != nil || len(rows) != 2 {
					t.Fatalf("old completion hid later exit: reports=%+v err=%v", rows, err)
				}
			})
		}
	}
}

func TestDurableExitRetainsRetryableCompletion(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.SessionModeChat, domain.SessionModeTUI} {
		t.Run(string(mode), func(t *testing.T) {
			h := newDurableHarness(t, mode)
			h.complete("retry this final")
			intent := h.pending()[0]
			h.execSQL("CREATE TRIGGER reject_fallback BEFORE INSERT ON reports BEGIN SELECT RAISE(ABORT, 'injected report failure'); END")
			h.exit()
			if pending := h.pending(); len(pending) != 1 || !pending[0].DueAt.Equal(intent.DueAt) {
				t.Fatalf("exit changed pending deadline: %+v", pending)
			}
			h.now = intent.DueAt
			h.o.deliverPending(context.Background())
			if len(h.pending()) != 1 {
				t.Fatal("failed write consumed completion")
			}
			h.restart()
			h.execSQL("DROP TRIGGER reject_fallback")
			h.o.deliverPending(context.Background())
			h.restart()
			h.exit()
			rows, err := h.st.ListReportsByProject(context.Background(), h.rec.ProjectID)
			if err != nil || len(rows) != 1 || rows[0].ID != intent.ID {
				t.Fatalf("retry reports=%+v err=%v", rows, err)
			}
		})
	}
}

func TestDurableHandledCompletionSuppressesExitAfterReattachment(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.SessionModeChat, domain.SessionModeTUI} {
		for _, explicit := range []bool{false, true} {
			name := string(mode) + "/emitted"
			if explicit {
				name = string(mode) + "/explicit"
			}
			t.Run(name, func(t *testing.T) {
				h := newDurableHarness(t, mode)
				h.complete("final")
				if explicit {
					h.report(domain.ReportDone)
				} else {
					h.now = h.now.Add(time.Minute)
					h.o.deliverPending(context.Background())
				}
				h.restart()
				if mode == domain.SessionModeChat {
					if err := h.st.ClaimLiveChatControllerGeneration(context.Background(), h.rec.ID, "reattached", ""); err != nil {
						t.Fatal(err)
					}
				}
				h.exit()
				h.now = h.now.Add(time.Minute)
				h.o.deliverPending(context.Background())
				rows, err := h.st.ListReportsByProject(context.Background(), h.rec.ProjectID)
				if err != nil || len(rows) != 1 {
					t.Fatalf("handled result repeated after exit: %+v err=%v", rows, err)
				}
			})
		}
	}
}

func TestSuppressionWithoutExistingIntent(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.SessionModeChat, domain.SessionModeTUI} {
		for _, state := range []domain.ReportState{domain.ReportDone, domain.ReportNeedsInput, domain.ReportStuck, domain.ReportCheckpoint} {
			t.Run(string(mode)+"/"+string(state), func(t *testing.T) {
				h := newDurableHarness(t, mode)
				ctx := context.Background()
				h.restart()
				if mode == domain.SessionModeChat {
					// Simulate a pre-ledger turn; the exact live claim reconstructs it.
					h.execSQL("DELETE FROM turn_fallbacks")
					// Neither live attachment has completed this running turn yet.
					for _, generation := range []string{"reattach-1", "reattach-2"} {
						if err := h.st.ClaimLiveChatControllerGeneration(ctx, h.rec.ID, generation, h.turn); err != nil {
							t.Fatal(err)
						}
						h.restart()
					}
				} else {
					// An upgrade can retain accepted native checkpoint facts without a ledger.
					h.execSQL("DELETE FROM turn_fallbacks")
				}
				h.report(state)
				h.restart()
				h.complete("first final")
				h.restart()
				h.now = h.now.Add(time.Minute)
				h.o.deliverPending(ctx)
				want := 0
				if state == domain.ReportCheckpoint {
					want = 1
				}
				if rows := h.autoReports(); len(rows) != want {
					t.Fatalf("fallbacks=%+v want=%d", rows, want)
				}
				// The prior report may never suppress a genuinely later turn.
				h.start("turn-2")
				h.complete("later final")
				h.restart()
				h.now = h.now.Add(time.Minute)
				h.o.deliverPending(ctx)
				rows := h.autoReports()
				if len(rows) != want+1 {
					t.Fatalf("later fallbacks=%+v want=%d", rows, want+1)
				}
			})
		}
	}
}

func TestUncorrelatedTerminalReportDoesNotSuppressLaterIdentifiedFinal(t *testing.T) {
	h := newDurableHarness(t, domain.SessionModeTUI)
	// Neither a start ledger nor an accepted native checkpoint identifies the
	// work that produced this report. A later Stop cannot retroactively do so.
	h.execSQL("DELETE FROM turn_fallbacks")
	h.execSQL("UPDATE sessions SET conversation_checkpoint_state = 'legacy', conversation_checkpoint_turn_id = ''")
	h.report(domain.ReportDone)
	h.restart()
	h.turn = "unseen-later-turn"
	h.complete("later identified final")
	h.restart()
	h.now = h.now.Add(time.Minute)
	h.o.deliverPending(context.Background())
	h.restart()
	h.o.deliverPending(context.Background())
	if rows := h.autoReports(); len(rows) != 1 || !strings.Contains(rows[0].Message, "later identified final") {
		t.Fatalf("uncorrelated report hid/repeated a later final: %+v", rows)
	}
}

func TestLiveReclaimIntentAndGenerationCommitTogether(t *testing.T) {
	h := newDurableHarness(t, domain.SessionModeChat)
	// Simulate an accepted pre-ledger turn that reclaim must reconstruct.
	h.execSQL("DELETE FROM turn_fallbacks")
	h.execSQL("CREATE TRIGGER reject_reclaim BEFORE INSERT ON turn_fallbacks BEGIN SELECT RAISE(ABORT, 'injected reclaim failure'); END")
	if err := h.st.ClaimLiveChatControllerGeneration(context.Background(), h.rec.ID, "replacement", h.turn); err == nil {
		t.Fatal("claim ignored intent failure")
	}
	h.restart()
	rec, _, err := h.st.GetSession(context.Background(), h.rec.ID)
	if err != nil || rec.Metadata.ControllerGeneration != "g1" {
		t.Fatalf("partial generation claim: %+v %v", rec.Metadata, err)
	}
}

func TestUntrustedCheckpointCannotReconstructSuppression(t *testing.T) {
	for _, invalid := range []string{
		"conversation_checkpoint_generation = 'retired-launch'",
		"conversation_checkpoint_native_id = 'other-thread'",
		"conversation_checkpoint_unsettled = 1",
	} {
		t.Run(invalid, func(t *testing.T) {
			h := newDurableHarness(t, domain.SessionModeTUI)
			h.execSQL("DELETE FROM turn_fallbacks")
			h.execSQL("UPDATE sessions SET " + invalid)
			h.report(domain.ReportDone)
			h.restart()
			h.complete("unsuppressed final")
			h.restart()
			h.now = h.now.Add(time.Minute)
			h.o.deliverPending(context.Background())
			if rows := h.autoReports(); len(rows) != 1 {
				t.Fatalf("untrusted checkpoint suppressed final: %+v", rows)
			}
		})
	}
}

func TestUnidentifiedNewPromptRetiresPreviousResult(t *testing.T) {
	for _, disposition := range []string{"pending", "emitted", "suppressed"} {
		t.Run(disposition, func(t *testing.T) {
			h := newDurableHarness(t, domain.SessionModeTUI)
			h.complete("old final")
			if disposition == "emitted" {
				h.now = h.now.Add(time.Minute)
				h.o.deliverPending(context.Background())
			}
			if disposition == "suppressed" {
				h.report(domain.ReportDone)
			}
			h.now = h.now.Add(time.Second)
			h.turn = ""
			h.signal("user-prompt-submit", "new unidentified work")
			h.restart()
			h.now = h.now.Add(time.Minute)
			h.o.deliverPending(context.Background())
			wantOld := 0
			if disposition == "emitted" {
				wantOld = 1
			}
			if got := len(h.autoReports()); got != wantOld {
				t.Fatalf("stale fallback count=%d want=%d", got, wantOld)
			}
			if owns, err := h.st.HasCurrentCompletedTurnFallback(context.Background(), h.rec.ID); err != nil || owns {
				t.Fatalf("old result still owns notification=%v err=%v", owns, err)
			}
			h.exit()
			rows, err := h.st.ListReportsByProject(context.Background(), h.rec.ProjectID)
			if err != nil {
				t.Fatal(err)
			}
			exits := 0
			for _, row := range rows {
				if strings.HasPrefix(row.ID, "auto-exited:") {
					exits++
				}
			}
			if exits != 1 {
				t.Fatalf("new work exits=%d", exits)
			}
		})
	}
}

func TestNewPromptRetirementRollsBackWithActivity(t *testing.T) {
	h := newDurableHarness(t, domain.SessionModeTUI)
	h.complete("old final")
	h.execSQL("CREATE TRIGGER reject_retirement BEFORE UPDATE OF disposition ON turn_fallbacks WHEN NEW.disposition = 'superseded' BEGIN SELECT RAISE(ABORT, 'retirement failed'); END")
	h.now = h.now.Add(time.Second)
	signal := ports.ActivitySignal{Valid: true, State: domain.ActivityActive, Event: "user-prompt-submit", Timestamp: h.now, LaunchID: "launch", AgentSessionID: "native", LatestUserPrompt: "new work"}
	if err := h.o.ApplyActivitySignal(context.Background(), h.rec.ID, signal); err == nil {
		t.Fatal("retirement failure accepted prompt")
	}
	h.restart()
	rec, _, err := h.st.GetSession(context.Background(), h.rec.ID)
	if err != nil || rec.Activity.State != domain.ActivityIdle || rec.Metadata.LatestUserPrompt == "new work" || len(h.pending()) != 1 {
		t.Fatalf("partial retirement: %+v err=%v", rec, err)
	}
	h.execSQL("DROP TRIGGER reject_retirement")
	if err := h.o.ApplyActivitySignal(context.Background(), h.rec.ID, signal); err != nil {
		t.Fatal(err)
	}
	if len(h.pending()) != 0 {
		t.Fatal("retry failed to retire")
	}
}

func (h *durableHarness) handoff() string {
	h.t.Helper()
	ctx := context.Background()
	_, created, err := h.st.CreateSessionInterfaceTransition(ctx, domain.SessionInterfaceTransition{
		ID: "handoff", SessionID: h.rec.ID, SourceMode: domain.SessionModeTUI, TargetMode: domain.SessionModeChat,
		Policy: domain.SessionInterfaceTransitionDrain, HistoryPolicy: domain.SessionInterfaceTransitionHistoryStrict,
		Phase: domain.SessionInterfaceTransitionSourceStopped, NativeConversationID: "native", CreatedAt: h.now, UpdatedAt: h.now,
	})
	if err != nil || !created {
		h.t.Fatalf("create handoff: %v %v", created, err)
	}
	changed, err := h.st.CommitSessionControllerEpoch(ctx, h.rec.ID, domain.SessionModeTUI, domain.SessionModeChat, "native", h.now)
	if err != nil || !changed {
		h.t.Fatalf("commit epoch: %v %v", changed, err)
	}
	return h.handoffGeneration("handoff")
}
func (h *durableHarness) handoffGeneration(transition string) string {
	h.t.Helper()
	ctx := context.Background()
	rec, _, err := h.st.GetSession(ctx, h.rec.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	generation, err := h.st.TurnFallbackHandoffGeneration(ctx, rec.ID, rec.ControllerOwner(), transition)
	if err != nil || generation == "" {
		h.t.Fatalf("handoff generation=%q err=%v", generation, err)
	}
	return generation
}

func TestInterfaceHandoffPreservesFinalDisposition(t *testing.T) {
	for _, disposition := range []string{"pending", "emitted", "suppressed"} {
		for _, path := range []string{"claim", "rollback", "recovery", "live reconnect"} {
			t.Run(disposition+"/"+path, func(t *testing.T) {
				h := newDurableHarness(t, domain.SessionModeTUI)
				ctx := context.Background()
				h.complete("original final")
				original := h.pending()[0]
				if disposition == "suppressed" {
					h.report(domain.ReportDone)
				}
				if disposition == "emitted" {
					h.now = original.DueAt
					h.o.deliverPending(ctx)
				}
				generation := h.handoff()
				h.restart() // The committed empty-owner epoch must itself preserve the result.
				if disposition == "pending" {
					if p := h.pending(); len(p) != 1 || p[0] != original {
						t.Fatalf("epoch changed final: %+v", p)
					}
				}
				if got := h.handoffGeneration("handoff"); got != generation {
					t.Fatal("restart changed reserved claim")
				}
				if path == "rollback" || path == "recovery" {
					if err := h.st.ClaimChatControllerGeneration(ctx, h.rec.ID, generation); err != nil {
						t.Fatal(err)
					}
					changed, err := h.st.RestoreSessionControllerEpoch(ctx, h.rec.ID, domain.SessionModeChat, domain.SessionModeTUI, "native", h.now)
					if err != nil || !changed {
						t.Fatalf("restore: %v %v", changed, err)
					}
					if path == "recovery" {
						changed, err = h.st.AdvanceSessionInterfaceTransition(ctx, "handoff", domain.SessionInterfaceTransitionSourceStopped,
							domain.SessionInterfaceTransitionRecovery, "native", "DAEMON_RESTARTED", "", h.now)
						if err != nil || !changed {
							t.Fatalf("recovery: %v %v", changed, err)
						}
						h.restart()
						generation = h.handoffGeneration("")
					} else {
						generation = h.handoffGeneration("handoff")
					}
					rec, _, err := h.st.GetSession(ctx, h.rec.ID)
					if err != nil {
						t.Fatal(err)
					}
					rec.Metadata.RuntimeLaunchID = generation
					rec.Metadata.AgentSessionIDLaunchID = generation
					if err = h.st.UpdateSession(ctx, rec); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := h.st.ClaimChatControllerGeneration(ctx, h.rec.ID, generation); err != nil {
						t.Fatal(err)
					}
					if path == "live reconnect" {
						if err := h.st.ClaimLiveChatControllerGeneration(ctx, h.rec.ID, "live-reconnect", ""); err != nil {
							t.Fatal(err)
						}
					}
				}
				h.restart()
				if disposition == "pending" {
					if p := h.pending(); len(p) != 1 || p[0] != original {
						t.Fatalf("claim changed final: %+v", p)
					}
				}
				h.now = original.DueAt
				h.o.deliverPending(ctx)
				h.restart()
				h.o.deliverPending(ctx)
				want := 1
				if disposition == "suppressed" {
					want = 0
				}
				if got := len(h.autoReports()); got != want {
					t.Fatalf("reports=%d want=%d", got, want)
				}
				owns, err := h.st.HasCurrentCompletedTurnFallback(ctx, h.rec.ID)
				if err != nil || !owns {
					t.Fatalf("lost final disposition: %v %v", owns, err)
				}
			})
		}
	}
}

func TestInterfaceHandoffRejectsUnrelatedClaims(t *testing.T) {
	for _, scenario := range []string{"wrong target", "later replacement", "stale transition", "fresh epoch"} {
		t.Run(scenario, func(t *testing.T) {
			h := newDurableHarness(t, domain.SessionModeTUI)
			ctx := context.Background()
			h.complete("old final")
			generation := h.handoff()
			rec, _, err := h.st.GetSession(ctx, h.rec.ID)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "stale transition" {
				got, err := h.st.TurnFallbackHandoffGeneration(ctx, rec.ID, rec.ControllerOwner(), "wrong-saga")
				if err != nil || got != "" {
					t.Fatalf("stale saga authorized %q: %v", got, err)
				}
			}
			if scenario == "later replacement" {
				if err = h.st.ClaimChatControllerGeneration(ctx, h.rec.ID, generation); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "fresh epoch" {
				rec.Metadata.ProviderConversationID = "different-native"
				if err = h.st.UpdateSession(ctx, rec); err != nil {
					t.Fatal(err)
				}
			} else if err = h.st.ClaimChatControllerGeneration(ctx, h.rec.ID, "unrelated-generation"); err != nil {
				t.Fatal(err)
			}
			h.restart()
			if p := h.pending(); len(p) != 0 {
				t.Fatalf("unrelated claim adopted %+v", p)
			}
			owns, err := h.st.HasCurrentCompletedTurnFallback(ctx, h.rec.ID)
			if err != nil || owns {
				t.Fatalf("old final hides new exit: %v %v", owns, err)
			}
		})
	}
}

func TestInterfaceHandoffEpochRollbackAndNewWork(t *testing.T) {
	h := newDurableHarness(t, domain.SessionModeTUI)
	ctx := context.Background()
	h.complete("keep this final")
	original := h.pending()[0]
	h.execSQL(`CREATE TRIGGER reject_carry BEFORE UPDATE OF carry_transition_id ON turn_fallbacks BEGIN SELECT RAISE(ABORT, 'carry failed'); END`)
	_, _, err := h.st.CreateSessionInterfaceTransition(ctx, domain.SessionInterfaceTransition{ID: "handoff", SessionID: h.rec.ID,
		SourceMode: domain.SessionModeTUI, TargetMode: domain.SessionModeChat, Policy: domain.SessionInterfaceTransitionDrain,
		HistoryPolicy: domain.SessionInterfaceTransitionHistoryStrict, Phase: domain.SessionInterfaceTransitionSourceStopped,
		NativeConversationID: "native", CreatedAt: h.now, UpdatedAt: h.now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.st.CommitSessionControllerEpoch(ctx, h.rec.ID, domain.SessionModeTUI, domain.SessionModeChat, "native", h.now); err == nil {
		t.Fatal("carry failure did not roll back epoch")
	}
	h.restart()
	rec, _, err := h.st.GetSession(ctx, h.rec.ID)
	if err != nil || rec.Mode != domain.SessionModeTUI {
		t.Fatalf("epoch leaked: %+v %v", rec, err)
	}
	if p := h.pending(); len(p) != 1 || p[0] != original {
		t.Fatalf("rollback lost final: %+v", p)
	}
	h.execSQL(`DROP TRIGGER reject_carry`)
	if changed, err := h.st.CommitSessionControllerEpoch(ctx, h.rec.ID, domain.SessionModeTUI, domain.SessionModeChat, "native", h.now); err != nil || !changed {
		t.Fatalf("epoch retry: %v %v", changed, err)
	}
	generation := h.handoffGeneration("handoff")
	if err := h.st.ClaimChatControllerGeneration(ctx, h.rec.ID, generation); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.CreateConversation(ctx, "handoff-chat", domain.ConversationScopeSession, h.rec.ProjectID, h.rec.ID, h.now); err != nil {
		t.Fatal(err)
	}
	h.execSQL(`CREATE TRIGGER reject_intake BEFORE INSERT ON conversation_messages BEGIN SELECT RAISE(ABORT, 'intake failed'); END`)
	msg := domain.ConversationMessage{ID: "new-message", Role: domain.MessageRoleUser, Origin: domain.MessageOriginHuman, Text: "new work", ClientMessageID: "new"}
	if _, err := h.st.AppendUserMessage(ctx, "handoff-chat", h.rec.ID, generation, msg, "new-turn", h.now); err == nil {
		t.Fatal("intake failure ignored")
	}
	if p := h.pending(); len(p) != 1 || p[0] != original {
		t.Fatalf("failed intake retired final: %+v", p)
	}
	h.execSQL(`DROP TRIGGER reject_intake`)
	if _, err := h.st.AppendUserMessage(ctx, "handoff-chat", h.rec.ID, generation, msg, "new-turn", h.now); err != nil {
		t.Fatal(err)
	}
	h.restart()
	if p := h.pending(); len(p) != 0 {
		t.Fatalf("accepted Chat work kept old final: %+v", p)
	}
}

func TestRejectedPromptDoesNotRetireFinal(t *testing.T) {
	for _, scenario := range []string{"stale", "duplicate", "nested", "wrong launch"} {
		t.Run(scenario, func(t *testing.T) {
			h := newDurableHarness(t, domain.SessionModeTUI)
			ctx := context.Background()
			h.complete("original final")
			original := h.pending()[0]
			signal := ports.ActivitySignal{Valid: true, State: domain.ActivityActive, Event: "user-prompt-submit", Timestamp: h.now.Add(time.Second), LaunchID: "launch", AgentSessionID: "native", LatestUserPrompt: "new prompt"}
			switch scenario {
			case "stale":
				signal.Timestamp = h.now.Add(-time.Second)
			case "duplicate":
				signal.Timestamp = h.now
				signal.LatestUserPrompt = "request turn-1"
			case "nested":
				signal.SubagentID = "child"
			case "wrong launch":
				signal.LaunchID = "old-launch"
			}
			if err := h.o.ApplyActivitySignal(ctx, h.rec.ID, signal); err != nil {
				t.Fatal(err)
			}
			h.restart()
			if p := h.pending(); len(p) != 1 || p[0] != original {
				t.Fatalf("rejected prompt retired final: %+v", p)
			}
		})
	}
}

func TestPreparedHandoffPublicationPreservesFinalAtomically(t *testing.T) {
	for _, disposition := range []string{"pending", "emitted", "suppressed"} {
		for _, failure := range []string{"none", "history", "claim"} {
			t.Run(disposition+"/"+failure, func(t *testing.T) {
				h := newDurableHarness(t, domain.SessionModeTUI)
				ctx := context.Background()
				h.complete("original prepared final")
				original := h.pending()[0]
				if disposition == "suppressed" {
					h.report(domain.ReportDone)
				}
				if disposition == "emitted" {
					h.now = original.DueAt
					h.o.deliverPending(ctx)
				}
				generation := h.handoff()
				conversation, err := h.st.CreateConversation(ctx, "prepared-conversation", domain.ConversationScopeSession, h.rec.ProjectID, h.rec.ID, h.now)
				if err != nil {
					t.Fatal(err)
				}
				rec, _, err := h.st.GetSession(ctx, h.rec.ID)
				if err != nil {
					t.Fatal(err)
				}
				rec.Metadata.ControllerGeneration = generation
				branch := domain.ConversationBranch{ID: "prepared-branch", ConversationID: conversation.ID, SessionID: rec.ID,
					ProviderConversationID: "native", ParentBranchID: conversation.ActiveBranchID, ProviderScopeID: "prepared-branch", ProviderIDsScoped: true, CreatedAt: h.now}
				if failure == "claim" {
					h.execSQL(`CREATE TRIGGER reject_owner_claim BEFORE UPDATE OF owner_claimed ON turn_fallbacks WHEN NEW.owner_claimed=1 BEGIN SELECT RAISE(ABORT, 'claim failed'); END`)
				}
				prepare := func(txCtx context.Context) error {
					if err := h.st.AdoptProviderTurn(txCtx, conversation.ID, rec.ID, generation, "imported-turn", "imported-provider-turn", h.now); err != nil {
						return err
					}
					if err := h.st.SettleTurn(txCtx, conversation.ID, "imported-provider-turn", domain.TurnStateCompleted, "", h.now); err != nil {
						return err
					}
					if failure == "history" {
						return errors.New("history failed")
					}
					return nil
				}
				err = h.st.CommitChatSpawnPrepared(ctx, rec, branch, nil, prepare)
				if (err != nil) != (failure != "none") {
					t.Fatalf("publication error=%v", err)
				}
				h.restart()
				current, _, err := h.st.GetSession(ctx, rec.ID)
				if err != nil {
					t.Fatal(err)
				}
				snapshot, err := h.st.LoadConversationSnapshot(ctx, conversation.ID)
				if err != nil {
					t.Fatal(err)
				}
				if failure != "none" {
					if current.Metadata.ControllerGeneration != "" || len(snapshot.Turns) != 0 || snapshot.Conversation.ActiveBranchID != conversation.ActiveBranchID {
						t.Fatal("failed publication leaked ownership or history")
					}
					if got := h.handoffGeneration("handoff"); got != generation {
						t.Fatal("failed publication changed reservation")
					}
					if failure == "claim" {
						h.execSQL(`DROP TRIGGER reject_owner_claim`)
					}
				} else if current.Metadata.ControllerGeneration != generation || len(snapshot.Turns) != 1 {
					t.Fatal("publication did not commit together")
				}
				if disposition == "pending" {
					if p := h.pending(); len(p) != 1 || p[0] != original {
						t.Fatalf("publication changed final: %+v", p)
					}
				}
				h.now = original.DueAt
				h.o.deliverPending(ctx)
				h.restart()
				h.o.deliverPending(ctx)
				want := 1
				if disposition == "suppressed" {
					want = 0
				}
				if got := len(h.autoReports()); got != want {
					t.Fatalf("reports=%d want=%d", got, want)
				}
			})
		}
	}
}

func TestCarriedFinalSuppressionAndRetry(t *testing.T) {
	for _, state := range []domain.ReportState{domain.ReportDone, domain.ReportNeedsInput, domain.ReportStuck, domain.ReportCheckpoint} {
		t.Run(string(state), func(t *testing.T) {
			h := newDurableHarness(t, domain.SessionModeTUI)
			ctx := context.Background()
			h.complete("carried answer")
			original := h.pending()[0]
			generation := h.handoff()
			if err := h.st.ClaimChatControllerGeneration(ctx, h.rec.ID, generation); err != nil {
				t.Fatal(err)
			}
			h.report(state)
			h.restart()
			h.now = original.DueAt
			if state == domain.ReportCheckpoint {
				h.execSQL(`CREATE TRIGGER reject_report BEFORE INSERT ON reports BEGIN SELECT RAISE(ABORT, 'report failed'); END`)
				h.o.deliverPending(ctx)
				h.restart()
				if p := h.pending(); len(p) != 1 || p[0] != original {
					t.Fatalf("failed report changed carried final: %+v", p)
				}
				h.execSQL(`DROP TRIGGER reject_report`)
			}
			h.o.deliverPending(ctx)
			h.restart()
			h.o.deliverPending(ctx)
			want := 0
			if state == domain.ReportCheckpoint {
				want = 1
			}
			if got := len(h.autoReports()); got != want {
				t.Fatalf("reports=%d want=%d", got, want)
			}
		})
	}
}

// Chat turns without a committed completion row (interrupted, auth-wait,
// unrecorded) must keep the legacy idle timer instead of going silent.
func TestChatDurableSignalRequiresCompletedRow(t *testing.T) {
	h := newDurableHarness(t, domain.SessionModeChat)
	ctx := context.Background()
	if h.o.durableSignal(ctx, h.rec.ID, ports.ActivitySignal{}) {
		t.Fatal("turn without a completed durable row disabled the legacy timer")
	}
	h.complete("final")
	if !h.o.durableSignal(ctx, h.rec.ID, ports.ActivitySignal{}) {
		t.Fatal("completed durable row did not own the notice")
	}
}

// A Chat turn without a durable row (interrupted/unrecorded) gets the legacy
// idle notice after the delay. The next turn's durable completion must add
// exactly one notice of its own, and neither may be repeated afterwards.
func TestChatLegacyIdleNoticeThenDurableTurnNotDuplicated(t *testing.T) {
	h := newDurableHarness(t, domain.SessionModeChat)
	var timers []func()
	h.o.AfterFunc = func(d time.Duration, f func()) stopper {
		tm := &fakeTimer{}
		timers = append(timers, func() {
			if !tm.stopped {
				f()
			}
		})
		return tm
	}
	chatSignal := func(event, text string) {
		t.Helper()
		err := h.o.ApplyActivitySignal(context.Background(), h.rec.ID, ports.ActivitySignal{Valid: true, State: domain.ActivityIdle, Event: event, Timestamp: h.now, ControllerGeneration: "g1", FinalAssistantMessage: text})
		if err != nil {
			t.Fatal(err)
		}
	}
	chatSignal("chat.turn.started", "")
	chatSignal("chat.turn.completed", "interrupted partial")
	if len(timers) != 1 {
		t.Fatalf("legacy timer armed %d times, want 1", len(timers))
	}
	h.now = h.now.Add(DefaultIdleDelay)
	timers[0]()
	if got := len(noticeReports(h)); got != 1 {
		t.Fatalf("legacy notice reports=%d, want 1", got)
	}
	// Redelivery of the same completion, and a delivery sweep, add nothing.
	chatSignal("chat.turn.completed", "interrupted partial")
	h.o.deliverPending(context.Background())
	if got := len(noticeReports(h)); got != 1 {
		t.Fatalf("duplicate legacy notice: %d", got)
	}
	h.start("turn-2")
	chatSignal("chat.turn.started", "")
	h.complete("final")
	chatSignal("chat.turn.completed", "final")
	if len(timers) != 1 {
		t.Fatalf("durable turn armed the legacy timer (%d timers)", len(timers))
	}
	h.now = h.now.Add(domain.TurnFallbackDelay)
	h.o.deliverPending(context.Background())
	h.o.deliverPending(context.Background())
	if got := len(noticeReports(h)); got != 2 {
		t.Fatalf("turn-ended notices=%d, want 2 (one per turn): %+v", got, noticeReports(h))
	}
}

func noticeReports(h *durableHarness) []domain.ReportRecord {
	h.t.Helper()
	rows, err := h.st.ListReportsByProject(context.Background(), h.rec.ProjectID)
	if err != nil {
		h.t.Fatal(err)
	}
	var result []domain.ReportRecord
	for _, r := range rows {
		if strings.Contains(r.Message, "turn_ended") {
			result = append(result, r)
		}
	}
	return result
}

// B317: a turn stopped by the usage limit reaches the orchestrator as a pause,
// not as an idle worker quoting its earlier message.
func TestUsageLimitedFallbackReportsPauseNotLastMessage(t *testing.T) {
	h := newDurableHarness(t, domain.SessionModeChat)
	until := h.now.Add(23 * time.Minute)
	h.complete(domain.UsageLimitFallbackExcerpt(until, true))
	h.now = h.now.Add(time.Minute)
	h.o.deliverPending(context.Background())
	reports := h.autoReports()
	if len(reports) != 1 {
		t.Fatalf("reports = %d, want 1", len(reports))
	}
	msg := reports[0].Message
	if !strings.Contains(msg, "usage_limited") || !strings.Contains(msg, "paused until "+until.UTC().Format(time.RFC3339)) ||
		strings.Contains(msg, "went idle") || strings.Contains(msg, "Last message") {
		t.Fatalf("message = %q", msg)
	}
}
