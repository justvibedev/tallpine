package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type probeCodexClient struct{ email string }

func (c probeCodexClient) Read(context.Context, bool) (ports.CodexAccountObservation, error) {
	return ports.CodexAccountObservation{Authentication: domain.AgentAuthenticationAuthorized, Method: domain.CodexAuthMethodChatGPT, Email: &c.email}, nil
}
func (probeCodexClient) Logout(context.Context) error { return nil }
func (probeCodexClient) ReadCapacity(context.Context) (ports.CodexCapacityObservation, error) {
	return ports.CodexCapacityObservation{}, nil
}
func (probeCodexClient) ReadUsage(context.Context) (ports.CodexUsageObservation, error) {
	return ports.CodexUsageObservation{}, nil
}
func (probeCodexClient) ConsumeResetCredit(context.Context, string) (domain.CodexResetCreditOutcome, error) {
	return "", nil
}
func (probeCodexClient) Events() <-chan ports.CodexAccountEvent {
	ch := make(chan ports.CodexAccountEvent)
	close(ch)
	return ch
}
func (probeCodexClient) Close() error { return nil }

type probeCodexFactory struct{ email string }

func (f probeCodexFactory) Open(context.Context, ports.CodexAccountContext) (ports.CodexAccountClient, error) {
	return probeCodexClient(f), nil
}
func (probeCodexFactory) Capabilities(context.Context) domain.CodexAccountCapabilities {
	supported := domain.CodexCapabilityObservation{State: domain.CodexCapabilitySupported, ReasonCode: domain.CodexCapabilityReasonSupported, Reason: "supported"}
	return domain.CodexAccountCapabilities{AccountRead: supported, NativeLogin: supported, CapacityRead: supported, UsageRead: supported, ResetCreditConsume: supported, GlobalSwitch: supported}
}

// Moved from the Codex account tests: the production wiring (AuthenticationWithAccount
// on the readiness coordinator) must see a first login without a daemon restart.
func TestProbeRecognizesFirstCodexLoginWithoutDaemonRestart(t *testing.T) {
	root := t.TempDir()
	globalHome := filepath.Join(root, "global-codex")
	if err := os.MkdirAll(globalHome, 0o700); err != nil {
		t.Fatal(err)
	}
	svc := NewWithDeps(Deps{
		CodexAccountRoot:       filepath.Join(root, "accounts"),
		CodexPendingRoot:       filepath.Join(root, "pending"),
		CodexSwitchStagingRoot: filepath.Join(root, "staging"),
		CodexGlobalHome:        globalHome,
		CodexAccounts:          probeCodexFactory{email: "signed-in@example.com"},
	})
	svc.agents = []ports.HarnessAgent{readinessHarness("codex", "Codex", &readinessTestAgent{
		resolve: func(context.Context) (string, error) { return "codex", nil },
		auth:    func(context.Context) (ports.AgentAuthStatus, error) { return ports.AgentAuthStatusAuthorized, nil },
	})}
	svc.readiness = newReadinessCoordinator(readinessCoordinatorConfig{Agents: svc.agents, AuthenticationCheck: svc.Codex().AuthenticationWithAccount})
	ctx := context.Background()
	if err := svc.Codex().WaitCodexAccountStoreReady(ctx); err != nil {
		t.Fatal(err)
	}
	if err := svc.Codex().EnsureCodexDeviceAccountReconciled(ctx); err != nil {
		t.Fatal(err)
	}
	if before, err := svc.Probe(ctx, "codex"); err != nil || before.Agent.AuthStatus != ports.AgentAuthStatusUnauthorized {
		t.Fatalf("pre-login probe = %#v, %v", before, err)
	}
	credential, _ := json.Marshal(map[string]any{"tokens": map[string]string{"account_id": "provider-account", "access_token": "access", "refresh_token": "refresh-access"}})
	if err := os.WriteFile(filepath.Join(globalHome, "auth.json"), credential, 0o600); err != nil {
		t.Fatal(err)
	}
	if after, err := svc.Probe(ctx, "codex"); err != nil || after.Agent.AuthStatus != ports.AgentAuthStatusAuthorized {
		t.Fatalf("post-login probe = %#v, %v; want authorized without restart", after, err)
	}
	if view, err := svc.Codex().CachedCodexAccounts(ctx); err != nil || view.ActiveAccountID == "" {
		t.Fatalf("post-login probe did not import the newly signed-in account: %#v, %v", view, err)
	}
	readiness, err := svc.EnsureAgentReadiness(ctx, "codex", domain.AgentReadinessPurposeDisplay)
	if err != nil || readiness.Authentication.State != domain.AgentAuthenticationAuthorized {
		t.Fatalf("post-login Harness readiness = %#v, %v; want authorized", readiness.Authentication, err)
	}
}
