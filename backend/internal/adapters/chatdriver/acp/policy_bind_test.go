package acp

import (
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// A conversation's mode mapping and permission policy are bound to the decision
// the conversation started under, not to whatever the driver was configured with.
func TestDriverBindsTheConversationsDecisionToItsPolicyFuncs(t *testing.T) {
	var sawMode, sawPolicy agentpolicy.Decision
	d := &Driver{cfg: Config{
		SessionMode: func(decision agentpolicy.Decision, _ ports.PermissionMode) string {
			sawMode = decision
			return "m"
		},
		PermissionPolicy: func(decision agentpolicy.Decision, _ ports.PermissionMode, _ acpsdk.RequestPermissionRequest) (acpsdk.PermissionOptionId, bool) {
			sawPolicy = decision
			return "x", true
		},
	}}
	bound := policytest.Cap(t, permissioncap.AcceptEdits)
	if got := d.sessionModeFor(bound)(ports.PermissionModeAuto); got != "m" || !permissioncap.Of(sawMode).ForbidsBypass() {
		t.Fatalf("session mode saw %+v", sawMode)
	}
	if _, ok := d.permissionPolicyFor(bound)(ports.PermissionModeAuto, acpsdk.RequestPermissionRequest{}); !ok || !permissioncap.Of(sawPolicy).ForbidsBypass() {
		t.Fatalf("permission policy saw %+v", sawPolicy)
	}
	if (&Driver{}).sessionModeFor(bound) != nil || (&Driver{}).permissionPolicyFor(bound) != nil {
		t.Fatal("an unconfigured driver must bind nothing")
	}
}
