package github

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type seenReq struct{ host, path, auth string }

// hostFake serves every host from one httptest server and records which host,
// path and token each request addressed (a host-mapping RoundTripper).
type hostFake struct {
	mu   sync.Mutex
	seen []seenReq
	srv  *httptest.Server
}

type mapTransport struct {
	f *hostFake
}

func (m mapTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r2 := r.Clone(r.Context())
	r2.Header.Set("X-Orig-Host", r.URL.Host)
	u := *r.URL
	srvURL := m.f.srv.URL
	u.Scheme, u.Host = "http", strings.TrimPrefix(srvURL, "http://")
	r2.URL = &u
	return http.DefaultTransport.RoundTrip(r2)
}

var aliasRe = regexp.MustCompile(`(pr\d+): repository\(owner:"[^"]*",name:"[^"]*"\)\{ pullRequest\(number:(\d+)\)`)

func newHostFake(t *testing.T) *hostFake {
	f := &hostFake{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.seen = append(f.seen, seenReq{r.Header.Get("X-Orig-Host"), r.URL.Path, r.Header.Get("Authorization")})
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/graphql"):
			raw, _ := io.ReadAll(r.Body)
			var req struct {
				Query string `json:"query"`
			}
			_ = json.Unmarshal(raw, &req)
			body := req.Query
			data := map[string]any{}
			for _, m := range aliasRe.FindAllStringSubmatch(body, -1) {
				n := 0
				for _, c := range m[2] {
					n = n*10 + int(c-'0')
				}
				data[m[1]] = map[string]any{"pullRequest": map[string]any{"number": n}}
			}
			if len(data) == 0 { // single-PR query with variables
				data["repository"] = map[string]any{"pullRequest": map[string]any{"number": 1}}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		case strings.Contains(r.URL.Path, "/pulls/"):
			_, _ = w.Write([]byte(`{"state":"open","head":{"sha":"abc"}}`))
		default:
			_, _ = w.Write([]byte(`{"login":"` + r.Header.Get("X-Orig-Host") + `-user","type":"User"}`))
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *hostFake) hosts() map[string]bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]bool{}
	for _, s := range f.seen {
		out[s.host] = true
	}
	return out
}

func (f *hostFake) auths(host string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, s := range f.seen {
		if s.host == host {
			out = append(out, s.auth)
		}
	}
	return out
}

func (f *hostFake) pathsFor(host string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, s := range f.seen {
		if s.host == host {
			out = append(out, s.path)
		}
	}
	return out
}

func newEnterpriseProvider(t *testing.T, f *hostFake, mod func(*ProviderOptions)) *Provider {
	t.Helper()
	domain.SetGitHubEnterpriseHosts([]string{"ghes.corp.example", "ghes.corp.example:8443"})
	t.Cleanup(func() { domain.SetGitHubEnterpriseHosts(nil) })
	opts := ProviderOptions{
		Token:           StaticTokenSource("dotcom-token"),
		HTTPClient:      &http.Client{Transport: mapTransport{f}},
		EnterpriseHosts: []string{"ghes.corp.example", "ghes.corp.example:8443"},
		HostTokens:      map[string]string{"acme.ghe.com": "ghe-token", "ghes.corp.example": "ghes-token"},
	}
	if mod != nil {
		mod(&opts)
	}
	p, err := NewProvider(opts)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestObserveGHEComUsesTenantAPIAndToken(t *testing.T) {
	f := newHostFake(t)
	p := newEnterpriseProvider(t, f, nil)
	obs, err := p.Observe(ctx(), "https://acme.ghe.com/o/r/pull/7")
	if err != nil || !obs.Fetched {
		t.Fatalf("Observe = %+v, %v", obs, err)
	}
	if h := f.hosts(); len(h) != 1 || !h["api.acme.ghe.com"] {
		t.Fatalf("hosts = %v, want only api.acme.ghe.com", h)
	}
	if got := f.pathsFor("api.acme.ghe.com"); got[0] != "/repos/o/r/pulls/7" || got[1] != "/graphql" {
		t.Fatalf("paths = %v", got)
	}
	for _, a := range f.auths("api.acme.ghe.com") {
		if a != "Bearer ghe-token" {
			t.Fatalf("auth = %q", a)
		}
	}
}

func TestObserveGHESUsesAPIv3AndHostToken(t *testing.T) {
	f := newHostFake(t)
	p := newEnterpriseProvider(t, f, nil)
	obs, err := p.Observe(ctx(), "https://ghes.corp.example/o/r/pull/9")
	if err != nil || !obs.Fetched {
		t.Fatalf("Observe = %+v, %v", obs, err)
	}
	got := f.pathsFor("ghes.corp.example")
	if len(got) < 2 || got[0] != "/api/v3/repos/o/r/pulls/9" || got[1] != "/api/graphql" {
		t.Fatalf("paths = %v", got)
	}
	for _, a := range f.auths("ghes.corp.example") {
		if a != "Bearer ghes-token" {
			t.Fatalf("auth = %q", a)
		}
	}
	if len(f.hosts()) != 1 {
		t.Fatalf("hosts = %v", f.hosts())
	}
}

func TestGHESPortIsPartOfHostIdentity(t *testing.T) {
	f := newHostFake(t)
	p := newEnterpriseProvider(t, f, func(o *ProviderOptions) {
		o.HostTokens["ghes.corp.example:8443"] = "port-token"
	})
	if _, err := p.Observe(ctx(), "https://ghes.corp.example:8443/o/r/pull/9"); err != nil {
		t.Fatal(err)
	}
	if h := f.hosts(); len(h) != 1 || !h["ghes.corp.example:8443"] {
		t.Fatalf("hosts = %v", h)
	}
	if a := f.auths("ghes.corp.example:8443"); a[0] != "Bearer port-token" {
		t.Fatalf("auth = %v", a)
	}
}

func TestUnlistedHostSendsNoRequest(t *testing.T) {
	f := newHostFake(t)
	p := newEnterpriseProvider(t, f, nil)
	obs, err := p.FetchPullRequests(ctx(), []ports.SCMPRRef{{Repo: ports.SCMRepo{Provider: "github", Host: "evil.example.com", Owner: "o", Name: "r"}, Number: 1}})
	if err == nil && (len(obs) != 1 || obs[0].Fetched) {
		t.Fatalf("unlisted host fetched: %+v", obs)
	}
	if err != nil && !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("err = %v", err)
	}
	if _, err := p.Observe(ctx(), "https://evil.example.com/o/r/pull/1"); err == nil {
		t.Fatal("Observe on unlisted host succeeded")
	}
	if len(f.seen) != 0 {
		t.Fatalf("requests sent for unlisted host: %v", f.seen)
	}
}

