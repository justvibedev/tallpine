package store_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

func TestBindSessionAgentAccountRoundTripKeepsAutoResume(t *testing.T) {
	for _, harness := range []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessCodex} {
		t.Run(string(harness), func(t *testing.T) {
			ctx := context.Background()
			s := newTestStore(t)
			session, _ := seedAccountAutoResume(t, s, harness)
			provider, _ := domain.AgentAccountProviderFor(harness)
			mustNoError(t, s.BindSessionAgentAccount(ctx, session.ID, provider, "account-a", testNow.Add(-time.Minute)))
			mustNoError(t, s.BindSessionAgentAccount(ctx, session.ID, provider, "account-b", testNow.Add(10*time.Minute)))
			assertAccountAutoResume(t, s, session.ID, true, "")
			mustNoError(t, s.BindSessionAgentAccount(ctx, session.ID, provider, "account-a", testNow.Add(20*time.Minute)))
			assertAccountAutoResume(t, s, session.ID, true, "")
		})
	}
}

func TestSetTurnAutoResumeRejectsChangedSourceAccount(t *testing.T) {
	for _, harness := range []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessCodex} {
		t.Run(string(harness), func(t *testing.T) {
			ctx := context.Background()
			s := newTestStore(t)
			session, conversation := seedAccountAutoResume(t, s, harness)
			provider, _ := domain.AgentAccountProviderFor(harness)
			turnID := "turn-" + string(session.ID)
			mustNoError(t, s.BindSessionAgentAccount(ctx, session.ID, provider, "account-a", testNow.Add(-time.Minute)))
			// The switch precedes completion; the turn belongs to its requested account.
			mustNoError(t, s.BindSessionAgentAccount(ctx, session.ID, provider, "account-b", testNow.Add(30*time.Second)))
			reset := testNow.Add(2 * time.Hour)
			if err := s.SetTurnAutoResume(ctx, conversation.ID, turnID, &reset, true, ""); !errors.Is(err, ports.ErrAgentAccountChanged) {
				t.Fatalf("re-arm changed account = %v", err)
			}
			if err := s.SetTurnAutoResume(ctx, "other-conversation", turnID, &reset, true, ""); !errors.Is(err, domain.ErrNoConversationTurn) {
				t.Fatalf("re-arm wrong conversation = %v", err)
			}
			// Returning to the source account makes its known deadline usable again.
			mustNoError(t, s.BindSessionAgentAccount(ctx, session.ID, provider, "account-a", testNow.Add(2*time.Minute)))
			reset = testNow.Add(time.Hour)
			mustNoError(t, s.SetTurnAutoResume(ctx, conversation.ID, turnID, &reset, true, ""))
			assertAccountAutoResume(t, s, session.ID, true, "")
		})
	}
}

func TestSetTurnAutoResumePreservesUnknownSourceAccount(t *testing.T) {
	for _, harness := range []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessCodex} {
		t.Run(string(harness), func(t *testing.T) {
			ctx := context.Background()
			s := newTestStore(t)
			session, conversation := seedAccountAutoResume(t, s, harness)
			provider, _ := domain.AgentAccountProviderFor(harness)
			mustNoError(t, s.BindSessionAgentAccount(ctx, session.ID, provider, "account-a", testNow.Add(time.Minute)))
			mustNoError(t, s.BindSessionAgentAccount(ctx, session.ID, provider, "account-b", testNow.Add(2*time.Minute)))
			reset := testNow.Add(time.Hour)
			mustNoError(t, s.SetTurnAutoResume(ctx, conversation.ID, "turn-"+string(session.ID), &reset, true, ""))
			assertAccountAutoResume(t, s, session.ID, true, "")
		})
	}
}

func TestSetTurnAutoResumeCannotRearmRetriedSource(t *testing.T) {
	for _, harness := range []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessCodex} {
		for _, concurrent := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/concurrent=%v", harness, concurrent), func(t *testing.T) {
				ctx := context.Background()
				s := newTestStore(t)
				session, conversation := seedAccountAutoResume(t, s, harness)
				turnID := "turn-" + string(session.ID)
				retry := func() error {
					created, err := s.AppendRetryUserMessage(ctx, conversation.ID, session.ID, "generation-root",
						domain.ConversationMessage{ID: "manual-message", Origin: domain.MessageOriginHuman, Text: "finish this task"},
						"manual-retry", turnID, testNow.Add(2*time.Minute))
					if err == nil && !created {
						return errors.New("manual retry was not recorded")
					}
					return err
				}
				reset := testNow.Add(time.Hour)
				if concurrent {
					start, results := make(chan struct{}), make(chan error, 2)
					go func() { <-start; results <- retry() }()
					go func() {
						<-start
						err := s.SetTurnAutoResume(ctx, conversation.ID, turnID, &reset, true, "")
						if errors.Is(err, domain.ErrNoConversationTurn) {
							err = nil
						}
						results <- err
					}()
					close(start)
					for range 2 {
						mustNoError(t, <-results)
					}
				} else {
					mustNoError(t, retry())
				}
				if err := s.SetTurnAutoResume(ctx, conversation.ID, turnID, &reset, true, ""); !errors.Is(err, domain.ErrNoConversationTurn) {
					t.Fatalf("re-arm retried source = %v", err)
				}
				assertAccountAutoResume(t, s, session.ID, false, "")
				id, found, err := s.RetryTurnIDForSource(ctx, conversation.ID, turnID)
				if err != nil || !found || id != "manual-retry" {
					t.Fatalf("retry receipt changed: %q, %v, %v", id, found, err)
				}
			})
		}
	}
}

