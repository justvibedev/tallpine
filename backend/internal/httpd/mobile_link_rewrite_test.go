package httpd

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

const reportJSON = `{"text":"[w](tallpine://sessions/p/s)"}`

func reportHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "34")
		io.WriteString(w, reportJSON)
	})
}

func TestMobileLinkRewriteServesAOScheme(t *testing.T) {
	rec := httptest.NewRecorder()
	mobileLinkRewrite(reportHandler()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if got := rec.Body.String(); !strings.Contains(got, "ao://sessions/p/s") || strings.Contains(got, "tallpine://") {
		t.Fatalf("mobile body = %q", got)
	}
	if rec.Header().Get("Content-Length") != "" {
		t.Fatalf("stale Content-Length kept")
	}
}

func TestMobileLinkRewriteLeavesNonJSONAlone(t *testing.T) {
	rec := httptest.NewRecorder()
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "tallpine://sessions/p/s")
	})
	mobileLinkRewrite(h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Body.String() != "tallpine://sessions/p/s" {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestDesktopHandlerKeepsVOScheme(t *testing.T) {
	rec := httptest.NewRecorder()
	reportHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Body.String() != reportJSON {
		t.Fatalf("desktop body = %q", rec.Body.String())
	}
}

// The mobile listener's real handler chain must rewrite; the shared router
// (loopback) must not.
func TestMobileLANHandlerRewritesAndRouterDoesNot(t *testing.T) {
	m := NewMobileLAN(reportHandler(), "h", 0, nil)
	armTestBearerLAN(m, mobilebridge.HashPassword("x"))
	req := loopbackRequest(http.MethodGet, "/api/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer x")
	rec := httptest.NewRecorder()
	m.handler.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "ao://sessions/p/s") {
		t.Fatalf("LAN body = %q (status %d)", rec.Body.String(), rec.Code)
	}
}

func TestEventStreamSendRewritesOnlyForMobileLANClients(t *testing.T) {
	run := func(ctx context.Context, h http.Header) string {
		s := &eventStreamSession{ctx: ctx, upgrade: &http.Request{Header: h}, out: make(chan eventStreamServerMessage, 1)}
		data := "[w](tallpine://sessions/p/s)"
		if err := s.send(eventStreamServerMessage{Type: "event", Data: &data}); err != nil {
			t.Fatal(err)
		}
		return *(<-s.out).Data
	}
	lan := context.WithValue(context.Background(), lanConnContextKey{}, net.Conn(&net.TCPConn{}))
	if got := run(lan, http.Header{}); got != "[w](ao://sessions/p/s)" {
		t.Fatalf("mobile LAN stream data = %q", got)
	}
	if got := run(lan, http.Header{"X-Vo-Client": {"desktop"}}); got != "[w](tallpine://sessions/p/s)" {
		t.Fatalf("desktop remote-host stream data = %q", got)
	}
	if got := run(context.Background(), http.Header{}); got != "[w](tallpine://sessions/p/s)" {
		t.Fatalf("loopback stream data = %q", got)
	}
}

func TestMobileLANKeepsVOSchemeForDesktopRemoteProxy(t *testing.T) {
	m := NewMobileLAN(reportHandler(), "h", 0, nil)
	armTestBearerLAN(m, mobilebridge.HashPassword("x"))
	req := loopbackRequest(http.MethodGet, "/api/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set(desktopClientHeader, "desktop")
	rec := httptest.NewRecorder()
	m.handler.ServeHTTP(rec, req)
	if rec.Body.String() != reportJSON {
		t.Fatalf("desktop-over-LAN body = %q (status %d)", rec.Body.String(), rec.Code)
	}
}
