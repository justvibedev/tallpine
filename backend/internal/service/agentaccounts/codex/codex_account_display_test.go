package codex

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestCodexDisplayExplainsPendingLocalAccountReconciliation(t *testing.T) {
	fixture := newCodexLaunchReadinessFixture(t)
	fixture.manager.reconciliation = domain.CodexDeviceReconciliation{Status: domain.CodexDeviceReconciliationChecking}
	fixture.manager.deviceAccountID = ""
	observation, handled := fixture.service.codexAuthenticationWithAccount(context.Background(), "codex", domain.AgentReadinessPurposeDisplay)
	if !handled || observation.State == domain.AgentAuthenticationUnauthorized || len(observation.Diagnostics) != 1 || observation.Diagnostics[0].Code != "account_sync_pending" {
		t.Fatalf("observation=%+v handled=%v", observation, handled)
	}
}
