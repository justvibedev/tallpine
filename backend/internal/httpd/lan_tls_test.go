package httpd

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

func tlsTestLAN(t *testing.T) (*LANManager, *mobilebridge.GatewayTLS, int) {
	t.Helper()
	gw, err := mobilebridge.EnsureGatewayTLS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := NewMobileLAN(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}), "", 0, nil)
	armTestBearerLAN(m, mobilebridge.HashPassword("secret12"))
	m.SetTLS(gw.Cert)
	port, err := m.Start(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = m.Stop(ctx)
	})
	return m, gw, port
}

// pinnedClient trusts nothing but a certificate whose key hashes to pin.
func pinnedClient(pin string) *http.Client {
	return &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec // verified by VerifyPeerCertificate
			VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
				c, err := x509.ParseCertificate(raw[0])
				if err != nil {
					return err
				}
				if mobilebridge.SPKIPin(c) != pin {
					return fmt.Errorf("pin mismatch")
				}
				return nil
			},
		},
	}}
}

func getAuthed(c *http.Client, url string) error {
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer secret12")
	res, err := c.Do(req)
	if err != nil {
		return err
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", res.StatusCode)
	}
	return nil
}

func TestLANServesTLSOnTheGatewayPortWithMatchingPin(t *testing.T) {
	_, gw, port := tlsTestLAN(t)
	url := fmt.Sprintf("https://127.0.0.1:%d/api/v1/health", port)
	if err := getAuthed(pinnedClient(gw.Pin), url); err != nil {
		t.Fatalf("pinned https request failed: %v", err)
	}
	if err := getAuthed(pinnedClient("deadbeef"), url); err == nil {
		t.Fatal("a wrong pin must fail the handshake")
	}
}

func TestLoopbackPlainHTTPStillServedOnTheTLSGateway(t *testing.T) {
	_, _, port := tlsTestLAN(t)
	// Tailscale Serve and cloudflared reach the gateway as loopback plain http.
	if err := getAuthed(http.DefaultClient, fmt.Sprintf("http://127.0.0.1:%d/api/v1/health", port)); err != nil {
		t.Fatalf("loopback plain: %v", err)
	}
}

func TestRequirePinnedTLSRefusesNetworkPlain(t *testing.T) {
	m, _, _ := tlsTestLAN(t)
	ok := m.requirePinnedTLS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	do := func(remote string, tlsConn bool) int {
		r, _ := http.NewRequest(http.MethodGet, "/api/v1/health", nil)
		r.RemoteAddr = remote
		if tlsConn {
			r = r.WithContext(context.WithValue(r.Context(), lanTLSContextKey{}, true))
		}
		w := httptest.NewRecorder()
		ok.ServeHTTP(w, r)
		return w.Code
	}
	if got := do("192.168.1.9:5000", false); got != http.StatusUpgradeRequired {
		t.Fatalf("plain LAN = %d, want 426", got)
	}
	if got := do("192.168.1.9:5000", true); got != http.StatusOK {
		t.Fatalf("TLS LAN = %d, want 200", got)
	}
	if got := do("127.0.0.1:5000", false); got != http.StatusOK {
		t.Fatalf("loopback plain = %d, want 200", got)
	}
}

func TestLANConnContextFlagsTLSConnections(t *testing.T) {
	gw, err := mobilebridge.EnsureGatewayTLS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	seen := make(chan bool, 2)
	m := NewMobileLAN(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v, _ := r.Context().Value(lanTLSContextKey{}).(bool)
		seen <- v
	}), "", 0, nil)
	armTestBearerLAN(m, mobilebridge.HashPassword("secret12"))
	m.SetTLS(gw.Cert)
	port, err := m.Start(0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = m.Stop(ctx)
	}()
	_ = getAuthed(pinnedClient(gw.Pin), fmt.Sprintf("https://127.0.0.1:%d/api/v1/health", port))
	_ = getAuthed(http.DefaultClient, fmt.Sprintf("http://127.0.0.1:%d/api/v1/health", port))
	if a, b := <-seen, <-seen; !a || b {
		t.Fatalf("tls flag: https=%v http=%v, want true/false", a, b)
	}
}

type tempErr struct{}

func (tempErr) Error() string   { return "too many open files" }
func (tempErr) Temporary() bool { return true }
func (tempErr) Timeout() bool   { return false }

type flakyListener struct {
	net.Listener
	n int
}

func (f *flakyListener) Accept() (net.Conn, error) {
	f.n++
	if f.n == 1 {
		return nil, tempErr{}
	}
	return f.Listener.Accept()
}

func TestSniffListenerSurvivesTemporaryAcceptError(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sl := newSniffListener(&flakyListener{Listener: raw}, &tls.Config{})
	defer sl.Close()
	if _, err := sl.Accept(); err == nil {
		t.Fatal("first Accept should surface the temporary error")
	}
	go func() {
		c, err := net.Dial("tcp", raw.Addr().String())
		if err == nil {
			_, _ = c.Write([]byte("GET / HTTP/1.1\r\n\r\n"))
		}
	}()
	done := make(chan error, 1)
	go func() { _, err := sl.Accept(); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Accept after a temporary error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("gateway stopped accepting after one temporary error")
	}
}

func TestSniffListenerReportsFailedHandshakes(t *testing.T) {
	gw, err := mobilebridge.EnsureGatewayTLS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sl := newSniffListener(raw, &tls.Config{Certificates: []tls.Certificate{gw.Cert}})
	reported := make(chan error, 1)
	sl.onHandshakeErr = func(_ net.Addr, err error) { reported <- err }
	defer sl.Close()
	go func() { _, _ = sl.Accept() }()
	// A client that rejects the certificate (as a phone with the wrong pin does).
	conn, err := tls.Dial("tcp", raw.Addr().String(), &tls.Config{MinVersion: tls.VersionTLS12}) // no trust for the self-signed cert
	if err == nil {
		_ = conn.Close()
		t.Fatal("expected the client to reject the self-signed certificate")
	}
	select {
	case <-reported:
	case <-time.After(3 * time.Second):
		t.Fatal("a failed handshake left no trace")
	}
}
