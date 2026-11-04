package chat

import (
	"encoding/json"
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type noDrivers struct{}

func (noDrivers) Driver(domain.AgentHarness) (ports.ChatDriver, error) {
	return nil, ports.ErrChatUnsupported
}
func (noDrivers) SupportsChat(domain.AgentHarness) bool { return false }

// A side conversation restored after a daemon restart (or retried) rebuilds its
// launch config from the frozen JSON, which holds no policy decision. It must
// take the main chat's, or a side chat whose frozen mode is bypass would launch
// with full access under a cap.
func TestSideLaunchConfigRestoresTheMainChatsPolicy(t *testing.T) {
	frozen, err := json.Marshal(frozenSideConfig{DataDir: "/d", WorkspacePath: "/w", Permissions: ports.PermissionModeBypassPermissions})
	if err != nil {
		t.Fatal(err)
	}
	side := domain.SideConversation{SessionID: "p-1", Harness: domain.HarnessCodex, LaunchConfig: frozen}
	owner := domain.SessionConversationOwner("p-1")

	svc := &Service{drivers: noDrivers{}, startConfigs: map[domain.ConversationOwner]StartConfig{owner: {Policy: policytest.Cap(t, permissioncap.AcceptEdits)}}}
	cfg, _, _ := svc.sideLaunchConfig(side)
	if !permissioncap.Of(cfg.Policy).ForbidsBypass() {
		t.Fatal("the restored side chat lost the main chat's policy")
	}
	if got := permissioncap.Of(cfg.Policy).LowerLive(domain.HarnessCodex, cfg.Permissions); got != domain.PermissionModeAcceptEdits {
		t.Fatalf("frozen bypass must be lowered under the cap, got %q", got)
	}

	// Another session's policy is never borrowed, and a missing main config fails
	// closed: a stopped or not-yet-restored main chat must not yield an uncapped
	// side launch with the frozen (possibly bypass) mode.
	other := &Service{drivers: noDrivers{}, startConfigs: map[domain.ConversationOwner]StartConfig{domain.SessionConversationOwner("p-2"): {Policy: policytest.Cap(t, permissioncap.AcceptEdits)}}}
	if _, _, err := other.sideLaunchConfig(side); !errors.Is(err, ErrNoController) {
		t.Fatalf("a side chat with no main chat config must refuse to launch, got %v", err)
	}
	if _, _, err := (&Service{drivers: noDrivers{}}).sideLaunchConfig(side); !errors.Is(err, ErrNoController) {
		t.Fatalf("no start configs at all must refuse to launch, got %v", err)
	}
}
