package session

import (
	"errors"
	"fmt"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

// A launch the hardened profile refuses reaches the API as 409
// POLICY_REQUIRES_FENCE naming the harness and OS, on spawn and restore.
func TestFenceRefusalMapsToPolicyRequiresFence(t *testing.T) {
	wrapped := fmt.Errorf("spawn: %w", &agentpolicy.FenceRequiredError{Harness: "codex", GOOS: "darwin"})
	for name, mapped := range map[string]error{"restore": toAPIError(wrapped), "spawn": toSpawnAPIError(wrapped)} {
		var e *apierr.Error
		if !errors.As(mapped, &e) || e.Kind != apierr.KindConflict || e.Code != agentpolicy.CodeRequiresFence {
			t.Fatalf("%s: mapped = %#v", name, mapped)
		}
		if e.Details["harness"] != "codex" || e.Details["os"] != "darwin" {
			t.Fatalf("%s: details = %v", name, e.Details)
		}
	}
}
