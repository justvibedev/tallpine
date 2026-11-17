package store_test

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// completedTurnOn creates a second session bound to account and settles one
// completed turn on it at when.
func completedTurnOn(t *testing.T, s *sqlite.Store, harness domain.AgentHarness, account string, requested, started, when time.Time) {
	t.Helper()
	ctx := context.Background()
	record := sampleRecord("account-resume")
	record.Harness, record.Mode = harness, domain.SessionModeChat
	record.CreatedAt, record.UpdatedAt = testNow.Add(-time.Hour), testNow
	session, err := s.CreateSession(ctx, record)
	mustNoError(t, err)
	provider, _ := domain.AgentAccountProviderFor(harness)
	mustNoError(t, s.BindSessionAgentAccount(ctx, session.ID, provider, account, testNow.Add(-time.Minute)))
	conversation, err := s.CreateConversation(ctx, "conversation-"+string(session.ID), domain.ConversationScopeSession,
		"account-resume", session.ID, testNow)
	mustNoError(t, err)
	created, err := s.AppendUserMessage(ctx, conversation.ID, session.ID, "generation-root", domain.ConversationMessage{
		ID: "message-" + string(session.ID), Origin: domain.MessageOriginHuman, Text: "work",
		ClientMessageID: "client-" + string(session.ID),
	}, "turn-"+string(session.ID), requested)
	if err != nil || !created {
		t.Fatalf("AppendUserMessage: created=%v err=%v", created, err)
	}
	if !started.IsZero() {
		mustNoError(t, s.BindTurnToProvider(ctx, "turn-"+string(session.ID), "provider-"+string(session.ID), started))
	}
	mustNoError(t, s.SettleTurnByID(ctx, "turn-"+string(session.ID), domain.TurnStateCompleted, "", when))
}

func TestAccountTurnCompletedSince(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	session, _ := seedAccountAutoResume(t, s, domain.HarnessClaudeCode)
	provider, _ := domain.AgentAccountProviderFor(domain.HarnessClaudeCode)
	mustNoError(t, s.BindSessionAgentAccount(ctx, session.ID, provider, "account-a", testNow.Add(-time.Minute)))
	limitedAt := testNow.Add(time.Minute)
	check := func(want bool, why string) {
		t.Helper()
		got, err := s.AccountTurnCompletedSince(ctx, session.ID, limitedAt)
		if err != nil || got != want {
			t.Fatalf("%s: got %v err %v, want %v", why, got, err, want)
		}
	}
	check(false, "no other turn")
	completedTurnOn(t, s, domain.HarnessClaudeCode, "account-b", limitedAt.Add(time.Second), time.Time{}, testNow.Add(2*time.Minute))
	check(false, "other account completed")
	completedTurnOn(t, s, domain.HarnessClaudeCode, "account-a", testNow, time.Time{}, testNow.Add(30*time.Second))
	check(false, "same account completed before the limit turn")
	completedTurnOn(t, s, domain.HarnessClaudeCode, "account-a", testNow, time.Time{}, testNow.Add(3*time.Minute))
	check(false, "same account turn began before the limit turn and only finished after it")
	completedTurnOn(t, s, domain.HarnessClaudeCode, "account-a", testNow, testNow.Add(30*time.Second), testNow.Add(3*time.Minute))
	check(false, "same account turn requested and started before the limit turn")
	completedTurnOn(t, s, domain.HarnessClaudeCode, "account-a", testNow, limitedAt.Add(time.Second), testNow.Add(3*time.Minute))
	check(true, "same account turn queued before the limit turn but started after it")
	check(true, "same account completed after the limit turn")
}
