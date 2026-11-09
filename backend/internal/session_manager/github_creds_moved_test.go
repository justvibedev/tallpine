package sessionmanager

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// remoteTokens resolves an account only from the remote it is given, like the
// real resolver: a token per remote, nothing for an unknown (non-GitHub) one.
type remoteTokens map[string]string

func (r remoteTokens) TokenForProject(_ context.Context, remote, override string) (string, error) {
	if override != "" { // the real resolver returns a non-empty override before parsing the remote
		return "override-token", nil
	}
	return r[remote], nil
}

// B592: on a moved project the real gh login file carries the canonical
// account only under the canonical host, the origin account only under the
// origin host, and no token at all under a host with no account.
func TestGHLoginIsScopedPerHostOnAMovedProject(t *testing.T) {
	const canonical = "https://github.com/acme/app"
	for _, tc := range []struct {
		name, origin, user string
		tokens             remoteTokens
		want               map[string]string // host -> token expected under it
		absent             []string
	}{
		{"GHES origin keeps its own account", "git@ghes.corp.example:acme/app.git", "",
			remoteTokens{canonical: "gh-token", "git@ghes.corp.example:acme/app.git": "ghes-token"},
			map[string]string{"github.com": "gh-token", "ghes.corp.example": "ghes-token"}, nil},
		{"Bitbucket origin gets no token", "https://bitbucket.org/acme/app.git", "",
			remoteTokens{canonical: "gh-token"},
			map[string]string{"github.com": "gh-token"}, []string{"bitbucket.org"}},
		{"Bitbucket origin gets no token even with a githubUser override", "https://bitbucket.org/acme/app.git", "work",
			remoteTokens{},
			map[string]string{"github.com": "override-token"}, []string{"bitbucket.org"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			domain.SetGitHubEnterpriseHosts([]string{"ghes.corp.example"})
			t.Cleanup(func() { domain.SetGitHubEnterpriseHosts(nil) })
			m := githubCredsManager(t, "helper", tc.tokens)
			m.store.(*fakeStore).projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: tc.origin, Config: domain.ProjectConfig{GitHubUser: tc.user, CanonicalRepoURL: canonical}}
			seedSessionHarness(t, m, "mer-1", domain.HarnessCodex)
			raw, err := os.ReadFile(filepath.Join(m.runtimeEnv("mer-1", "mer", "", nil)["GH_CONFIG_DIR"], "hosts.yml"))
			if err != nil {
				t.Fatal(err)
			}
			got := string(raw)
			for host, token := range tc.want {
				if !strings.Contains(got, host+":\n    oauth_token: '"+token+"'") {
					t.Errorf("%s must carry %q:\n%s", host, token, got)
				}
			}
			for _, host := range tc.absent {
				if strings.Contains(got, host) {
					t.Errorf("%s must get no token:\n%s", host, got)
				}
			}
			if n := strings.Count(got, "oauth_token:"); n != len(tc.want) {
				t.Errorf("expected %d host entries, got %d:\n%s", len(tc.want), n, got)
			}
		})
	}
}

// A github.com origin with a canonical URL on another GitHub host keeps resolving
// GH_TOKEN from the origin: ghaccount only understands github.com remotes.
func TestGHTokenEnvUsesOriginForGitHubComOriginWithGHECanonical(t *testing.T) {
	f := &fakeGitHubTokens{token: "origin-token"}
	m := githubEnvManager(t, f)
	const origin = "git@github.com:old/app.git"
	m.store.(*fakeStore).projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: origin, Config: domain.ProjectConfig{CanonicalRepoURL: "https://acme.ghe.com/new/app"}}
	env := m.runtimeEnv("mer-1", "mer", "", nil)
	if f.gotRemote != origin || env["GH_TOKEN"] != "origin-token" {
		t.Fatalf("remote=%q GH_TOKEN=%q", f.gotRemote, env["GH_TOKEN"])
	}
}
