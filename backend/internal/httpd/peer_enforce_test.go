//go:build !windows

package httpd

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/daemonsock"
	"tallpine.eu/tallpine/backend/internal/daemontoken"
	"tallpine.eu/tallpine/backend/internal/peeradmit"
)

type stubVerifier peeradmit.Signature

func (s stubVerifier) Verify([8]uint32) peeradmit.Signature { return peeradmit.Signature(s) }

type stubRoots struct{ agent, shell []int }

func (s stubRoots) AgentRoots(context.Context, peeradmit.Credential) ([]int, error) {
	return s.agent, nil
}
func (s stubRoots) ShellRoots(context.Context) ([]int, error) { return s.shell, nil }

const (
	agentRoot = 30
	shellRoot = 40
)

// enforcingChecker places this test process (the socket peer) under parent:
// agentRoot, shellRoot, or 1 for a process Tallpine did not start.
func enforcingChecker(t *testing.T, parent int, signature peeradmit.Signature) *peeradmit.Checker {
	t.Helper()
	checker := peeradmit.New(peeradmit.Options{
		Mode: peeradmit.ModeEnforce, ModeReason: "test", Verifier: stubVerifier(signature),
		Roots: stubRoots{agent: []int{agentRoot}, shell: []int{shellRoot}},
		Inspect: func(pid int) (peeradmit.Lineage, error) {
			return peeradmit.Lineage{Chain: []int{pid, parent}, Session: pid}, nil
		},
		SessionOf:   func(pid int) (int, error) { return pid, nil },
		LaunchGrace: -1,
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go checker.Run(ctx)
	return checker
}

// unixPeerServer serves handler on a Unix socket, so the kernel reports this
// process as the peer, as on the daemon socket.
func unixPeerServer(t *testing.T, handler http.Handler) (*http.Client, string) {
	t.Helper()
	path := filepath.Join(filepath.Dir(shortRunPath(t)), "d.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(handler)
	srv.Listener = listener
	srv.Config.ConnContext = daemonsock.ContextWithConn
	srv.Start()
	t.Cleanup(srv.Close)
	transport := daemonsock.Transport(path)
	transport.DisableKeepAlives = true
	return &http.Client{Transport: transport}, "http://daemon"
}

func enforceHandler(t *testing.T, peers *peeradmit.Checker) (http.Handler, *fakeAgentScopeStore, string) {
	t.Helper()
	store, tokens := newAgentScopeFixture()
	scopes := newAgentScopes(tokens, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	scopes.peers = peers
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("X-Reached", "yes") })
	return daemonAuthMiddleware(testDaemonToken, "", nil, scopes, peers)(inner), store, store.tokenFor(tokens, "app-1")
}

func sendWith(t *testing.T, client *http.Client, base, method, path, token string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, base+path, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set(daemontoken.Header, token)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body.Code
}

func TestEnforceRefusesInstallTokenFromOutsideTheDesktop(t *testing.T) {
	handler, _, _ := enforceHandler(t, enforcingChecker(t, 1, peeradmit.SignatureOther))
	client, base := unixPeerServer(t, handler)
	status, code := sendWith(t, client, base, http.MethodGet, "/api/v1/sessions", testDaemonToken)
	if status != http.StatusForbidden || code != PeerNotAdmittedCode {
		t.Fatalf("install token from an outside process = %d %q, want 403 %s", status, code, PeerNotAdmittedCode)
	}
	if status, _ := sendWith(t, client, base, http.MethodPost, "/shutdown", testDaemonToken); status != http.StatusForbidden {
		t.Fatalf("POST /shutdown from an outside process = %d, want 403", status)
	}
}

func TestEnforceAdmitsDesktopShellAndAgent(t *testing.T) {
	cases := []struct {
		name      string
		parent    int
		signature peeradmit.Signature
		scoped    bool
	}{
		{"signed desktop", 1, peeradmit.SignatureDesktop, false},
		{"desktop-opened shell", shellRoot, peeradmit.SignatureOther, false},
		{"agent under its session", agentRoot, peeradmit.SignatureUnknown, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler, _, agentToken := enforceHandler(t, enforcingChecker(t, tc.parent, tc.signature))
			client, base := unixPeerServer(t, handler)
			token := testDaemonToken
			if tc.scoped {
				token = agentToken
			}
			if status, code := sendWith(t, client, base, http.MethodGet, "/api/v1/sessions/app-1", token); status != http.StatusOK {
				t.Fatalf("status %d %q, want 200", status, code)
			}
		})
	}
}

func TestEnforceRefusesStolenScopedToken(t *testing.T) {
	handler, _, agentToken := enforceHandler(t, enforcingChecker(t, 1, peeradmit.SignatureOther))
	client, base := unixPeerServer(t, handler)
	if status, code := sendWith(t, client, base, http.MethodGet, "/api/v1/sessions/app-1", agentToken); status != http.StatusForbidden || code != PeerNotAdmittedCode {
		t.Fatalf("scoped token outside its session = %d %q, want 403 %s", status, code, PeerNotAdmittedCode)
	}
}

// A chat agent is a daemon child, so the daemon is in its chain; that never
// unlocks the install token.
func TestEnforceRefusesDaemonChildWithInstallToken(t *testing.T) {
	handler, _, _ := enforceHandler(t, enforcingChecker(t, os.Getppid(), peeradmit.SignatureOther))
	client, base := unixPeerServer(t, handler)
	if status, _ := sendWith(t, client, base, http.MethodGet, "/api/v1/sessions", testDaemonToken); status != http.StatusForbidden {
		t.Fatalf("daemon child with the install token = %d, want 403", status)
	}
}

func TestEnforceKeepsWrongTokenAndExemptRoutes(t *testing.T) {
	handler, _, _ := enforceHandler(t, enforcingChecker(t, 1, peeradmit.SignatureOther))
	client, base := unixPeerServer(t, handler)
	if status, code := sendWith(t, client, base, http.MethodGet, "/api/v1/sessions", "wrong"); status != http.StatusUnauthorized || code != "DAEMON_TOKEN_REQUIRED" {
		t.Fatalf("wrong token = %d %q, want 401", status, code)
	}
	for _, token := range []string{"", testDaemonToken} {
		for _, path := range []string{"/healthz", "/readyz"} {
			if status, _ := sendWith(t, client, base, http.MethodGet, path, token); status != http.StatusOK {
				t.Fatalf("exempt %s with token=%v = %d", path, token != "", status)
			}
		}
	}
}

func TestEnforceRefusesTCPExceptExemptRoutes(t *testing.T) {
	// Even a desktop signature cannot be seen over TCP.
	srv := peerCheckTestServer(t, enforcingChecker(t, 1, peeradmit.SignatureDesktop))
	if status, header, _ := (peerCheckRequest{method: http.MethodGet, path: "/api/v1/sessions", token: testDaemonToken}).send(t, srv); status != http.StatusForbidden || header.Get("X-Reached") != "" {
		t.Fatalf("TCP with the install token = %d, want 403", status)
	}
	for _, req := range []peerCheckRequest{
		{method: http.MethodGet, path: "/healthz", token: testDaemonToken},
		{method: http.MethodGet, path: "/readyz"},
		{method: http.MethodOptions, path: "/api/v1/sessions", origin: true},
	} {
		if status, _, _ := req.send(t, srv); status != http.StatusOK {
			t.Fatalf("exempt %s %s over TCP = %d", req.method, req.path, status)
		}
	}
}

func TestEnforceOffSwitchAppliesLive(t *testing.T) {
	checker := enforcingChecker(t, 1, peeradmit.SignatureOther)
	handler, _, _ := enforceHandler(t, checker)
	client, base := unixPeerServer(t, handler)
	checker.SetDisabled(true)
	if status, _ := sendWith(t, client, base, http.MethodGet, "/api/v1/sessions", testDaemonToken); status != http.StatusOK {
		t.Fatalf("off switch: %d, want 200", status)
	}
	checker.SetDisabled(false)
	if status, _ := sendWith(t, client, base, http.MethodGet, "/api/v1/sessions", testDaemonToken); status != http.StatusForbidden {
		t.Fatalf("back to default: %d, want 403", status)
	}
}

func reviewerPost(t *testing.T, client *http.Client, base, path, token, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(daemontoken.Header, token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out.Code
}

// A reviewer's session-end hook fires after its runtime is gone; it is dropped
// with 200 and never reaches the handler. Nothing else is admitted from outside.
func TestEnforceDropsReviewerSessionEndOutsideItsRoots(t *testing.T) {
	store, tokens := newAgentScopeFixture()
	peers := enforcingChecker(t, 1, peeradmit.SignatureOther)
	scopes := newAgentScopes(tokens, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	scopes.peers = peers
	reached := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true })
	client, base := unixPeerServer(t, daemonAuthMiddleware(testDaemonToken, "", nil, scopes, peers)(inner))
	token := tokens.IssueReviewer("rev-app-1", "app-1")
	path := "/api/v1/reviews/rev-app-1/activity"

	if status, _ := reviewerPost(t, client, base, path, token, `{"event":"session-end","state":"exited"}`); status != http.StatusOK || reached {
		t.Fatalf("session-end = %d reached=%v, want quiet 200 without reaching the handler", status, reached)
	}
	if status, code := reviewerPost(t, client, base, path, token, `{"event":"stop","state":"idle"}`); status != http.StatusForbidden || code != PeerNotAdmittedCode {
		t.Fatalf("other activity from outside = %d %q, want 403", status, code)
	}
	if status, code := reviewerPost(t, client, base, "/api/v1/reviews/rev-app-2/activity", token, `{"event":"session-end"}`); status == http.StatusOK || reached {
		t.Fatalf("session-end for another review = %d %q reached=%v, want refusal", status, code, reached)
	}
	if status, code := sendWith(t, client, base, http.MethodGet, "/api/v1/sessions/app-1", token); status != http.StatusForbidden || code == "" {
		t.Fatalf("other route from outside = %d %q, want 403", status, code)
	}
}
