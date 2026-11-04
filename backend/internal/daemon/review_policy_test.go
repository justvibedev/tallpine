package daemon

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestReviewerConversationsGetAPolicyDecision(t *testing.T) {
	engine, _ := policytest.Engine(t, map[string]map[string]any{"permissionCap": {"max": "auto"}})
	got := chatLauncher{policy: engine}.decideReview(context.Background(), "p", domain.HarnessClaudeCode)
	if !permissioncap.Of(got).ForbidsBypass() || got.Session().Project != "p" {
		t.Fatalf("a reviewer chat must start under the policy: %+v", got.Session())
	}
	if permissioncap.Of(chatLauncher{}.decideReview(context.Background(), "p", domain.HarnessClaudeCode)).ForbidsBypass() || (chatLauncher{}.decideReview(context.Background(), "", "") != agentpolicy.Decision{}) {
		t.Fatal("no engine wired restricts nothing")
	}
}
