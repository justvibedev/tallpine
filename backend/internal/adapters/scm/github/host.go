package github

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// ErrHostNotAllowed marks a request for a host that is neither github.com, a
// *.ghe.com tenant, nor a listed GHES host. No request is sent for it.
var ErrHostNotAllowed = errors.New("github scm: host is not an allowed GitHub host")

// hostClients lazily builds one Client per enterprise host: its own API base,
// its own token source, its own ETag cache. github.com keeps Provider.client.
type hostClients struct {
	opts    ProviderOptions
	listed  map[string]bool
	logger  *slog.Logger
	mu      sync.Mutex
	clients map[string]*Client
	// ghToken builds the `gh auth token --hostname` source; tests replace it.
	ghToken func(hostname string) TokenSource
}

func newHostClients(opts ProviderOptions, logger *slog.Logger) *hostClients {
	listed := map[string]bool{}
	for _, h := range opts.EnterpriseHosts {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			listed[h] = true
		}
	}
	return &hostClients{opts: opts, listed: listed, logger: logger, clients: map[string]*Client{},
		ghToken: func(hostname string) TokenSource { return &GHTokenSource{Host: hostname} }}
}

// canonicalHost returns "" for github.com (default client) or the lowercase
// host (port kept) for any other host.
func canonicalHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || domain.IsGitHubDotComHost(host) {
		return ""
	}
	return host
}

// clientFor returns the Client for host. A rejected host yields a Client whose
// every call fails with ErrHostNotAllowed before any request is made.
func (p *Provider) clientFor(host string) *Client {
	host = canonicalHost(host)
	if host == "" {
		return p.client
	}
	return p.hosts.get(host)
}

func (h *hostClients) get(host string) *Client {
	h.mu.Lock()
	defer h.mu.Unlock()
	if c, ok := h.clients[host]; ok {
		return c
	}
	c := h.build(host)
	h.clients[host] = c
	return c
}

func (h *hostClients) build(host string) *Client {
	opts := ClientOptions{HTTPClient: h.opts.HTTPClient, UserAgent: h.opts.UserAgent}
	hostname := host
	if i := strings.LastIndex(host, ":"); i >= 0 && !strings.Contains(host[i:], "]") {
		hostname = host[:i]
	}
	switch {
	case h.listed[host]:
		opts.RESTBase = "https://" + host + "/api/v3"
		opts.GraphQLURL = "https://" + host + "/api/graphql"
	case domain.IsGHEComHost(host) && !strings.HasPrefix(hostname, "api.") && hostname == host:
		opts.RESTBase = "https://api." + host
		opts.GraphQLURL = "https://api." + host + "/graphql"
	default:
		opts.HTTPClient = &http.Client{Transport: deniedTransport{fmt.Errorf("%w: %q", ErrHostNotAllowed, host)}}
		return NewClient(opts)
	}
	var src TokenSource
	if h.opts.HostToken != nil {
		src = h.opts.HostToken(host)
	} else if t := strings.TrimSpace(h.opts.HostTokens[host]); t != "" {
		src = StaticTokenSource(t)
	} else {
		src = h.ghToken(hostname)
	}
	opts.Token = &logOnceTokenSource{inner: src, host: host, logger: h.logger}
	return NewClient(opts)
}

// logOnceTokenSource logs the first credential failure per host.
type logOnceTokenSource struct {
	inner  TokenSource
	host   string
	logger *slog.Logger
	once   sync.Once
}

func (s *logOnceTokenSource) Token(ctx context.Context) (string, error) {
	t, err := s.inner.Token(ctx)
	if err != nil {
		s.once.Do(func() {
			s.logger.Warn("github scm: no usable token for enterprise host", "host", s.host, "error", err)
		})
	}
	return t, err
}

func (s *logOnceTokenSource) InvalidateToken() {
	if inv, ok := s.inner.(tokenInvalidator); ok {
		inv.InvalidateToken()
	}
}

// deniedTransport fails every request before it leaves the process.
type deniedTransport struct{ err error }

func (d deniedTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, d.err }

// credentialsAvailable reports whether any enterprise host has a usable token:
// configured tokens/hosts, or a gh login for a listed GHES host or a *.ghe.com
// host registered projects use.
func (h *hostClients) credentialsAvailable(ctx context.Context) bool {
	if len(h.opts.HostTokens) > 0 || h.opts.HostToken != nil {
		return true
	}
	cands := make([]string, 0, len(h.listed)+4)
	for host := range h.listed {
		cands = append(cands, host)
	}
	if h.opts.KnownHosts != nil {
		for _, host := range h.opts.KnownHosts(ctx) {
			if host = canonicalHost(host); domain.IsGHEComHost(host) {
				cands = append(cands, host)
			}
		}
	}
	for _, host := range cands {
		if c := h.get(host); c.tokens != nil {
			if _, err := c.tokens.Token(ctx); err == nil {
				return true
			}
		}
	}
	return false
}
