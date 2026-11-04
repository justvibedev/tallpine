package domain

import (
	"context"
	"errors"
	"testing"
)

func TestCanonicalRepoAcceptsSSHHostAlias(t *testing.T) {
	old := ResolveSSHHostAlias
	defer func() { ResolveSSHHostAlias = old }()
	ResolveSSHHostAlias = func(h string) string {
		if h == "github-work" {
			return "github.com"
		}
		return ""
	}
	c := ProjectConfig{CanonicalRepoURL: "https://github.com/acme/repo"}
	if err := c.ValidateCanonicalRepository("git@github-work:acme/repo"); err != nil {
		t.Fatal(err)
	}
	if err := c.ValidateCanonicalRepository("git@gitlab.example.com:o/r"); err == nil {
		t.Fatal("expected mismatch error")
	}
}

func TestParseRepositoryIdentityResolvesSSHAliasProvider(t *testing.T) {
	old := ResolveSSHHostAlias
	defer func() { ResolveSSHHostAlias = old }()
	ResolveSSHHostAlias = func(h string) string {
		if h == "github-work" {
			return "github.com"
		}
		return ""
	}
	for _, raw := range []string{"git@github-work:o/r.git", "ssh://git@github-work/o/r"} {
		id, err := ParseRepositoryIdentity(raw)
		if err != nil || id.Provider != "github" || id.Host != "github.com" || id.Namespace != "o" || id.Name != "r" {
			t.Fatalf("%s: %+v %v", raw, id, err)
		}
	}
	id, _ := ParseRepositoryIdentity("https://github-work/o/r")
	if id.Provider != "gitlab" {
		t.Fatalf("https hosts are never alias-resolved: %+v", id)
	}
	id, _ = ParseRepositoryIdentity("git@unknown-alias:o/r")
	if id.Host != "unknown-alias" {
		t.Fatalf("unresolvable alias must fall back: %+v", id)
	}
}

func TestSSHAliasLookupFailureIsNotCached(t *testing.T) {
	old := sshHostName
	defer func() { sshHostName = old }()
	const alias = "retry-alias-test"
	defer sshAliasCache.Delete(alias)
	calls := 0
	sshHostName = func(context.Context, string) (string, error) {
		calls++
		if calls == 1 {
			return "", errors.New("timeout")
		}
		return "github.com", nil
	}
	if got := resolveSSHHostAliasViaSSH(alias); got != "" || calls != 1 {
		t.Fatalf("first: %q calls=%d", got, calls)
	}
	if got := resolveSSHHostAliasViaSSH(alias); got != "github.com" || calls != 2 {
		t.Fatalf("second must retry: %q calls=%d", got, calls)
	}
	if got := resolveSSHHostAliasViaSSH(alias); got != "github.com" || calls != 2 {
		t.Fatalf("third must be cached: %q calls=%d", got, calls)
	}
}
