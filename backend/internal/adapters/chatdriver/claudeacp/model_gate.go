package claudeacp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/claudecode"
	acpdriver "tallpine.eu/tallpine/backend/internal/adapters/chatdriver/acp"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// claudeVersionTooOldFailure recognizes the API rejecting a model the installed
// Claude Code predates. It records the reported minimum for the model so the
// catalog marks it unavailable from now on, and replaces the raw
// "API Error: 400 {json}" with the remedy.
func claudeVersionTooOldFailure(title, details, model string) error {
	text := title + "\n" + details
	parsed, ok := claudecode.ParseVersionTooOld(text)
	if !ok {
		if !claudecode.IsVersionTooOld(text) {
			return nil
		}
		return ports.NewChatProviderFailure(
			"The installed Claude Code is too old for this model. Run `claude update`, then try again.",
			"", ports.ErrAgentVersionTooOld)
	}
	model = strings.TrimSpace(model)
	if model != "" && !isClaudeNativeModelAlias(model) {
		// Aliases resolve differently per CLI version, so only a concrete model
		// ID carries a stable requirement.
		_ = claudecode.DefaultModelVersionRequirements().Record(model, parsed.Required)
	}
	subject := "this model"
	if model != "" {
		subject = model
	}
	return ports.NewChatProviderFailure(
		fmt.Sprintf("Claude Code %s does not support %s; version %s or newer is required.",
			parsed.Installed, subject, parsed.Required),
		"Run `claude update` (or update the Claude desktop app), then try again.",
		ports.ErrAgentVersionTooOld)
}

// claudeValidateModel refuses a model the installed Claude Code is known to be
// too old for, so a running chat cannot switch to what its next turn would
// fail on. An alias always passes: each CLI resolves it to a model it supports.
func claudeValidateModel(ctx context.Context, plugin claudePlugin, model string) error {
	if isClaudeNativeModelAlias(model) {
		return nil
	}
	// A binary Tallpine cannot resolve has no known version, so the model may be tried.
	if binary, err := plugin.ResolveBinary(ctx); err == nil {
		if reason := claudecode.UnsupportedModelReason(ctx, binary, model); reason != "" {
			return fmt.Errorf("%w: model %q: %s", ports.ErrAgentVersionTooOld, model, reason)
		}
	}
	return nil
}

// claudeServedModel reports what a Claude alias actually resolved to. The ACP
// adapter does not surface the answering model, but Claude's own transcript
// records it on every assistant message. since is the turn start: a turn that
// wrote no assistant message (/clear, local commands) reports nothing instead
// of the previous turn's model.
func claudeServedModel(ctx context.Context, cfg acpdriver.LaunchConfig, sessionID, selected string, since time.Time) (string, string) {
	if !isClaudeNativeModelAlias(selected) {
		return "", ""
	}
	served, err := claudecode.LastAssistantModelSince(ctx, cfg.Env, sessionID, since)
	if err != nil || served == "" {
		return "", ""
	}
	return served, fmt.Sprintf("The installed Claude Code resolves %q to %s", selected, served)
}