func TestPostponeTurnAutoResumeNeverRestoresConsumedIntent(t *testing.T) {
	for _, state := range []string{"armed", "cancelled", "refused", "retried"} {
		t.Run(state, func(t *testing.T) {
			ctx := context.Background()
			s := newTestStore(t)
			session, conversation := seedAccountAutoResume(t, s, domain.HarnessClaudeCode)
			turnID := "turn-" + string(session.ID)
			diagnostic := ""
			switch state {
			case "cancelled", "refused":
				if state == "refused" {
					diagnostic = "original refusal"
				}
				mustNoError(t, s.SetTurnAutoResume(ctx, conversation.ID, turnID, nil, false, diagnostic))
			case "retried":
				created, err := s.AppendRetryUserMessage(ctx, conversation.ID, session.ID, "generation-root",
					domain.ConversationMessage{ID: "manual-message", Origin: domain.MessageOriginHuman, Text: "finish this task"},
					"manual-retry", turnID, testNow.Add(2*time.Minute))
				mustNoError(t, err)
				if !created {
					t.Fatal("manual retry was not recorded")
				}
			}
			reset := testNow.Add(2 * time.Hour)
			before, err := s.LatestSeq(ctx)
			mustNoError(t, err)
			if moved, err := s.PostponeTurnAutoResume(ctx, "other-conversation", turnID, reset); err != nil || moved {
				t.Fatalf("postpone wrong conversation = %v, %v", moved, err)
			}
			if moved, err := s.PostponeTurnAutoResume(ctx, conversation.ID, "missing", reset); err != nil || moved {
				t.Fatalf("postpone missing turn = %v, %v", moved, err)
			}
			moved, err := s.PostponeTurnAutoResume(ctx, conversation.ID, turnID, reset)
			if err != nil || moved != (state == "armed") {
				t.Fatalf("postpone %s = %v, %v", state, moved, err)
			}
			events, err := s.EventsAfter(ctx, before, 10)
			mustNoError(t, err)
			wantEvents := 0
			if moved {
				wantEvents = 1
			}
			if len(events) != wantEvents {
				t.Fatalf("postpone CDC events = %+v, want %d", events, wantEvents)
			}
			if moved && (events[0].SessionID != string(session.ID) || events[0].Type != "session_updated") {
				t.Fatalf("postpone CDC attribution = %+v", events[0])
			}
			if state != "armed" {
				assertAccountAutoResume(t, s, session.ID, false, diagnostic)
				return
			}
			source, err := s.TurnByID(ctx, turnID)
			mustNoError(t, err)
			if source.UsageLimit == nil || !source.UsageLimit.AutoResume || source.UsageLimit.ResetsAt == nil ||
				!source.UsageLimit.ResetsAt.Equal(reset) || source.UsageLimit.ResumeError != "" {
				t.Fatalf("postponed intent = %+v", source.UsageLimit)
			}
		})
	}
}

func seedAccountAutoResume(t *testing.T, s *sqlite.Store, harness domain.AgentHarness) (domain.SessionRecord, domain.ConversationRecord) {
	t.Helper()
	ctx := context.Background()
	seedProject(t, s, "account-resume")
	record := sampleRecord("account-resume")
	record.Harness, record.Mode = harness, domain.SessionModeChat
	record.CreatedAt, record.UpdatedAt = testNow.Add(-time.Hour), testNow
	record.Metadata.ControllerGeneration = "generation-root"
	record.Metadata.ProviderConversationID = "native-thread"
	session, err := s.CreateSession(ctx, record)
	mustNoError(t, err)
	conversation, err := s.CreateConversation(ctx, "conversation-"+string(session.ID), domain.ConversationScopeSession,
		"account-resume", session.ID, testNow)
	mustNoError(t, err)
	appendBranchPrompt(t, s, session, conversation, "generation-root", string(session.ID), "finish this task")
	turnID := "turn-" + string(session.ID)
	mustNoError(t, s.SettleTurnByID(ctx, turnID, domain.TurnStateFailed, "Usage limit reached", testNow.Add(time.Minute)))
	reset := testNow.Add(time.Hour)
	mustNoError(t, s.SetTurnAutoResume(ctx, conversation.ID, turnID, &reset, true, ""))
	return session, conversation
}

func assertAccountAutoResume(t *testing.T, s *sqlite.Store, id domain.SessionID, armed bool, diagnostic string) {
	t.Helper()
	source, err := s.TurnByID(context.Background(), "turn-"+string(id))
	if err != nil || source.UsageLimit == nil {
		t.Fatalf("source = %+v, %v", source, err)
	}
	limit := source.UsageLimit
	if limit.AutoResume != armed || limit.ResumeError != diagnostic || limit.ResetsAt == nil ||
		!limit.ResetsAt.Equal(testNow.Add(time.Hour)) || source.ErrorMessage != "Usage limit reached" {
		t.Fatalf("source metadata changed: source=%+v limit=%+v", source, limit)
	}
}
