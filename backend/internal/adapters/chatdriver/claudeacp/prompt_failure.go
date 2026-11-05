package claudeacp

import (
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/ports"
)

func claudePromptResponseFailure(response acpsdk.PromptResponse, model string) error {
	if response.StopReason != acpsdk.StopReasonEndTurn {
		return nil
	}
	air := claudeNestedMap(claudeNestedMap(response.Meta, "jetbrains"), "air")
	version, versionOK := air["version"].(float64)
	failure := claudeNestedMap(air, "sessionFailure")
	id, _ := failure["id"].(string)
	title, _ := failure["title"].(string)
	if !versionOK || version < 1 || strings.TrimSpace(id) == "" || strings.TrimSpace(title) == "" || failure["severity"] != "error" {
		return nil
	}
	details, _ := failure["details"].(string)
	if tooOld := claudeVersionTooOldFailure(title, details, model); tooOld != nil {
		return tooOld
	}
	var cause error
	if actions, ok := failure["actions"].([]any); ok {
		for _, action := range actions {
			if action == "login" {
				cause = ports.ErrChatAuthRequired
				break
			}
		}
	}
	return ports.NewChatProviderFailure(strings.TrimSpace(title), strings.TrimSpace(details), cause)
}
