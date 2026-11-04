package sessionmanager

import (
	"context"
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type fakeGitHubTokens struct {
	token       string
	err         error
	gotRemote   string
	gotOverride string
	calls       int
}

func (f *fakeGitHubTokens) TokenForProject(_ context.Context, remote, override string) (string, error) {
	f.calls++
	f.gotRemote, f.gotOverride = remote, override
	return f.token, f.err
}

func githubEnvManager(t *testing.T, tokens GitHubAccountTokens) *Manager {
	t.Helper()
	for _, key := range explicitGitHubTokenEnv {
		t.Setenv(key, "")
	}
	m, st, _, _ := newManager()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "git@github-xyz:acme/app.git", Config: domain.ProjectConfig{GitHubUser: "acme-bot"}}
	m.SetGitHubAccounts(tokens)
	return m
}

// The picked account's token reaches the session env as GH_TOKEN, resolved from
// the project's own remote and override.
func TestRuntimeEnvInjectsPickedGitHubAccountToken(t *testing.T) {
	f := &fakeGitHubTokens{token: "picked-token"}
	m := githubEnvManager(t, f)
	env := m.runtimeEnv("mer-1", "mer", "", nil)
	if env["GH_TOKEN"] != "picked-token" {
		t.Fatalf("GH_TOKEN not injected for the picked account")
	}
	if f.gotRemote != "git@github-xyz:acme/app.git" || f.gotOverride != "acme-bot" {
		t.Fatalf("resolved with remote %q override %q", f.gotRemote, f.gotOverride)
	}
}

// An explicit token (project env or daemon env) wins and skips the lookup.
func TestRuntimeEnvKeepsExplicitGitHubToken(t *testing.T) {
	f := &fakeGitHubTokens{token: "picked-token"}
	m := githubEnvManager(t, f)
	env := m.runtimeEnv("mer-1", "mer", "", map[string]string{"GH_TOKEN": "explicit"})
	if env["GH_TOKEN"] != "explicit" || f.calls != 0 {
		t.Fatalf("project-env GH_TOKEN must win without a lookup (calls=%d)", f.calls)
	}
	// An ambient daemon GH_TOKEN/GITHUB_TOKEN (inherited from an install run in an
	// agent session) must not stop the project's account being picked.
	t.Setenv("GITHUB_TOKEN", "from-daemon")
	t.Setenv("GH_TOKEN", "from-daemon")
	env = m.runtimeEnv("mer-1", "mer", "", nil)
	if env["GH_TOKEN"] == "" || env["GH_TOKEN"] == "from-daemon" || f.calls == 0 {
		t.Fatalf("ambient daemon token must not bypass auto-pick: %q (calls=%d)", env["GH_TOKEN"], f.calls)
	}
	f.calls = 0
	t.Setenv("TALLPINE_GITHUB_TOKEN", "pinned")
	m.runtimeEnv("mer-1", "mer", "", nil)
	if f.calls != 0 {
		t.Fatalf("daemon-env TALLPINE_GITHUB_TOKEN must bypass auto-pick (calls=%d)", f.calls)
	}
}

// A failed or empty lookup leaves the env untouched.
func TestRuntimeEnvGitHubAccountFailureLeavesEnvAlone(t *testing.T) {
	for name, f := range map[string]*fakeGitHubTokens{
		"error": {err: errors.New("gh broke")},
		"empty": {},
	} {
		env := githubEnvManager(t, f).runtimeEnv("mer-1", "mer", "", nil)
		if _, ok := env["GH_TOKEN"]; ok {
			t.Errorf("%s: GH_TOKEN set", name)
		}
	}
}
