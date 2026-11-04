package agentaccounts

import (
	"context"
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// fakeCodex is a second provider whose account lookup the store can break.
type fakeCodex struct{ *fakeClaude }

func (fakeCodex) Provider() domain.AgentAccountProvider { return domain.AgentAccountProviderCodex }

// codexBrokenStore fails every Codex binding lookup.
type codexBrokenStore struct{ *memStore }

var errCodexLookup = errors.New("codex account lookup failed")

func (s codexBrokenStore) GetSessionAgentAccount(ctx context.Context, id domain.SessionID, p domain.AgentAccountProvider) (string, bool, error) {
	if p == domain.AgentAccountProviderCodex {
		return "", false, errCodexLookup
	}
	return s.memStore.GetSessionAgentAccount(ctx, id, p)
}

func TestSessionLaunchEnvForIsolatesProviders(t *testing.T) {
	ctx := context.Background()
	claude := &fakeClaude{signedInAs: map[string]string{"": "a@example.com"}}
	store := codexBrokenStore{&memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}}
	svc := New(store, []ports.AgentAccountProviderCLI{claude, fakeCodex{claude}}, t.TempDir(), fakeTerminals{}, nil)

	// A device-bound Claude worker: no env and no error, whatever Codex does.
	env, err := svc.SessionLaunchEnvFor(ctx, "s1", domain.AgentAccountProviderClaude)
	if err != nil || env["CLAUDE_CONFIG_DIR"] != "" {
		t.Fatalf("Claude lookup = %v, %v; want empty and no error", env, err)
	}
	if _, err := svc.SessionLaunchEnvFor(ctx, "s1", domain.AgentAccountProviderCodex); !errors.Is(err, errCodexLookup) {
		t.Fatalf("Codex lookup err = %v, want %v", err, errCodexLookup)
	}
	// The all-provider lookup still reports every provider's failure.
	if _, err := svc.SessionLaunchEnv(ctx, "s1"); !errors.Is(err, errCodexLookup) {
		t.Fatalf("SessionLaunchEnv err = %v, want %v", err, errCodexLookup)
	}
}

func TestSessionLaunchEnvForReturnsTheBoundAccountEnv(t *testing.T) {
	ctx, root, claude, _, svc := newDeviceAccounts(t)
	start, _ := svc.StartLogin(ctx, domain.AgentAccountProviderClaude)
	home := root + "/claude/" + start.OperationID
	claude.signedInAs[home] = "b@example.com"
	if status, _, err := svc.VerifyLogin(ctx, start.OperationID); err != nil || status != LoginComplete {
		t.Fatalf("login = %q, %v", status, err)
	}
	if err := svc.BindSession(ctx, "s1", domain.HarnessClaudeCode, start.OperationID); err != nil {
		t.Fatal(err)
	}
	env, err := svc.SessionLaunchEnvFor(ctx, "s1", domain.AgentAccountProviderClaude)
	if err != nil || env["CLAUDE_CONFIG_DIR"] != home {
		t.Fatalf("env = %v, %v; want account home %s", env, err, home)
	}
}

// A device-account session still gets the provider's device env, so an
// inherited CLAUDE_CONFIG_DIR cannot move it onto another account's login.
func TestSessionLaunchEnvForDeviceAccountClearsInheritedHome(t *testing.T) {
	ctx := context.Background()
	claude := &fakeClaude{signedInAs: map[string]string{"": "a@example.com"}}
	store := &memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}
	svc := New(store, []ports.AgentAccountProviderCLI{claude}, t.TempDir(), fakeTerminals{}, nil)
	env, err := svc.SessionLaunchEnvFor(ctx, "s1", domain.AgentAccountProviderClaude)
	if v, set := env["CLAUDE_CONFIG_DIR"]; err != nil || !set || v != "" {
		t.Fatalf("env = %v, %v; want CLAUDE_CONFIG_DIR cleared", env, err)
	}
}

