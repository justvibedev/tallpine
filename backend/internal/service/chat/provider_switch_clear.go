package chat

import (
	"context"
	"errors"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// ErrClearWhileBusy reports a /clear on a conversation with a turn in flight. A
// clear replaces the provider conversation the turn runs on, so it waits for
// the turn to end.
var ErrClearWhileBusy = errors.New("wait for the current turn to finish before clearing the context") //nolint:staticcheck // user-facing copy

// contextClearedSummary is the transcript marker for a cleared context.
const contextClearedSummary = "Context cleared"

// clearsByHandoff reports a harness whose /clear Tallpine carries out as a
// handoff to a fresh context of the same agent. Claude Code clears in place
// when the text reaches it, but that keeps only the system prompt: the task
// brief and everything else Tallpine sent as messages are gone, and the
// adapter keeps resuming the transcript from before the clear, so a restart
// brings the old context back and loses what came after. Codex's app-server
// reads slash commands as prompt text, and a bare new thread would keep only
// the developer instructions, so it hands over the same way.
func clearsByHandoff(harness domain.AgentHarness) bool {
	return harness == domain.HarnessClaudeCode || harness == domain.HarnessCodex
}

// clearByHandoff runs a /clear as a provider switch to the same agent: the
// source stops, Tallpine writes a handoff note with the task, the workspace
// state and a record of the conversation, and a fresh provider conversation
// starts with the standing instructions and that note in its system prompt.
// The model, effort, fast mode and compaction window carry over. It returns
// the transcript marker's id.
func (s *Service) clearByHandoff(ctx context.Context, id domain.SessionID) (string, error) {
	unlock, err := s.lockProviderSwitch(ctx, id)
	if err != nil {
		return "", err
	}
	defer unlock()
	record, err := s.requireChatSession(ctx, id)
	if err != nil {
		return "", err
	}
	conversation, err := s.store.ConversationForSession(ctx, id)
	if err != nil {
		return "", err
	}
	settings := conversation.Settings
	if controller, err := s.Controller(id); err == nil {
		settings = controller.Settings()
	}
	request := domain.ProviderSwitchRequest{
		TargetHarness: record.Harness, IdempotencyKey: s.newID(),
		Model: settings.Model, Effort: settings.ReasoningEffort, FastMode: settings.FastMode,
		AutoCompactWindow: conversation.Settings.AutoCompactWindow,
		ClearContext:      true,
	}
	if err := s.switchProvider(ctx, id, request); err != nil {
		return "", clearRefusal(err)
	}
	cleared, err := s.store.ConversationForSession(context.WithoutCancel(ctx), id)
	if err != nil {
		// The switch committed; only the marker id is unknown.
		s.log.Warn("context cleared but the marker id was not read", "session", id, "err", err)
		return "", nil
	}
	return contextClearedActivityID(cleared.ActiveBranchID), nil
}

// clearRefusal maps a busy source to ErrClearWhileBusy, so every client shows
// the clear's own message for it.
func clearRefusal(err error) error {
	var refusal *domain.ProviderSwitchError
	if errors.As(err, &refusal) && refusal.Kind == domain.ProviderSwitchRefusalBusy {
		return ErrClearWhileBusy
	}
	return err
}

func contextClearedActivityID(branchID string) string {
	return branchID + ":context-cleared"
}

// contextClearedActivity is the transcript marker of a /clear handoff. It is
// not a provider_switch marker: nothing changed agent, and clients draw those
// as a switch.
func contextClearedActivity(branchID, requestID string) domain.ConversationActivity {
	id := contextClearedActivityID(branchID)
	return domain.ConversationActivity{
		ID: id, Kind: domain.ActivityKindSystem, Status: domain.ActivityStatusCompleted,
		Summary: contextClearedSummary, RequestID: strings.TrimSpace(requestID), ProviderItemID: id,
	}
}
