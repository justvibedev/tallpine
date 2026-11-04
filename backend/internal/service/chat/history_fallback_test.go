package chat_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/lifecycle"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// Importing an idle worker's native history must not start a fresh delivery
// clock. The interface epoch carries the original result, independently of the
// Tallpine turn rows created while projecting the historical transcript.
func TestWorkerHistoryImportPreservesHandoffFinal(t *testing.T) {
	for _, disposition := range []string{"pending", "emitted", "suppressed"} {
		t.Run(disposition, func(t *testing.T) {
			ctx := context.Background()
			st := openStore(t)
			project, _, err := st.GetProject(ctx, string(testProject))
			if err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
			rec, _, err := st.GetSession(ctx, testSession)
			if err != nil {
				t.Fatal(err)
			}
			rec.UpdatedAt = now
			rec.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}
			rec.Kind = domain.KindWorker
			rec.Mode = domain.SessionModeTUI
			if changed, err := st.CommitSessionControllerEpoch(ctx, testSession, domain.SessionModeChat, domain.SessionModeTUI, "thread-1", now); err != nil || !changed {
				t.Fatalf("seed terminal epoch: %v %v", changed, err)
			}
			rec.Metadata.RuntimeLaunchID = "terminal-launch"
			rec.Metadata.AgentSessionID = "thread-1"
			rec.Metadata.AgentSessionIDLaunchID = "terminal-launch"
			if err = st.UpdateSession(ctx, rec); err != nil {
				t.Fatal(err)
			}
			lcm := lifecycle.New(st, nil)
			for _, event := range []string{"user-prompt-submit", "stop"} {
				state := domain.ActivityActive
				if event == "stop" {
					state = domain.ActivityIdle
				}
				if err = lcm.ApplyActivitySignal(ctx, testSession, ports.ActivitySignal{Valid: true, Event: event, State: state, Timestamp: now,
					LaunchID: "terminal-launch", AgentSessionID: "thread-1", ProviderTurnID: "native-turn", LatestUserPrompt: "original request", FinalAssistantMessage: "original answer", LatestAssistantUpdate: "original answer"}); err != nil {
					t.Fatal(err)
				}
			}
			pending, err := st.ListPendingTurnFallbacks(ctx)
			if err != nil || len(pending) != 1 {
				t.Fatalf("seed final: %+v %v", pending, err)
			}
			original := pending[0]
			reports := reportsvc.New(reportsvc.Deps{Store: st, Now: func() time.Time { return original.DueAt }})
			switch disposition {
			case "suppressed":
				if _, err = reports.Create(ctx, reportsvc.CreateInput{SessionID: testSession, State: domain.ReportDone, Note: "already reported"}); err != nil {
					t.Fatal(err)
				}
			case "emitted":
				if _, err = reports.Create(ctx, reportsvc.CreateInput{ID: original.ID, TurnFallbackID: original.ID, SessionID: testSession, Synthesized: true, Message: "original final"}); err != nil {
					t.Fatal(err)
				}
			}
			_, created, err := st.CreateSessionInterfaceTransition(ctx, domain.SessionInterfaceTransition{ID: "history-switch", SessionID: testSession, SourceMode: domain.SessionModeTUI, TargetMode: domain.SessionModeChat,
				Policy: domain.SessionInterfaceTransitionDrain, HistoryPolicy: domain.SessionInterfaceTransitionHistoryStrict, Phase: domain.SessionInterfaceTransitionSourceStopped, NativeConversationID: "thread-1", CreatedAt: now, UpdatedAt: now})
			if err != nil || !created {
				t.Fatalf("transition: %v %v", created, err)
			}
			if changed, err := st.CommitSessionControllerEpoch(ctx, testSession, domain.SessionModeTUI, domain.SessionModeChat, "thread-1", now); err != nil || !changed {
				t.Fatalf("epoch: %v %v", changed, err)
			}
			rec, _, err = st.GetSession(ctx, testSession)
			if err != nil {
				t.Fatal(err)
			}
			generation, err := st.TurnFallbackHandoffGeneration(ctx, testSession, rec.ControllerOwner(), "history-switch")
			if err != nil || generation == "" {
				t.Fatalf("generation: %q %v", generation, err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				provider := &nativeHistoryConversation{fakeConversation: newFakeConversation(), events: []ports.ChatEvent{
					{Kind: ports.ChatEventTurnStarted, ProviderEventID: "history-start", ProviderTurnID: "native-turn"},
					{Kind: ports.ChatEventUserMessageCompleted, ProviderEventID: "history-user", ProviderTurnID: "native-turn", ProviderItemID: "history-user", Text: "original request"},
					{Kind: ports.ChatEventMessageCompleted, ProviderEventID: "history-answer", ProviderTurnID: "native-turn", ProviderItemID: "history-answer", Text: "original answer"},
					{Kind: ports.ChatEventTurnCompleted, ProviderEventID: "history-end", ProviderTurnID: "native-turn", TurnState: domain.TurnStateCompleted},
				}}
				svc := chatsvc.New(chatsvc.Options{Store: st, Sessions: st, Reader: fullSnapshotReader(st), Drivers: fakeRegistry{driver: fakeDriver{conv: provider}}, NewID: uuid.NewString, Now: func() time.Time { return now.Add(10 * time.Minute) }})
				t.Cleanup(func() { svc.StopAll(ctx) })
				ctrl, err := svc.Start(ctx, chatsvc.StartConfig{SessionID: testSession, ProjectID: testProject, Kind: domain.KindWorker, Harness: domain.HarnessCodex, WorkspacePath: project.Path,
					ProviderConversationID: "thread-1", ControllerGeneration: generation, HistoryMode: ports.ChatHistoryRequired})
				if err != nil {
					t.Fatal(err)
				}
				rows, err := st.LoadConversationSnapshot(ctx, ctrl.ConversationID())
				if err != nil || len(rows.Turns) != 1 {
					t.Fatalf("history: %+v %v", rows.Turns, err)
				}
				pending, err = st.ListPendingTurnFallbacks(ctx)
				want := 0
				if disposition == "pending" {
					want = 1
				}
				if err != nil || len(pending) != want {
					t.Fatalf("import manufactured final: %+v %v", pending, err)
				}
				if want == 1 && pending[0] != original {
					t.Fatalf("import changed original final: %+v", pending)
				}
				if rows := fallbackDispositions(t, st); len(rows) != 1 {
					t.Fatalf("historical start created active ownership: %v", rows)
				}
				if attempt == 1 {
					// The same controller must still create a new intent for genuinely live work.
					turn, err := ctrl.Send(ctx, ports.ChatUserMessage{Text: "new live request", ClientMessageID: "live", Origin: domain.MessageOriginHuman})
					if err != nil {
						t.Fatal(err)
					}
					provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderConversationID: "thread-1", ProviderTurnID: turn.ProviderTurnID},
						ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderConversationID: "thread-1", ProviderTurnID: turn.ProviderTurnID, ProviderItemID: "new-answer", Text: "new live answer"},
						ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderConversationID: "thread-1", ProviderTurnID: turn.ProviderTurnID, TurnState: domain.TurnStateCompleted})
					h := &harness{st: st, ctrl: ctrl}
					h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
						return turnStateByText(t, s)["new live request"] == domain.TurnStateCompleted
					})
					pending, err = st.ListPendingTurnFallbacks(ctx)
					if err != nil || len(pending) != 1 || pending[0].ID == original.ID || pending[0].Excerpt != "new live answer" || !pending[0].CompletedAt.Equal(now.Add(10*time.Minute)) {
						t.Fatalf("live final: %+v %v", pending, err)
					}
				}
				svc.StopAll(ctx)
				if err = st.Close(); err != nil {
					t.Fatal(err)
				}
				st, err = sqlite.OpenPreMigrated(project.Path)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = st.Close() })
			}
		})
	}
}
