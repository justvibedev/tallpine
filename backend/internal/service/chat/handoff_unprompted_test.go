package chat

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// A provider that keeps working after its turn completed (a monitor wakeup)
// writes messages no turn owns; they must still show in the session history.
func TestChatHandoffTurnsKeepsOutputNoTurnOwns(t *testing.T) {
	turns := []domain.ConversationTurn{{ID: "t1", State: domain.TurnStateCompleted}}
	messages := []domain.ConversationMessage{
		{TurnID: "t1", Sequence: 1, Role: domain.MessageRoleUser, Origin: domain.MessageOriginHuman, Text: "do it"},
		{TurnID: "t1", Sequence: 2, Role: domain.MessageRoleAssistant, Text: "first answer"},
		{Sequence: 3, Role: domain.MessageRoleAssistant, Text: "woke up, working", Streaming: true},
		{Sequence: 5, Role: domain.MessageRoleAssistant, Text: "final result after the wakeup", Streaming: true},
	}
	got := chatHandoffTurns(turns, messages, nil)
	if len(got) != 2 || got[0].AgentResponse != "first answer" ||
		got[1].UserRequest != domain.UnpromptedTurnRequest || got[1].AgentResponse != "final result after the wakeup" {
		t.Fatalf("turns = %+v, want the prompted turn then the unprompted one with the run's last message", got)
	}
}
