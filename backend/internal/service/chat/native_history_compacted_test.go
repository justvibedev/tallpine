package chat

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// codexCommandReplay is a Codex thread/read replay of one completed command
// turn: a detail rebuilt from the item (normalize_items.go activityDetail),
// rawCommand and the aggregated output included, under item ids that differ
// from the live stream's.
func codexCommandReplay(detail []byte) []ports.ChatEvent {
	return []ports.ChatEvent{
		{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "native-turn"},
		{Kind: ports.ChatEventUserMessageCompleted, ProviderTurnID: "native-turn", ProviderItemID: "item-1", Text: "run the tests"},
		{
			Kind: ports.ChatEventActivityCompleted, ProviderTurnID: "native-turn", ProviderItemID: "item-2",
			ActivityKind: domain.ActivityKindCommand, ActivityStatus: domain.ActivityStatusCompleted,
			Summary: "go test ./...", Detail: detail,
		},
		{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: "native-turn", ProviderItemID: "item-3", Text: "all green"},
		{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "native-turn", TurnState: domain.TurnStateCompleted},
	}
}

var codexReplayDetail = mustJSON(map[string]any{
	"command": "go test ./...", "rawCommand": "go test ./...", "cwd": "/ws", "exitCode": 0,
	"output": "ok  pkg\n", "outputMayBePartial": true, "outputSource": "aggregate",
})

func mustJSON(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

func storedCommandTurn(detail []byte) ([]domain.ConversationTurn, []domain.ConversationMessage, []domain.ConversationActivity) {
	turns := []domain.ConversationTurn{{ID: "turn-1", ProviderTurnID: "native-turn", State: domain.TurnStateCompleted}}
	messages := []domain.ConversationMessage{
		{TurnID: "turn-1", Sequence: 1, Role: domain.MessageRoleUser, ProviderItemID: "msg_user", Text: "run the tests"},
		{TurnID: "turn-1", Sequence: 3, Role: domain.MessageRoleAssistant, ProviderItemID: "msg_answer", Text: "all green"},
	}
	activities := []domain.ConversationActivity{{
		TurnID: "turn-1", Sequence: 2, Kind: domain.ActivityKindCommand, Status: domain.ActivityStatusCompleted,
		ProviderItemID: "call_live", Summary: "go test ./...", Detail: detail,
	}}
	return turns, messages, activities
}

func reimported(events []ports.ChatEvent) bool {
	for _, event := range events {
		if event.Kind == ports.ChatEventActivityCompleted {
			return true
		}
	}
	return false
}

// Break caught in review of B842: the store drops a streamed command's output
// copy and a rawCommand equal to command from the row, while a Codex replay
// still carries both, so the detail digest the replay matches by (its item ids
// differ from the live ones) differed and every streamed command was
// re-imported on history resync. The stored detail below is what the SQL
// backfill leaves (json_remove keeps the original key order).
func TestReconcileNativeHistoryMatchesCompactedStreamedCommand(t *testing.T) {
	t.Parallel()
	compacted := []byte(`{"command":"go test ./...","cwd":"/ws","exitCode":0}`)
	turns, messages, activities := storedCommandTurn(compacted)
	if got := reconcileNativeHistory(codexCommandReplay(codexReplayDetail), turns, messages, activities); reimported(got) {
		t.Fatalf("compacted streamed command re-imported: %+v", got)
	}

	// A row that kept its output (it never streamed) still needs its output
	// to match.
	kept := mustJSON(map[string]any{"command": "go test ./...", "cwd": "/ws", "exitCode": 0,
		"output": "different\n", "outputMayBePartial": true, "outputSource": "aggregate"})
	turns, messages, activities = storedCommandTurn(kept)
	if got := reconcileNativeHistory(codexCommandReplay(codexReplayDetail), turns, messages, activities); !reimported(got) {
		t.Fatal("a replay with different output matched a row that kept its own")
	}
}

// A history prune recorded the plain SHA-256 of the original bytes before
// digests were canonical; such a row still matches a byte-identical replay.
func TestReconcileNativeHistoryMatchesRowPrunedBeforeCanonicalDigests(t *testing.T) {
	t.Parallel()
	sum := sha256.Sum256(codexReplayDetail)
	pruned := mustJSON(map[string]any{"command": "go test ./...", "output": "ok", "outputTruncated": true,
		domain.ActivityDetailOriginalDigestKey: hex.EncodeToString(sum[:])})
	turns, messages, activities := storedCommandTurn(pruned)
	if got := reconcileNativeHistory(codexCommandReplay(codexReplayDetail), turns, messages, activities); reimported(got) {
		t.Fatalf("row pruned with a legacy digest re-imported: %+v", got)
	}
}

// The high-water boundary compares the newest settled activity with the
// replay the same way, so a compacted boundary row is no mismatch.
func TestHighWaterBoundaryMatchesCompactedCommand(t *testing.T) {
	t.Parallel()
	compacted := []byte(`{"command":"go test ./...","cwd":"/ws","exitCode":0}`)
	turns, messages, activities := storedCommandTurn(compacted)
	checkpoint := nativeHistoryCheckpoint{tallpineHighWater: nativeHistoryHighWater{
		providerTurnID: "native-turn", activityKind: domain.ActivityKindCommand,
		activityStatus: domain.ActivityStatusCompleted, summary: "go test ./...", detail: compacted,
	}}
	completed := map[string]bool{"native-turn": true}
	if got := checkpoint.highWaterMismatches(nil, codexCommandReplay(codexReplayDetail), completed, turns, messages, activities); len(got) != 0 {
		t.Fatalf("compacted boundary reported a mismatch: %v", got)
	}
	other := mustJSON(map[string]any{"command": "go vet ./...", "exitCode": 0})
	if got := checkpoint.highWaterMismatches(nil, codexCommandReplay(other), completed, turns, messages, activities); len(got) == 0 {
		t.Fatal("a different command satisfied the boundary")
	}
}

// A fork copies its ancestry from the parent conversation and the replay
// repeats it; the copied prefix is dropped only when every replayed activity
// matches a copied row, including a compacted streamed command.
func TestCopiedPrefixMatchesCompactedStreamedCommand(t *testing.T) {
	t.Parallel()
	turns, messages, activities := storedCommandTurn([]byte(`{"command":"go test ./...","cwd":"/ws","exitCode":0}`))
	replay := codexCommandReplay(codexReplayDetail)
	rows := ConversationRows{Turns: turns, Messages: messages, Activities: activities}
	if got := omitCopiedPrefix(replay, replay, rows); len(got) != 0 {
		t.Fatalf("copied compacted turn kept %d replayed events, want none", len(got))
	}
}
