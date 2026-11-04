package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
)

type fakeAccounts struct {
	logins      map[string]string // "owner/name" -> login
	invalidated *[]string
}

func (f fakeAccounts) InvalidateLogin(login string) {
	if f.invalidated != nil {
		*f.invalidated = append(*f.invalidated, login)
	}
}

func (f fakeAccounts) LoginForRepo(_ context.Context, owner, name string) string {
	return f.logins[owner+"/"+name]
}

func (f fakeAccounts) TokenForLogin(_ context.Context, login string) (string, error) {
	if login == "broken" {
		return "", errors.New("gh failed")
	}
	return "tkn-" + login, nil
}

func newAccountProvider(t *testing.T, f *fakeGH, accts AccountTokens) *Provider {
	t.Helper()
	p, err := NewProvider(ProviderOptions{
		Token:      StaticTokenSource("tkn-default"),
		Accounts:   accts,
		HTTPClient: f.server.Client(),
		RESTBase:   f.server.URL,
		GraphQLURL: f.server.URL + "/graphql",
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func repoRef(owner, name string, n int) ports.SCMPRRef {
	return ports.SCMPRRef{Repo: ports.SCMRepo{Provider: "github", Host: "github.com", Owner: owner, Name: name, Repo: owner + "/" + name}, Number: n}
}

// A mixed-repo batch must go out once per account, each with that account's
// token, and the observations must come back in the caller's order.
func TestFetchPullRequestsSplitsBatchPerAccount(t *testing.T) {
	fake := newFakeGH(t)
	fx := basePRFixture()
	var pr map[string]any
	fx.prData(func(m map[string]any) { pr = m })
	var tokens []string
	fake.on(http.MethodPost, "/graphql", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		tokens = append(tokens, auth)
		// alice owns refs 1 and 3, bob owns ref 2; echo each ref's own number.
		nums := map[string][]int{"Bearer tkn-alice": {1, 3}, "Bearer tkn-bob": {2}}[auth]
		data := map[string]any{}
		for i, n := range nums {
			c := map[string]any{}
			for k, v := range pr {
				c[k] = v
			}
			c["number"] = n
			data["pr"+string(rune('0'+i))] = map[string]any{"pullRequest": c}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	})
	p := newAccountProvider(t, fake, fakeAccounts{logins: map[string]string{"alice/a": "alice", "bob/b": "bob"}})

	obs, err := p.FetchPullRequests(ctx(), []ports.SCMPRRef{repoRef("alice", "a", 1), repoRef("bob", "b", 2), repoRef("alice", "a", 3)})
	if err != nil {
		t.Fatalf("FetchPullRequests: %v", err)
	}
	if len(tokens) != 2 {
		t.Fatalf("graphql calls = %v, want one per account", tokens)
	}
	for i, want := range []int{1, 2, 3} {
		if !obs[i].Fetched || obs[i].PR.Number != want {
			t.Fatalf("obs[%d] = %+v, want PR %d in caller order", i, obs[i].PR, want)
		}
	}
}

// With no AccountTokens the original single-request path runs unchanged.
func TestFetchPullRequestsWithoutAccountsIsOneRequest(t *testing.T) {
	fake := newFakeGH(t)
	fx := basePRFixture()
	var pr map[string]any
	fx.prData(func(m map[string]any) { pr = m })
	fake.on(http.MethodPost, "/graphql", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tkn-default" {
			t.Errorf("auth = %q, want default token", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"pr0": map[string]any{"pullRequest": pr}, "pr1": map[string]any{"pullRequest": pr},
		}})
	})
	p := newAccountProvider(t, fake, nil)
	if _, err := p.FetchPullRequests(ctx(), []ports.SCMPRRef{repoRef("alice", "a", 1), repoRef("bob", "b", 2)}); err != nil {
		t.Fatal(err)
	}
	if n := fake.callsTo(http.MethodPost, "/graphql"); n != 1 {
		t.Fatalf("graphql calls = %d, want 1", n)
	}
}

// REST calls derive the repo from /repos/{owner}/{name}; unmapped repos and a
// failing token lookup both keep the default token.
func TestRESTUsesAccountTokenPerRepo(t *testing.T) {
	fake := newFakeGH(t)
	got := map[string]string{}
	for _, path := range []string{"/repos/bob/b/pulls", "/repos/plain/p/pulls", "/repos/bad/x/pulls"} {
		fake.on(http.MethodGet, path, func(w http.ResponseWriter, r *http.Request) {
			got[path] = r.Header.Get("Authorization")
			w.Header().Set("ETag", "x")
			_, _ = w.Write([]byte("[]"))
		})
	}
	p := newAccountProvider(t, fake, fakeAccounts{logins: map[string]string{"bob/b": "bob", "bad/x": "broken"}})
	for _, r := range [][2]string{{"bob", "b"}, {"plain", "p"}, {"bad", "x"}} {
		if _, err := p.RepoPRListGuard(ctx(), repoRef(r[0], r[1], 0).Repo, ""); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string]string{
		"/repos/bob/b/pulls": "Bearer tkn-bob", "/repos/plain/p/pulls": "Bearer tkn-default", "/repos/bad/x/pulls": "Bearer tkn-default",
	}
	for path, w := range want {
		if !strings.EqualFold(got[path], w) {
			t.Errorf("%s auth = %q, want %q", path, got[path], w)
		}
	}
}

// An auth failure on a per-account token drops that account's memoized token.
func TestAuthFailureInvalidatesAccountToken(t *testing.T) {
	fake := newFakeGH(t)
	fake.on(http.MethodGet, "/repos/bob/b/pulls", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
	})
	fake.on(http.MethodPost, "/graphql", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
	})
	var dropped []string
	p := newAccountProvider(t, fake, fakeAccounts{logins: map[string]string{"bob/b": "bob"}, invalidated: &dropped})
	if _, err := p.RepoPRListGuard(ctx(), repoRef("bob", "b", 0).Repo, ""); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("REST err = %v, want ErrAuthFailed", err)
	}
	if len(dropped) != 1 || dropped[0] != "bob" {
		t.Fatalf("REST 401 invalidated %v, want [bob]", dropped)
	}
	dropped = nil
	if _, err := p.FetchPullRequests(ctx(), []ports.SCMPRRef{repoRef("bob", "b", 1)}); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("GraphQL err = %v, want ErrAuthFailed", err)
	}
	if len(dropped) != 1 || dropped[0] != "bob" {
		t.Fatalf("GraphQL 401 invalidated %v, want [bob]", dropped)
	}
}
