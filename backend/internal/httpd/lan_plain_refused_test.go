package httpd

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// shellRequest builds an authenticated GET from the given peer address, over
// the gateway's TLS so it reaches auth.
func shellRequest(from, path, token string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.RemoteAddr = from
	r.Header.Set("Authorization", "Bearer "+token)
	return overTLS(r)
}

// plainRequest runs one request through the route-policy gate alone.
func plainRequest(method, path, from string) *httptest.ResponseRecorder {
	h := lanControlBlock(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	r := httptest.NewRequest(method, path, nil)
	r.RemoteAddr = from
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// overTLS marks r as arriving on a connection the gateway terminated TLS on,
// as the listener's ConnContext does, for tests of what lies behind the gate.
func overTLS(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), lanTLSContextKey{}, true))
}

var routeParam = regexp.MustCompile(`\{[^}]*\}`)

// Every entry point of the mobile listener (API, chat, streams, shell, tunnel,
// pairing, identity probe) refuses plain http from the network with 426
// HTTPS_REQUIRED, even with a valid device token, and from a tailnet address
// too. This manager has no TLS configured: the listener must fail closed, not
// fall back to serving plain http.
func TestEveryEntryPointRefusesPlainHTTPFromTheNetwork(t *testing.T) {
	m, tokens := newPairingManager(t, "Code1234")
	tok, _, err := tokens.Issue("i-1", "")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/sessions"},
		{http.MethodGet, "/api/v1/sessions/tp-1/conversation"},
		{http.MethodPost, "/api/v1/sessions/tp-1/conversation/messages"},
		{http.MethodGet, "/mux"},
		{http.MethodGet, identityProbePath},
		{http.MethodGet, lanTunnelPath},
		{http.MethodPost, lanPairExchangePath},
	}
	for _, peer := range []string{"192.168.1.20:5000", "100.80.1.9:5000", "203.0.113.7:5000"} {
		for _, c := range cases {
			r := httptest.NewRequest(c.method, c.path, strings.NewReader(`{"code":"Code1234","installId":"i-2"}`))
			r.RemoteAddr = peer
			r.Header.Set("Authorization", "Bearer "+tok)
			w := httptest.NewRecorder()
			m.dispatch().ServeHTTP(w, r)
			if w.Code != http.StatusUpgradeRequired || !strings.Contains(w.Body.String(), "HTTPS_REQUIRED") {
				t.Errorf("%s %s from %s = %d %s, want 426 HTTPS_REQUIRED", c.method, c.path, peer, w.Code, w.Body)
			}
		}
	}
	// The refused exchange must not have spent the code: over loopback (the
	// Tailscale Serve / tunnel proxy, which terminated TLS) it still pairs.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, lanPairExchangePath, strings.NewReader(`{"code":"Code1234","installId":"i-2"}`))
	r.RemoteAddr = "127.0.0.1:5000"
	m.dispatch().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("loopback exchange = %d %s, want 200", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	m.dispatch().ServeHTTP(w, shellRequest("127.0.0.1:5001", "/api/v1/sessions", tok))
	if w.Code != http.StatusOK {
		t.Fatalf("loopback API = %d, want 200", w.Code)
	}
}

// On a real socket without TLS, a request through the machine's own
// non-loopback address is refused; the same request via loopback is served.
func TestRealListenerWithoutTLSRefusesNetworkPeers(t *testing.T) {
	var lanIP net.IP
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLoopback() {
			lanIP = ipn.IP
			break
		}
	}
	if lanIP == nil {
		t.Skip("no non-loopback IPv4 address on this machine")
	}
	m, tokens := newPairingManager(t, "")
	tok, _, _ := tokens.Issue("i-1", "")
	port, err := m.Start(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop(t.Context()) })
	get := func(host string) int {
		r, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s:%d/api/v1/sessions", host, port), nil)
		r.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if got := get(lanIP.String()); got != http.StatusUpgradeRequired {
		t.Fatalf("plain http via the LAN address = %d, want 426", got)
	}
	if got := get("127.0.0.1"); got != http.StatusOK {
		t.Fatalf("via loopback = %d, want 200", got)
	}
}
