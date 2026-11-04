package httpd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

// The quick tunnel's visitor address is believed only on cloudflared's own
// loopback port; Tailscale Serve reaches the main port from loopback too.
func TestCFConnectingIPIsTrustedOnlyOnTheTunnelPort(t *testing.T) {
	st := &authState{}
	armTestBearer(st, mobilebridge.HashPassword("secret12"))
	echo := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, clientSource(r)) })
	m := NewLANManager(echo, st, 0, slog.Default())
	port, err := m.StartLoopback(0)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop(context.Background())
	tunnelPort := m.OpenTunnelPort()
	if tunnelPort == 0 || tunnelPort == port {
		t.Fatalf("tunnel port = %d, main port = %d; cloudflared needs its own", tunnelPort, port)
	}
	get := func(p int) string {
		req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/api/v1/sessions", p), nil)
		req.Header.Set("Authorization", "Bearer secret12")
		req.Header.Set("CF-Connecting-IP", "203.0.113.7")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	if got := get(tunnelPort); got != "203.0.113.7" {
		t.Errorf("tunnel port source = %q, want the visitor", got)
	}
	if got := get(port); got != "127.0.0.1" {
		t.Errorf("main port source = %q, want the loopback peer (header must be ignored)", got)
	}
}

// No cloudflared loopback port exists until the quick tunnel asks for one, and
// it is gone again after CloseTunnelPort.
func TestTunnelPortOpensOnlyOnDemand(t *testing.T) {
	st := &authState{}
	armTestBearer(st, mobilebridge.HashPassword("secret12"))
	m := NewLANManager(http.NotFoundHandler(), st, 0, slog.Default())
	if _, err := m.StartLoopback(0); err != nil {
		t.Fatal(err)
	}
	defer m.Stop(context.Background())
	if p := m.TunnelPort(); p != 0 {
		t.Fatalf("tunnel port %d open before the tunnel was enabled", p)
	}
	p := m.OpenTunnelPort()
	if p == 0 || m.OpenTunnelPort() != p {
		t.Fatalf("open tunnel port = %d, want stable non-zero", p)
	}
	m.CloseTunnelPort()
	if m.TunnelPort() != 0 {
		t.Fatal("tunnel port still set after close")
	}
	if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p), time.Second); err == nil {
		_ = c.Close()
		t.Fatalf("port %d still accepting after close", p)
	}
}