func TestDotComTokenNeverSentToEnterpriseHost(t *testing.T) {
	f := newHostFake(t)
	// No enterprise token: the github.com token must not be used instead.
	p := newEnterpriseProvider(t, f, func(o *ProviderOptions) {
		o.HostTokens = nil
		o.HostToken = func(string) TokenSource { return StaticTokenSource("") }
	})
	if _, err := p.Observe(ctx(), "https://acme.ghe.com/o/r/pull/7"); err == nil {
		t.Fatal("expected auth error")
	}
	for _, s := range f.seen {
		if s.auth == "Bearer dotcom-token" {
			t.Fatalf("github.com token sent to %s", s.host)
		}
	}
}

func TestMixedBatchGroupsByHostAndAligns(t *testing.T) {
	f := newHostFake(t)
	p := newEnterpriseProvider(t, f, nil)
	gh := ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "o", Name: "r", Repo: "o/r"}
	ghe := ports.SCMRepo{Provider: "github", Host: "acme.ghe.com", Owner: "o", Name: "r", Repo: "o/r"}
	obs, err := p.FetchPullRequests(ctx(), []ports.SCMPRRef{{Repo: gh, Number: 1}, {Repo: ghe, Number: 2}, {Repo: gh, Number: 3}})
	if err != nil || len(obs) != 3 {
		t.Fatalf("obs=%v err=%v", obs, err)
	}
	for i, want := range []int{1, 2, 3} {
		if obs[i].PR.Number != want || !obs[i].Fetched {
			t.Fatalf("obs[%d] = %+v, want fetched PR %d", i, obs[i], want)
		}
	}
	if a := f.auths("api.github.com"); len(a) != 1 || a[0] != "Bearer dotcom-token" {
		t.Fatalf("github.com auths = %v", a)
	}
	if a := f.auths("api.acme.ghe.com"); len(a) != 1 || a[0] != "Bearer ghe-token" {
		t.Fatalf("ghe auths = %v", a)
	}
}

func TestMissingEnterpriseTokenIsolatesFailure(t *testing.T) {
	f := newHostFake(t)
	p := newEnterpriseProvider(t, f, func(o *ProviderOptions) {
		o.HostTokens = nil
		o.HostToken = func(string) TokenSource { return StaticTokenSource("") }
	})
	gh := ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "o", Name: "r", Repo: "o/r"}
	ghe := ports.SCMRepo{Provider: "github", Host: "acme.ghe.com", Owner: "o", Name: "r", Repo: "o/r"}
	obs, err := p.FetchPullRequests(ctx(), []ports.SCMPRRef{{Repo: gh, Number: 1}, {Repo: ghe, Number: 2}})
	if err != nil {
		t.Fatalf("err = %v, want per-host isolation", err)
	}
	if !obs[0].Fetched || obs[1].Fetched || !errors.Is(obs[1].Error, ErrAuthFailed) {
		t.Fatalf("obs = %+v", obs)
	}
}

func TestAuthenticatedIdentityForHost(t *testing.T) {
	f := newHostFake(t)
	p := newEnterpriseProvider(t, f, nil)
	a, err := p.AuthenticatedIdentityForHost(ctx(), "")
	if err != nil || a.Login != "api.github.com-user" {
		t.Fatalf("dotcom = %+v %v", a, err)
	}
	b, err := p.AuthenticatedIdentityForHost(ctx(), "acme.ghe.com")
	if err != nil || b.Login != "api.acme.ghe.com-user" {
		t.Fatalf("ghe = %+v %v", b, err)
	}
}

