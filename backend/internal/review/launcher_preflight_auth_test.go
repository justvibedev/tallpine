package review

import (
	"context"
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type recordingLaunchAuth struct {
	status ports.AgentAuthStatus
	err    error
	calls  int
	env    map[string]string
	dir    string
}

func (r *recordingLaunchAuth) ValidateLaunchAuth(_ context.Context, dir string, env map[string]string) (ports.AgentAuthStatus, error) {
	r.calls++
	r.dir, r.env = dir, env
	return r.status, r.err
}

func preflightLauncher(accounts ports.AgentAccountProviderEnv, device ports.AgentAuthStatus, launch ReviewerLaunchAuth) Launcher {
	reviewer := &fakeReviewerForPreflight{Argv: []string{"go"}}
	return NewLauncher(fakeReviewerResolver{reviewer: reviewer, ok: true}, &fakeRuntime{}, "",
		WithAgentAuth(fakeAgentAuthResolver{status: device, ok: true}),
		WithAgentAccounts(accounts), WithReviewerLaunchAuth(launch))
}

func TestPreflightChecksWorkerAccountNotDeviceLogin(t *testing.T) {
	home := t.TempDir()
	launch := &recordingLaunchAuth{status: ports.AgentAuthStatusAuthorized}
	l := preflightLauncher(&fakeAccountEnv{env: accountBEnv(home)}, ports.AgentAuthStatusUnauthorized, launch)
	if err := l.Preflight(context.Background(), "mer-1", domain.ReviewerClaudeCode, "/ws/mer-1"); err != nil {
		t.Fatalf("Preflight: %v (device login is signed out, account B is authorized)", err)
	}
	if launch.calls != 1 || launch.env["CLAUDE_CONFIG_DIR"] != home || launch.dir != "/ws/mer-1" {
		t.Fatalf("validator calls=%d dir=%q env=%v", launch.calls, launch.dir, launch.env)
	}
}

func TestPreflightRefusesWhenWorkerAccountIsSignedOut(t *testing.T) {
	launch := &recordingLaunchAuth{status: ports.AgentAuthStatusUnauthorized}
	l := preflightLauncher(&fakeAccountEnv{env: accountBEnv(t.TempDir())}, ports.AgentAuthStatusAuthorized, launch)
	if err := l.Preflight(context.Background(), "mer-1", domain.ReviewerClaudeCode, "/ws/mer-1"); !errors.Is(err, ports.ErrChatAuthRequired) {
		t.Fatalf("err = %v, want ErrChatAuthRequired (device login is fine, account B is signed out)", err)
	}
}

func TestPreflightDeviceBoundWorkerKeepsDevicePath(t *testing.T) {
	launch := &recordingLaunchAuth{status: ports.AgentAuthStatusAuthorized}
	l := preflightLauncher(&fakeAccountEnv{env: map[string]string{}}, ports.AgentAuthStatusUnauthorized, launch)
	if err := l.Preflight(context.Background(), "mer-1", domain.ReviewerClaudeCode, "/ws/mer-1"); !errors.Is(err, ports.ErrChatAuthRequired) {
		t.Fatalf("err = %v, want the device verdict", err)
	}
	if launch.calls != 0 {
		t.Fatalf("validator called %d times for a device-bound worker", launch.calls)
	}
}

func TestPreflightInconclusiveAccountProbeDoesNotBlock(t *testing.T) {
	launch := &recordingLaunchAuth{err: errors.New("cli timed out")}
	l := preflightLauncher(&fakeAccountEnv{env: accountBEnv(t.TempDir())}, ports.AgentAuthStatusUnauthorized, launch)
	if err := l.Preflight(context.Background(), "mer-1", domain.ReviewerClaudeCode, "/ws/mer-1"); err != nil {
		t.Fatalf("Preflight: %v", err)
	}
}

func TestPreflightAccountLookupErrorFails(t *testing.T) {
	boom := errors.New("accounts down")
	l := preflightLauncher(&fakeAccountEnv{err: boom}, ports.AgentAuthStatusAuthorized, &recordingLaunchAuth{})
	if err := l.Preflight(context.Background(), "mer-1", domain.ReviewerClaudeCode, "/ws/mer-1"); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
}

func TestReviewerDeviceBoundClaudeIgnoresBrokenCodexAccount(t *testing.T) {
	src := &fakeAccountEnv{env: map[string]string{}, errFor: map[domain.AgentAccountProvider]error{
		domain.AgentAccountProviderCodex: errors.New("codex account failed to prepare"),
	}}
	_, rt, err := spawnWithAccounts(t, src, launchSpec())
	if err != nil {
		t.Fatalf("Spawn: %v (a Codex failure must not fail a device-bound Claude reviewer)", err)
	}
	if _, ok := rt.createCfg.Env["CLAUDE_CONFIG_DIR"]; ok {
		t.Fatalf("device-bound worker got account env: %v", rt.createCfg.Env)
	}
	for _, p := range src.providers {
		if p != domain.AgentAccountProviderClaude {
			t.Fatalf("lookup asked for %s", p)
		}
	}
}
