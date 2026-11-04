package ghaccount

import (
	"context"
	"testing"
)

func TestInspectStatuses(t *testing.T) {
	repo := Repo{Owner: "o", Name: "r"}
	cases := []struct {
		name     string
		login    string
		identity string
		allow    bool
		noToken  bool
		want     string
		tokenAs  string
	}{
		{"ok", "a", "a", true, false, StatusOK, "a"},
		{"case-insensitive", "Acme", "acme", true, false, StatusOK, "acme"},
		{"mismatch", "a", "b", true, false, StatusMismatch, "b"},
		{"no repo access", "a", "a", false, false, StatusNoRepoAccess, "a"},
		{"no token", "a", "a", true, true, StatusNoToken, ""},
		{"no account", "", "", false, false, StatusNoAccount, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			allow := map[string][]string{}
			if c.allow {
				allow["tok-"+c.login] = []string{repo.Key()}
			}
			r, gh, _, _ := newTestResolver([]string{c.login}, allow)
			if c.noToken {
				gh.tokens[c.login] = ""
			}
			r.Identity = fakeIdentity{login: c.identity}
			got := r.Inspect(context.Background(), c.login, repo)
			if got.Status != c.want || got.TokenLogin != c.tokenAs {
				t.Fatalf("got %+v, want status %s tokenLogin %s", got, c.want, c.tokenAs)
			}
		})
	}
}
