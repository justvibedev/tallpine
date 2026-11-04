package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// AuthenticatedIdentity returns the account associated with the active GitHub
// token. Successful results are cached for the provider's lifetime.
func (p *Provider) AuthenticatedIdentity(ctx context.Context) (ports.SCMIdentity, error) {
	return p.AuthenticatedIdentityForHost(ctx, "")
}

// AuthenticatedIdentityForHost resolves the account behind host's own token.
func (p *Provider) AuthenticatedIdentityForHost(ctx context.Context, host string) (ports.SCMIdentity, error) {
	p.identityMu.Lock()
	defer p.identityMu.Unlock()
	host = canonicalHost(host)
	if host == "" && p.identityResolved {
		return p.identity, nil
	}
	if id, ok := p.hostIdentities[host]; ok && host != "" {
		return id, nil
	}
	resp, err := p.clientFor(host).doREST(ctx, http.MethodGet, "/user", nil, nil)
	if err != nil {
		return ports.SCMIdentity{}, err
	}
	var user struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	}
	if err := json.Unmarshal(resp.Body, &user); err != nil {
		return ports.SCMIdentity{}, fmt.Errorf("github scm: decode authenticated user: %w", err)
	}
	id := ports.SCMIdentity{
		Login: strings.TrimSpace(user.Login),
		Human: strings.EqualFold(strings.TrimSpace(user.Type), "User"),
	}
	if host == "" {
		p.identity, p.identityResolved = id, true
		return id, nil
	}
	if p.hostIdentities == nil {
		p.hostIdentities = map[string]ports.SCMIdentity{}
	}
	p.hostIdentities[host] = id
	return id, nil
}
