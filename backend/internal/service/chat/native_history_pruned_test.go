package chat

import (
	"encoding/json"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// prunedDetailLike builds what a history prune stores for original: a head of
// output with its flag and the digest of the original detail.
func prunedDetailLike(t *testing.T, original []byte) []byte {
	t.Helper()
	pruned, err := json.Marshal(map[string]any{
		"command": "go test ./...", "output": "line", "outputTruncated": true,
		domain.ActivityDetailOriginalDigestKey: domain.ActivityDetailDigest(original),
	})
	if err != nil {
		t.Fatal(err)
	}
	return pruned
}

// Break caught: a history prune shortens old activity details in place, while
// native-history reconciliation matched a replayed activity to its stored row
// by exact detail bytes. Codex replays items under ids that differ from the
// live stream, so that fingerprint is the only match, and a resume replaying
// a pruned turn re-imported every shortened activity as a duplicate. The
// fingerprint now compares detail digests, and a pruned row carries the digest
// of its original.
func TestReconcileNativeHistoryMatchesPrunedActivity(t *testing.T) {
	t.Parallel()
	original := []byte(`{"command":"go test ./...","output":"` + strings.Repeat("line\n", 2000) + `"}`)
	replay := func(detail []byte) []ports.ChatEvent {
		return []ports.ChatEvent{
			{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "replayed-turn"},
			{Kind: ports.ChatEventUserMessageCompleted, ProviderTurnID: "replayed-turn", ProviderItemID: "item-1", Text: "run the tests"},
			{
				Kind: ports.ChatEventActivityCompleted, ProviderTurnID: "replayed-turn", ProviderItemID: "item-2",
				ActivityKind: domain.ActivityKindCommand, ActivityStatus: domain.ActivityStatusCompleted,
				Summary: "go test ./...", Detail: detail,
			},
			{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: "replayed-turn", ProviderItemID: "item-3", Text: "all green"},
			{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "replayed-turn", TurnState: domain.TurnStateCompleted},
		}
	}
	turns := []domain.ConversationTurn{{ID: "turn-1", ProviderTurnID: "live-turn", State: domain.TurnStateCompleted}}
	messages := []domain.ConversationMessage{
		{TurnID: "turn-1", Role: domain.MessageRoleUser, ProviderItemID: "msg_user", Text: "run the tests"},
		{TurnID: "turn-1", Role: domain.MessageRoleAssistant, ProviderItemID: "msg_answer", Text: "all green"},
	}
	activities := []domain.ConversationActivity{{
		TurnID: "turn-1", Kind: domain.ActivityKindCommand, Status: domain.ActivityStatusCompleted,
		ProviderItemID: "call_live", Summary: "go test ./...", Detail: prunedDetailLike(t, original),
	}}

	got := reconcileNativeHistory(replay(original), turns, messages, activities)
	for _, event := range got {
		if event.Kind != ports.ChatEventTurnStarted && event.Kind != ports.ChatEventTurnCompleted {
			t.Fatalf("pruned turn re-imported %s: %#v", event.Kind, event)
		}
	}
	if len(got) != 2 {
		t.Fatalf("reconciled events = %d, want only the turn boundaries", len(got))
	}

	// A replay whose activity differs from the original is still new.
	changed := reconcileNativeHistory(replay([]byte(`{"command":"go test ./...","output":"other"}`)), turns, messages, activities)
	imported := false
	for _, event := range changed {
		imported = imported || event.Kind == ports.ChatEventActivityCompleted
	}
	if !imported {
		t.Fatal("a different activity matched a pruned row")
	}
}