func TestListPRsAndGuardRouteByRepoHost(t *testing.T) {
	f := newHostFake(t)
	p := newEnterpriseProvider(t, f, nil)
	repo := ports.SCMRepo{Provider: "github", Host: "ghes.corp.example", Owner: "o", Name: "r"}
	_, _ = p.RepoPRListGuard(ctx(), repo, "")
	_, _ = p.ListPRsByRepo(ctx(), repo, time.Time{})
	if h := f.hosts(); len(h) != 1 || !h["ghes.corp.example"] {
		t.Fatalf("hosts = %v", h)
	}
}

func TestCredentialsAvailableWithOnlyEnterpriseCredentials(t *testing.T) {
	f := newHostFake(t)
	p := newEnterpriseProvider(t, f, func(o *ProviderOptions) { o.Token, o.SkipTokenPreflight = StaticTokenSource(""), true })
	if ok, err := p.SCMCredentialsAvailable(ctx()); err != nil || !ok {
		t.Fatalf("enterprise-only = %v, %v; want true", ok, err)
	}
	bare := newEnterpriseProvider(t, f, func(o *ProviderOptions) {
		o.Token, o.EnterpriseHosts, o.HostTokens, o.SkipTokenPreflight = StaticTokenSource(""), nil, nil, true
	})
	if ok, _ := bare.SCMCredentialsAvailable(ctx()); ok {
		t.Fatal("no credentials at all must stay false")
	}
}

func TestCredentialsAvailableGHEComViaGhOnly(t *testing.T) {
	f := newHostFake(t)
	ghTok := func(host string) TokenSource {
		return &GHTokenSource{Host: host, GH: func(context.Context) (string, error) { return "gh-login-token", nil }}
	}
	mk := func(known []string) *Provider {
		p := newEnterpriseProvider(t, f, func(o *ProviderOptions) {
			o.Token, o.SkipTokenPreflight = StaticTokenSource(""), true // no github.com login
			o.EnterpriseHosts, o.HostTokens = nil, nil
			o.KnownHosts = func(context.Context) []string { return known }
		})
		p.hosts.ghToken = func(host string, _ bool) TokenSource { return ghTok(host) }
		return p
	}
	if ok, err := mk([]string{"acme.ghe.com"}).SCMCredentialsAvailable(ctx()); err != nil || !ok {
		t.Fatalf("ghe.com via gh only = %v, %v; want true", ok, err)
	}
	if ok, _ := mk(nil).SCMCredentialsAvailable(ctx()); ok {
		t.Fatal("no known ghe.com host and no listing must stay false")
	}
}

type errTokenSource struct{ err error }

func (e errTokenSource) Token(context.Context) (string, error) { return "", e.err }

func TestCredentialsAvailableEnterpriseWhenGhExitsNonZero(t *testing.T) {
	f := newHostFake(t)
	for _, tokenErr := range []error{errors.New("exit status 1"), exec.ErrNotFound} {
		p := newEnterpriseProvider(t, f, func(o *ProviderOptions) {
			o.Token, o.SkipTokenPreflight = errTokenSource{tokenErr}, true
		})
		if ok, err := p.SCMCredentialsAvailable(ctx()); err != nil || !ok {
			t.Fatalf("%v: = %v, %v; want true", tokenErr, ok, err)
		}
	}
	bare := newEnterpriseProvider(t, f, func(o *ProviderOptions) {
		o.Token, o.SkipTokenPreflight = errTokenSource{errors.New("exit status 1")}, true
		o.EnterpriseHosts, o.HostTokens = nil, nil
	})
	if ok, err := bare.SCMCredentialsAvailable(ctx()); ok || err == nil {
		t.Fatalf("no enterprise creds must surface the error: %v, %v", ok, err)
	}
}

func TestGHEComTenantNeverGetsAmbientToken(t *testing.T) {
	f := newHostFake(t)
	var gotStored = map[string]bool{}
	p := newEnterpriseProvider(t, f, func(o *ProviderOptions) {
		o.EnterpriseHosts, o.HostTokens = []string{"corp.ghe.com"}, nil
	})
	p.hosts.ghToken = func(host string, stored bool) TokenSource {
		gotStored[host] = stored
		return StaticTokenSource("x")
	}
	p.hosts.get("stranger.ghe.com")
	listed := p.hosts.get("corp.ghe.com")
	if listed.restBase != "https://api.corp.ghe.com" {
		t.Fatalf("listed tenant base = %q, want api.corp.ghe.com", listed.restBase)
	}
	if !gotStored["stranger.ghe.com"] {
		t.Fatal("unconfigured repo-named tenant must use stored gh login only")
	}
	if gotStored["corp.ghe.com"] {
		t.Fatal("a listed tenant may use the ambient token")
	}
}
