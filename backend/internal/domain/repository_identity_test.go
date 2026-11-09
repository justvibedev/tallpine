package domain

import "testing"

func TestCanonicalRepositoryValidation(t *testing.T) {
	for _, tc := range []struct {
		name, origin, canonical string
		valid                   bool
	}{
		{"legacy", "", "", true},
		{"authenticated checkout origin", "https://git-user:token@github.com/alice/repo.git", "https://github.com/acme/repo", true},
		{"github fork", "git@github.com:alice/repo.git", "https://github.com/acme/repo", true},
		{"gitlab nested", "ssh://git@gitlab.example.com/alice/team/repo.git", "https://gitlab.example.com/group/subgroup/repo", true},
		{"moved from bitbucket", "git@bitbucket.org:acme/repo.git", "https://github.com/acme/repo", true},
		{"cross host", "https://gitlab.com/alice/repo", "https://gitlab.example.com/group/repo", true},
		{"cross provider", "https://github.com/alice/repo", "https://gitlab.com/group/repo", true},
		{"no origin", "", "https://github.com/acme/repo", false},
		{"remote name", "https://github.com/alice/repo", "upstream", false},
		{"PR URL", "https://github.com/alice/repo", "https://github.com/acme/repo/pull/7", false},
		{"MR URL", "https://gitlab.com/alice/repo", "https://gitlab.com/acme/repo/-/merge_requests/7", false},
		{"query", "https://github.com/alice/repo", "https://github.com/acme/repo?x=y", false},
		{"credentials", "https://github.com/alice/repo", "https://user:secret@github.com/acme/repo", false},
		{"traversal", "https://gitlab.com/alice/repo", "https://gitlab.com/acme/../repo", false},
		{"encoded namespace", "https://gitlab.com/alice/repo", "https://gitlab.com/acme%2fsub/repo", false},
		{"custom authority", "https://gitlab.example.com:8443/alice/repo", "https://gitlab.example.com:8443/group/sub/repo", true},
		{"port", "https://github.com/alice/repo", "https://github.com:8443/acme/repo", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := ProjectConfig{CanonicalRepoURL: tc.canonical}
			if err := cfg.ValidateCanonicalRepository(tc.origin); (err == nil) != tc.valid {
				t.Fatalf("validation = %v, valid=%v", err, tc.valid)
			}
		})
	}
}

func TestSameRepoPathTreatsRenamedRepoAsOne(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"justvibedev/tallpine-dev", "justvibedev/tallpine-dev", true},
		{"JustVibeDev/Tallpine-Dev.git", "justvibedev/tallpine-dev", true},
		{"justvibedev/tallpine-dev", "justvibedev/tallpine-dev", true},
		{"justvibedev/tallpine-dev", "justvibedev/other", false},
		{"other/aox", "justvibedev/tallpine-dev", false},
	} {
		if got := SameRepoPath(tc.a, tc.b); got != tc.want {
			t.Errorf("SameRepoPath(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestIsLocalPathRemote(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]bool{
		"": false, "/srv/git/proj.git": true, "./proj": true, "../proj.git": true, "~/proj": true,
		"file:///srv/proj.git": true, "FILE:///srv/proj.git": true, `C:\repos\proj`: true,
		"https://github.com/a/b.git": false, "ssh://git@host/a/b.git": false,
		"git@github.com:a/b.git": false, "host:a/b.git": false,
	} {
		if got := IsLocalPathRemote(raw); got != want {
			t.Errorf("IsLocalPathRemote(%q)=%v want %v", raw, got, want)
		}
	}
}

func TestCredentialRepoURLOnlyFollowsGitHubHosts(t *testing.T) {
	origin := "git@github.com:acme/app.git"
	if got := (ProjectConfig{CanonicalRepoURL: "https://gitlab.com/acme/app"}).CredentialRepoURL(origin); got != origin {
		t.Fatalf("non-GitHub canonical host widened credential scope: %q", got)
	}
	if got := (ProjectConfig{CanonicalRepoURL: "https://github.com/acme/app"}).CredentialRepoURL("git@bitbucket.org:acme/app.git"); got != "https://github.com/acme/app" {
		t.Fatalf("moved GitHub canonical not followed: %q", got)
	}
}

func TestTrackedRepoURL(t *testing.T) {
	for _, tc := range []struct{ name, origin, canonical, want string }{
		{"no canonical", "git@bitbucket.org:a/r.git", "", "git@bitbucket.org:a/r.git"},
		{"same host fork keeps origin", "git@github.com:me/r.git", "https://github.com/acme/r", "git@github.com:me/r.git"},
		{"moved host uses canonical", "git@bitbucket.org:a/r.git", "https://github.com/a/r", "https://github.com/a/r"},
		{"unparseable canonical keeps origin", "git@bitbucket.org:a/r.git", "upstream", "git@bitbucket.org:a/r.git"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := (ProjectConfig{CanonicalRepoURL: tc.canonical}).TrackedRepoURL(tc.origin); got != tc.want {
				t.Fatalf("TrackedRepoURL = %q, want %q", got, tc.want)
			}
		})
	}
}

