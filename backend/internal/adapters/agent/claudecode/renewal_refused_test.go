package claudecode

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
)

func resetLoginRefusals(t *testing.T) {
	t.Helper()
	reset := func() {
		claudeLoginRefusals.mu.Lock()
		claudeLoginRefusals.at = nil
		claudeLoginRefusals.mu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

func probeStatus(t *testing.T, resolved claudeProviderContext) ports.AgentAuthStatus {
	t.Helper()
	status, ok := (&Plugin{}).probeResolvedAuthStatus(context.Background(), resolved, true)
	if !ok {
		t.Fatal("probe gave no verdict")
	}
	return status
}

// B520: once the provider refused an expired login (Claude Code failed to
// renew it), readiness must say signed out, as launch does, and not
// "configured". The refusal belongs to that credential only: another
// account's expired login may still renew, so it stays configured.
func TestExpiredLoginRefusedByProviderReadsUnauthorized(t *testing.T) {
	InvalidateAuthCache()
	t.Cleanup(InvalidateAuthCache)
	resetLoginRefusals(t)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	pinClaudeNow(t, now)
	server := withStubValidator(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	})
	accountA := storedLoginContext(now.Add(-time.Hour), true, server.URL)
	accountB := storedLoginContext(now.Add(-time.Hour), true, server.URL)
	accountB.credential.Secret = "sk-ant-oat01-other-account"
	accountB.credential.Source = "config-dir-b"

	noteLoginRefused(accountA)
	if got := probeStatus(t, accountA); got != ports.AgentAuthStatusUnauthorized {
		t.Fatalf("refused account status = %q, want unauthorized", got)
	}
	if got := probeStatus(t, accountB); got != ports.AgentAuthStatusConfigured {
		t.Fatalf("other account status = %q, want configured: its login may still renew", got)
	}

	// A refusal of a login that had not yet expired says nothing about renewal.
	resetLoginRefusals(t)
	fresh := storedLoginContext(now.Add(time.Minute), true, server.URL)
	noteLoginRefused(fresh)
	pinClaudeNow(t, now.Add(2*time.Minute))
	if got := probeStatus(t, fresh); got != ports.AgentAuthStatusConfigured {
		t.Fatalf("status = %q, want configured for a login refused before it expired", got)
	}
}

func writeExpiredLogin(t *testing.T, dir, token string, expiresAt time.Time) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	login := fmt.Sprintf(`{"claudeAiOauth":{"accessToken":%q,"refreshToken":"sk-ant-ort01-%s","expiresAt":%d}}`, token, token, expiresAt.UnixMilli())
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(login), 0o600); err != nil {
		t.Fatal(err)
	}
}

// B524: the runtime handler resolves the refused launch's own environment, so
// the refusal lands on that account alone. An isolated daemon's "This computer"
// launch sets no CLAUDE_CONFIG_DIR at all (a set one would move Claude's
// Keychain entry), so there the clear state is signed out, and it must not
// spread to a named account or the reverse.
func TestProviderRefusalStaysOnTheRefusedLaunchAccount(t *testing.T) {
	noKeychain(t)
	InvalidateAuthCache()
	t.Cleanup(InvalidateAuthCache)
	resetLoginRefusals(t)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	pinClaudeNow(t, now)
	previous := claudeModelAuthReport
	claudeModelAuthReport = func(context.Context, string, string, map[string]string) (claudeAuthReport, bool) {
		loggedIn := true
		return claudeAuthReport{LoggedIn: &loggedIn, APIProvider: "firstParty"}, true
	}
	t.Cleanup(func() { claudeModelAuthReport = previous })
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(claudeConfigDirEnv, "")
	workspace := t.TempDir()
	expired := now.Add(-time.Hour)
	writeExpiredLogin(t, filepath.Join(home, ".claude"), "sk-ant-oat01-device", expired)
	accountA, accountB := t.TempDir(), t.TempDir()
	writeExpiredLogin(t, accountA, "sk-ant-oat01-account-a", expired)
	writeExpiredLogin(t, accountB, "sk-ant-oat01-account-b", expired)
	device := map[string]string{}
	envA := map[string]string{claudeConfigDirEnv: accountA}
	envB := map[string]string{claudeConfigDirEnv: accountB}
	status := func(env map[string]string) ports.AgentAuthStatus {
		t.Helper()
		resolved := (&Plugin{}).resolveProviderContext(context.Background(), "/fixture/claude", workspace, env, claudeModelAuthReport)
		got, _, ok := expiredLoginStatus(resolved)
		if !ok {
			t.Fatalf("env %v did not resolve an expired renewable login: %+v", env, resolved.credential.Source)
		}
		return got
	}
	refuse := func(env map[string]string) {
		NoteProviderRefusedLogin(context.Background(), "/fixture/claude", workspace, env)
	}

	refuse(envA)
	if got := status(envA); got != ports.AgentAuthStatusUnauthorized {
		t.Fatalf("refused account = %q, want unauthorized", got)
	}
	if got := status(envB); got != ports.AgentAuthStatusConfigured {
		t.Fatalf("other account = %q, want configured: its login may still renew", got)
	}
	if got := status(device); got != ports.AgentAuthStatusConfigured {
		t.Fatalf("device login = %q, want configured after another account's refusal", got)
	}

	refuse(device)
	if got := status(device); got != ports.AgentAuthStatusUnauthorized {
		t.Fatalf("refused device login = %q, want unauthorized: an isolated daemon has no other needs-auth signal at launch", got)
	}
	if got := status(envB); got != ports.AgentAuthStatusConfigured {
		t.Fatalf("other account after device refusal = %q, want configured", got)
	}
}
