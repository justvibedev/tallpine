package acp

import (
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// B491: a steer goes out as steerCancelNotice+text but Tallpine records only
// text. On replay the notice must be stripped so native-history reconcile
// fingerprints the message as already imported instead of adding a duplicate.
func TestACPReplayStripsSteerCancelNotice(t *testing.T) {
	conv := replayConversation()
	conv.beginHistoryReplay("session-1")
	half := len(steerCancelNotice) / 2
	history := replayHistory(t, conv, []acpsdk.SessionNotification{
		replayUserChunk("session-1", "u1", "do the long thing"),
		replayAgentChunk("session-1", "a1", "working"),
		replayUserChunk("session-1", "u2", steerCancelNotice[:half]),
		replayUserChunk("session-1", "u2", steerCancelNotice[half:]+"hold, don't push"),
		replayAgentChunk("session-1", "a2", "ok"),
	})
	var userTexts []string
	for _, event := range history {
		if event.Kind == ports.ChatEventUserMessageCompleted {
			userTexts = append(userTexts, event.Text)
		}
	}
	if len(userTexts) != 2 || userTexts[1] != "hold, don't push" {
		t.Fatalf("user texts = %q, want the steer replayed as recorded (no notice)", userTexts)
	}
}
