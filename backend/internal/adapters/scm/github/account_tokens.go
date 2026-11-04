package github

import (
	"context"
	"net/http"
	"strings"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// AccountTokens routes a repository to the signed-in gh account that can push
// to it, so the observer reads each project's PRs as that account. A nil
// AccountTokens keeps the single global token. Implementations hold logins and
// fetch tokens on demand; the adapter never logs or stores them.
type AccountTokens interface {
	// LoginForRepo returns the account login for owner/name, or "" for the
	// default token.
	LoginForRepo(ctx context.Context, owner, name string) string
	// TokenForLogin returns that account's token.
	TokenForLogin(ctx context.Context, login string) (string, error)
}

// accountTokenInvalidator is the optional capability of dropping an account's
// memoized token after an auth failure, so a rotated token is picked up at once.
type accountTokenInvalidator interface {
	InvalidateLogin(login string)
}

type accountLoginKey struct{}

func withAccountLogin(ctx context.Context, login string) context.Context {
	return context.WithValue(ctx, accountLoginKey{}, login)
}

// repoCtx pins ctx to the account for owner/name; GraphQL requests carry no
// repo in their URL, so they rely on this.
func (p *Provider) repoCtx(ctx context.Context, host, owner, name string) context.Context {
	if p.client.accounts == nil || canonicalHost(host) != "" {
		return ctx
	}
	return withAccountLogin(ctx, p.client.accounts.LoginForRepo(ctx, owner, name))
}

// accountToken returns the per-repo account token for req. The login comes from
// ctx (GraphQL) or from the REST path /repos/{owner}/{name}/...; ok is false
// when the default token applies.
func (c *Client) accountToken(ctx context.Context, req *http.Request) (string, bool) {
	if c.accounts == nil {
		return "", false
	}
	login, pinned := ctx.Value(accountLoginKey{}).(string)
	if !pinned {
		owner, name, found := repoFromPath(req.URL.Path)
		if !found {
			return "", false
		}
		login = c.accounts.LoginForRepo(ctx, owner, name)
	}
	if login == "" {
		return "", false
	}
	token, err := c.accounts.TokenForLogin(ctx, login)
	if err != nil || strings.TrimSpace(token) == "" {
		return "", false
	}
	return token, true
}

// invalidateAccountToken drops the memoized token of the account that served a
// failed request. The login comes from ctx (GraphQL) or the REST path.
func (c *Client) invalidateAccountToken(ctx context.Context, path string) {
	inv, ok := c.accounts.(accountTokenInvalidator)
	if !ok {
		return
	}
	login, pinned := ctx.Value(accountLoginKey{}).(string)
	if !pinned {
		owner, name, found := repoFromPath(path)
		if !found {
			return
		}
		login = c.accounts.LoginForRepo(ctx, owner, name)
	}
	if login != "" {
		inv.InvalidateLogin(login)
	}
}

func repoFromPath(path string) (owner, name string, ok bool) {
	_, rest, found := strings.Cut(path, "/repos/")
	if !found {
		return "", "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// FetchPullRequests batches refs per account: one GraphQL request carries one
// token, and refs from different repos may need different accounts. Results
// stay positionally aligned with refs.
func (p *Provider) FetchPullRequests(ctx context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	type group struct {
		login string
		host  string
		idx   []int
	}
	var groups []*group
	byLogin := map[string]*group{}
	for i, ref := range refs {
		host := canonicalHost(ref.Repo.Host)
		login := ""
		if host == "" && p.client.accounts != nil {
			login = p.client.accounts.LoginForRepo(ctx, ref.Repo.Owner, ref.Repo.Name)
		}
		key := host + "|" + login
		g := byLogin[key]
		if g == nil {
			g = &group{login: login, host: host}
			byLogin[key] = g
			groups = append(groups, g)
		}
		g.idx = append(g.idx, i)
	}
	if len(groups) == 1 {
		return p.fetchPullRequestsBatch(p.groupCtx(ctx, groups[0].host, groups[0].login), refs)
	}
	out := make([]ports.SCMObservation, len(refs))
	for _, g := range groups {
		sub := make([]ports.SCMPRRef, len(g.idx))
		for j, i := range g.idx {
			sub[j] = refs[i]
		}
		obs, err := p.fetchPullRequestsBatch(p.groupCtx(ctx, g.host, g.login), sub)
		if err != nil {
			if g.host == "" {
				return nil, err
			}
			// An enterprise host's failure (no token, unlisted host) must not
			// hide github.com results: mark only its refs unfetched.
			obs = make([]ports.SCMObservation, len(sub))
			for j, ref := range sub {
				obs[j] = ports.SCMObservation{
					Provider: ref.Repo.Provider, Host: ref.Repo.Host, Repo: repoFullName(ref.Repo),
					PR:    ports.SCMPRObservation{Number: ref.Number, URL: ref.URL},
					Error: err,
				}
			}
		}
		for j, i := range g.idx {
			out[i] = obs[j]
		}
	}
	return out, nil
}

// groupCtx pins a batch to its account; enterprise hosts and account-less
// providers keep the plain context (their client has one token).
func (p *Provider) groupCtx(ctx context.Context, host, login string) context.Context {
	if host != "" || p.client.accounts == nil {
		return ctx
	}
	return withAccountLogin(ctx, login)
}