// A registered GHES host on a custom port is a valid canonical URL (only
// github.com and ghe.com reject a port).
func TestCanonicalRepositoryAcceptsRegisteredGHESPort(t *testing.T) {
	SetGitHubEnterpriseHosts([]string{"ghes.corp.example", "ghes.corp.example:8443"})
	t.Cleanup(func() { SetGitHubEnterpriseHosts(nil) })
	cfg := ProjectConfig{CanonicalRepoURL: "https://ghes.corp.example:8443/up/r"}
	if err := cfg.ValidateCanonicalRepository("https://ghes.corp.example:8443/me/r"); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialRemoteForHost(t *testing.T) {
	for _, tc := range []struct{ name, origin, canonical, host, want string }{
		{"github.com origin, ghe canonical: origin host keeps origin", "git@github.com:old/app.git", "https://acme.ghe.com/new/app", "github.com", "git@github.com:old/app.git"},
		{"github.com origin, ghe canonical: canonical host uses canonical", "git@github.com:old/app.git", "https://acme.ghe.com/new/app", "acme.ghe.com", "https://acme.ghe.com/new/app"},
		{"bitbucket origin moved: github.com uses canonical", "https://bitbucket.org/a/app.git", "https://github.com/a/app", "github.com", "https://github.com/a/app"},
		{"bitbucket origin moved: bitbucket host keeps origin", "https://bitbucket.org/a/app.git", "https://github.com/a/app", "bitbucket.org", "https://bitbucket.org/a/app.git"},
		{"no canonical", "git@github.com:a/app.git", "", "github.com", "git@github.com:a/app.git"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := (ProjectConfig{CanonicalRepoURL: tc.canonical}).CredentialRemoteForHost(tc.origin, tc.host); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAccountRemoteForHostRefusesNonGitHubOriginOnAMovedProject(t *testing.T) {
	moved := ProjectConfig{CanonicalRepoURL: "https://github.com/a/app"}
	if _, ok := moved.AccountRemoteForHost("https://bitbucket.org/a/app.git", "bitbucket.org"); ok {
		t.Fatal("a moved project's non-GitHub origin host must resolve no account")
	}
	if r, ok := moved.AccountRemoteForHost("https://bitbucket.org/a/app.git", "github.com"); !ok || r != "https://github.com/a/app" {
		t.Fatalf("canonical host: %q %v", r, ok)
	}
	// A plain project (no canonical) keeps override-driven behaviour for a non-GitHub origin.
	if _, ok := (ProjectConfig{}).AccountRemoteForHost("git@ghes.example:a/app.git", "ghes.example"); !ok {
		t.Fatal("unmoved projects must be unchanged")
	}
}
