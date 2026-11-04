package registry

import (
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// What the daemon ships, stated once.
//
// Registration is the whole capability gate — a harness with no driver here cannot
// run chat mode — so the shipped set is a release decision, not an implementation
// detail. Codex uses its native app-server and Claude uses the reusable ACP
// transport, and DeepSeek Harness uses `dsh --profile acp` through the deepseekharnessacp driver. Every remaining harness, Kiro included, is deliberately TUI-only.
func TestShippedChatDrivers(t *testing.T) {
	r := Build(nil, nil)

	for _, harness := range []domain.AgentHarness{
		domain.HarnessCodex,
		domain.HarnessClaudeCode,
		domain.HarnessDeepSeek,
		domain.HarnessKiro,
	} {
		if !r.SupportsChat(harness) {
			t.Errorf("%s has no chat driver", harness)
		}
		if _, err := r.Driver(harness); err != nil {
			t.Errorf("resolving the %s driver: %v", harness, err)
		}
	}

	// Every other harness stays TUI-only, and asking for chat must be refused with a
	// typed answer rather than quietly producing a terminal session.
	for _, harness := range []domain.AgentHarness{
		"opencode", // removed harness
		"definitely-not-an-agent",
	} {
		if _, err := r.Driver(harness); err == nil {
			t.Errorf("%s resolved a chat driver", harness)
		} else if !errors.Is(err, ports.ErrChatUnsupported) {
			t.Errorf("%s refused with %v, want ErrChatUnsupported so callers can branch", harness, err)
		}
	}
}