// newSessionEnvFixture signs in a home account and makes it the default.
func newSessionEnvFixture(t *testing.T) (*Service, *fakeClaude, string) {
	t.Helper()
	ctx, root, claude, _, svc := newDeviceAccounts(t)
	start, err := svc.StartLogin(ctx, domain.AgentAccountProviderClaude)
	if err != nil {
		t.Fatal(err)
	}
	home := root + "/claude/" + start.OperationID
	claude.signedInAs[home] = "b@example.com"
	if status, _, err := svc.VerifyLogin(ctx, start.OperationID); err != nil || status != LoginComplete {
		t.Fatalf("login = %q, %v", status, err)
	}
	if err := svc.SetDefault(ctx, start.OperationID); err != nil {
		t.Fatal(err)
	}
	claude.prepared.Store(0)
	return svc, claude, home
}

func TestNewSessionLaunchEnvDeviceAccountClearsHome(t *testing.T) {
	ctx := context.Background()
	claude := &fakeClaude{signedInAs: map[string]string{"": "a@example.com"}}
	store := &memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}
	svc := New(store, []ports.AgentAccountProviderCLI{claude}, t.TempDir(), fakeTerminals{}, nil)
	env, err := svc.NewSessionLaunchEnv(ctx, domain.HarnessClaudeCode)
	if v, set := env["CLAUDE_CONFIG_DIR"]; err != nil || !set || v != "" {
		t.Fatalf("env = %v, %v; want CLAUDE_CONFIG_DIR cleared", env, err)
	}
	if claude.prepared.Load() != 0 {
		t.Fatal("device account must not be prepared")
	}
}

func TestNewSessionLaunchEnvHomeAccountPreparesAndReturnsHome(t *testing.T) {
	svc, claude, home := newSessionEnvFixture(t)
	env, err := svc.NewSessionLaunchEnv(context.Background(), domain.HarnessClaudeCode)
	if err != nil || env["CLAUDE_CONFIG_DIR"] != home {
		t.Fatalf("env = %v, %v; want home %s", env, err, home)
	}
	if claude.prepared.Load() != 1 {
		t.Fatalf("prepare calls = %d, want 1", claude.prepared.Load())
	}
}

func TestNewSessionLaunchEnvReadOnlyDoesNotPrepare(t *testing.T) {
	svc, claude, home := newSessionEnvFixture(t)
	env, err := svc.NewSessionLaunchEnvReadOnly(context.Background(), domain.HarnessClaudeCode)
	if err != nil || env["CLAUDE_CONFIG_DIR"] != home {
		t.Fatalf("env = %v, %v; want home %s", env, err, home)
	}
	if claude.prepared.Load() != 0 {
		t.Fatalf("prepare calls = %d, want 0 on the read path", claude.prepared.Load())
	}
}

func TestNewSessionLaunchEnvFallsBackWhenDefaultIsUnusable(t *testing.T) {
	ctx := context.Background()
	svc, claude, home := newSessionEnvFixture(t)
	// Cache a reading of the default, then sign it out so it is known unusable.
	if _, err := svc.List(ctx, true); err != nil {
		t.Fatal(err)
	}
	delete(claude.signedInAs, home)
	if _, err := svc.List(ctx, true); err != nil {
		t.Fatal(err)
	}
	env, err := svc.NewSessionLaunchEnv(ctx, domain.HarnessClaudeCode)
	if v, set := env["CLAUDE_CONFIG_DIR"]; err != nil || !set || v != "" {
		t.Fatalf("env = %v, %v; want the signed-in device account", env, err)
	}
}

func TestNewSessionLaunchEnvHarnessWithoutProvider(t *testing.T) {
	claude := &fakeClaude{signedInAs: map[string]string{"": "a@example.com"}}
	store := &memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}
	svc := New(store, []ports.AgentAccountProviderCLI{claude}, t.TempDir(), fakeTerminals{}, nil)
	env, err := svc.NewSessionLaunchEnv(context.Background(), domain.HarnessKiro)
	if env != nil || err != nil {
		t.Fatalf("env = %v, %v; want nil, nil", env, err)
	}
}

func TestNewSessionLaunchEnvProviderNotRegistered(t *testing.T) {
	store := &memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}
	svc := New(store, nil, t.TempDir(), fakeTerminals{}, nil)
	env, err := svc.NewSessionLaunchEnv(context.Background(), domain.HarnessClaudeCode)
	if env != nil || err != nil {
		t.Fatalf("env = %v, %v; want nil, nil", env, err)
	}
}
