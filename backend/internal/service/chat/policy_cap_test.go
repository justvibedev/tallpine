package chat

import (
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestCapabilityAdmission_DoesNotSuggestBypassUnderPolicy(t *testing.T) {
	// Everything but approvals: only a bypass session passes the floor.
	caps := ports.ChatCapabilities{ports.ChatCapabilityStreaming: true, ports.ChatCapabilityInterrupt: true, ports.ChatCapabilityResume: true}
	perm := ports.PermissionModeAuto
	allowed := func(d agentpolicy.Decision) []ports.PermissionMode {
		var cerr *ports.ChatCapabilityError
		if !errors.As(capabilityAdmissionError(domain.HarnessClaudeCode, caps, perm, d), &cerr) {
			t.Fatal("want a capability error")
		}
		return cerr.AllowedPermissionModes
	}
	if len(allowed(agentpolicy.Decision{})) == 0 {
		t.Fatal("without a policy bypass is the suggested alternative")
	}
	if got := allowed(policytest.Cap(t, permissioncap.Auto)); len(got) != 0 {
		t.Fatalf("policy forbids bypass but it was offered: %v", got)
	}
}
