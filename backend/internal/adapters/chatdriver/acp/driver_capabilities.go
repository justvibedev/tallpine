package acp

import (
	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/ports"
)

func cloneCapabilities(in ports.ChatCapabilities) ports.ChatCapabilities {
	out := make(ports.ChatCapabilities, len(in))
	for capability, enabled := range in {
		out[capability] = enabled
	}
	return out
}

func conversationCapabilities(
	configured ports.ChatCapabilities,
	init acpsdk.InitializeResponse,
) ports.ChatCapabilities {
	caps := cloneCapabilities(configured)
	if !supportsSessionRestore(init) {
		caps[ports.ChatCapabilityResume] = false
	}
	caps[ports.ChatCapabilityHistory] = init.AgentCapabilities.LoadSession
	caps[ports.ChatCapabilityFork] = false // ACP has no turn-anchored fork in the pinned SDK.
	caps[ports.ChatCapabilityForkCurrent] = init.AgentCapabilities.SessionCapabilities.Fork != nil &&
		supportsSessionRestore(init)
	if extensionSupported(init.Meta, "steering") {
		caps[ports.ChatCapabilitySteer] = true
	}
	caps[ports.ChatCapabilityImages] = init.AgentCapabilities.PromptCapabilities.Image
	caps[ports.ChatCapabilityEmbeddedContext] = init.AgentCapabilities.PromptCapabilities.EmbeddedContext
	// ResourceLink is a baseline ACP content block rather than an optional prompt
	// capability. Every ACP conversation preserves it natively.
	caps[ports.ChatCapabilityResourceLinks] = true
	// These are facilities Tallpine itself negotiated as the ACP client. An agent that
	// never uses them simply produces no matching events.
	caps[ports.ChatCapabilityElicitation] = true
	caps[ports.ChatCapabilityNestedAgents] = true
	caps[ports.ChatCapabilityTerminalOutput] = true
	return caps
}

func supportsSessionRestore(init acpsdk.InitializeResponse) bool {
	return init.AgentCapabilities.LoadSession ||
		init.AgentCapabilities.SessionCapabilities.Resume != nil
}

func extensionSupported(meta map[string]any, name string) bool {
	extension, ok := meta[name].(map[string]any)
	if !ok {
		return false
	}
	supported, _ := extension["supported"].(bool)
	return supported
}

func pointer[T any](value T) *T { return &value }
