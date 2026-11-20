package controllers

import (
	"encoding/json"
	"reflect"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// The store drops what an activity detail repeats (B842): the output keys the
// streamed command_output column replaces on read, and a rawCommand equal to
// command. A client must see the same activity either way. rawCommand itself
// disappears from the wire, which every reader already treats as "command":
// ApprovalCard, the mobile approval sheet and approvalCommand all fall back.
func TestCompactedActivityDetailReadsTheSameAsTheLegacyDetail(t *testing.T) {
	legacy := `{"protocol":"acp","command":"go test ./...","rawCommand":"go test ./...",` +
		`"output":"ok  pkg\n","outputTruncated":true,"outputSource":"aggregate","outputMayBePartial":false,"exitCode":0}`
	compacted := `{"exitCode":0,"command":"go test ./...","protocol":"acp"}`
	for _, truncated := range []bool{false, true} {
		read := func(detail string) map[string]any {
			payload := activityDetailPayload(domain.ConversationActivity{
				Kind: domain.ActivityKindCommand, Detail: json.RawMessage(detail),
				CommandOutput: "ok  pkg\nFAIL other\n", CommandOutputTruncated: truncated,
			})
			if _, ok := payload["rawCommand"]; !ok {
				payload["rawCommand"] = payload["command"]
			}
			return payload
		}
		if got, want := read(compacted), read(legacy); !reflect.DeepEqual(got, want) {
			t.Fatalf("truncated=%v: compacted reads %v, legacy reads %v", truncated, got, want)
		}
	}
	if got, want := approvalCommand(json.RawMessage(compacted)), approvalCommand(json.RawMessage(legacy)); got != want {
		t.Fatalf("approval command = %q, want %q", got, want)
	}
}
