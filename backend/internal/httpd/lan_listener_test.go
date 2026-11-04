package httpd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	"tallpine.eu/tallpine/backend/internal/mobilebridge"
	"tallpine.eu/tallpine/backend/internal/ports"
	agentsvc "tallpine.eu/tallpine/backend/internal/service/agent"
)

func TestLANControlBlockMarksRequestContext(t *testing.T) {
	seenLAN := false
	handler := lanControlBlock(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenLAN = requestscope.IsLAN(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, loopbackRequest(http.MethodGet, "/api/v1/sessions/s1/conversation/messages", nil))
	if recorder.Code != http.StatusNoContent || !seenLAN {
		t.Fatalf("status=%d seenLAN=%v", recorder.Code, seenLAN)
	}
}

func TestMobileLANRejectsReassignedAddressWithSamePassword(t *testing.T) {
	calls := 0
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusAccepted)
	})
	// The address formerly belonged to h_A; h_B now answers there and happens
	// to use the same connection password.
	m := NewMobileLAN(inner, "h_B", 0, nil)
	armTestBearerLAN(m, mobilebridge.HashPassword("same-secret"))
	request := func(expectedHostID, password string) *httptest.ResponseRecorder {
		req := loopbackRequest(http.MethodPost, "/api/v1/sessions/s1/conversation/interrupt", nil)
		req.Header.Set("Authorization", "Bearer "+password)
		if expectedHostID != "" {
			req.Header.Set("X-AO-Expected-Host-ID", expectedHostID)
		}
		recorder := httptest.NewRecorder()
		m.handler.ServeHTTP(recorder, req)
		return recorder
	}
	if got := request("h_A", "same-secret"); got.Code != http.StatusMisdirectedRequest || !strings.Contains(got.Body.String(), `"code":"HOST_ID_MISMATCH"`) || calls != 0 {
		t.Fatalf("stale h_A request: status=%d body=%q calls=%d", got.Code, got.Body.String(), calls)
	}
	if got := request("h_B", "same-secret"); got.Code != http.StatusAccepted || calls != 1 {
		t.Fatalf("matching h_B request: status=%d calls=%d", got.Code, calls)
	}
	if got := request("", "same-secret"); got.Code != http.StatusAccepted || calls != 2 {
		t.Fatalf("legacy request: status=%d calls=%d", got.Code, calls)
	}
	// The host check runs before auth, so another host's phone is told 421
	// rather than a 401 it would read as its own pairing being removed. The
	// host id is public anyway (GET /api/v1/identity).
	if got := request("h_A", "wrong"); got.Code != http.StatusMisdirectedRequest || calls != 2 {
		t.Fatalf("other host's credential: status=%d calls=%d", got.Code, calls)
	}
	if got := request("h_B", "wrong"); got.Code != http.StatusUnauthorized || calls != 2 {
		t.Fatalf("unauthenticated request: status=%d calls=%d", got.Code, calls)
	}
}

func TestLANManagerAuthGatesSharedHandler(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	})
	st := &authState{}
	armTestBearer(st, mobilebridge.HashPassword("secret12"))
	m := NewLANManager(inner, st, 0, slog.Default()) // port 0 → ephemeral
	port, err := m.Start(0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.Stop(context.Background())
	if !m.Running() || m.BoundPort() != port {
		t.Fatalf("running=%v boundPort=%d port=%d", m.Running(), m.BoundPort(), port)
	}

	base := fmt.Sprintf("http://127.0.0.1:%d/api/v1/sessions", port)
	// no auth → 401
	resp, _ := http.Get(base)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-auth: got %d want 401", resp.StatusCode)
	}
	// with auth → 200
	req, _ := http.NewRequest(http.MethodGet, base, nil)
	req.Header.Set("Authorization", "Bearer secret12")
	resp2, _ := http.DefaultClient.Do(req)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("auth: got %d want 200", resp2.StatusCode)
	}
}

func TestLANManagerServesHTTP2WithoutTLS(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, r.Proto)
	})
	st := &authState{}
	armTestBearer(st, mobilebridge.HashPassword("secret12"))
	m := NewLANManager(inner, st, 0, slog.Default())
	port, err := m.Start(0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.Stop(context.Background())

	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	client := &http.Client{Transport: &http.Transport{Protocols: protocols}}
	base := fmt.Sprintf("http://127.0.0.1:%d/api/v1/sessions", port)
	resp, err := client.Get(base)
	if err != nil {
		t.Fatalf("h2c without auth: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("h2c without auth: got %d want 401", resp.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodGet, base, nil)
	req.Header.Set("Authorization", "Bearer secret12")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("h2c with auth: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "HTTP/2.0" {
		t.Fatalf("h2c with auth: got %d %q, want 200 over HTTP/2.0", resp.StatusCode, body)
	}
}

func TestLANManagerTunnelOnlyBindsLoopback(t *testing.T) {
	m := NewMobileLAN(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), "h_test", 0, nil)
	armTestBearerLAN(m, mobilebridge.HashPassword("secret12"))
	if _, err := m.StartLoopback(0); err != nil {
		t.Fatal(err)
	}
	defer m.Stop(context.Background())
	if addr := m.ln.Addr().(*net.TCPAddr); !addr.IP.IsLoopback() {
		t.Fatalf("tunnel-only listener bound %s, want loopback", addr)
	}
	if _, err := m.Start(0); err == nil {
		t.Fatal("LAN start silently reused a loopback-only listener")
	}
}

// TestLANManagerIdentityEndpoint verifies the unauthenticated identity probe
// works as expected for mobile pairing (ADR 0003).
func TestLANManagerIdentityEndpoint(t *testing.T) {
	inner := chi.NewRouter()
	inner.Get("/api/v1/identity", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"hostId":"test-host-id","apiVersion":1}`)
	})
	st := &authState{}
	armTestBearer(st, mobilebridge.HashPassword("secret12"))
	m := NewLANManager(inner, st, 0, slog.Default())
	port, err := m.Start(0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.Stop(context.Background())

	// Identity endpoint should be accessible without auth (ADR 0003)
	base := fmt.Sprintf("http://127.0.0.1:%d/api/v1/identity", port)
	resp, err := http.Get(base)
	if err != nil {
		t.Fatalf("identity probe failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("identity probe: got %d want 200", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"hostId":"test-host-id","apiVersion":1}` {
		t.Fatalf("identity probe response: got %s want hostId", string(body))
	}

	// Other endpoints should still require auth
	authBase := fmt.Sprintf("http://127.0.0.1:%d/api/v1/sessions", port)
	resp2, _ := http.Get(authBase)
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("other endpoint without auth: got %d want 401", resp2.StatusCode)
	}
}

func TestLANManagerStartStopIdempotent(t *testing.T) {
	m := NewLANManager(http.NotFoundHandler(), &authState{}, 0, slog.Default())
	p1, _ := m.Start(0)
	p2, _ := m.Start(0) // idempotent — same port, no error
	if p1 != p2 {
		t.Fatalf("second start changed port: %d != %d", p1, p2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := m.Stop(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if m.Running() {
		t.Fatal("still running after stop")
	}
	_ = m.Stop(ctx) // second stop is a no-op
}

// End-to-end through the real LAN stack (lanControlBlock + authMiddleware +
// router): the identity probe answers without a credential, and nothing else
// does. The middleware unit tests cover the exemption in isolation; this covers
// the composition, which is where a wiring mistake would actually live.
func TestLANManagerServesIdentityProbeWithoutAPassword(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	})
	st := &authState{}
	armTestBearer(st, mobilebridge.HashPassword("secret12"))
	m := NewLANManager(inner, st, 0, slog.Default())
	port, err := m.Start(0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.Stop(context.Background())

	get := func(path string) int {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
		resp, err := http.DefaultClient.Do(req) // deliberately no Authorization
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode
	}

	if code := get("/api/v1/identity"); code != http.StatusOK {
		t.Errorf("unauthenticated GET /api/v1/identity got %d, want 200", code)
	}
	if code := get("/api/v1/sessions"); code != http.StatusUnauthorized {
		t.Errorf("unauthenticated GET /api/v1/sessions got %d, want 401", code)
	}
}

// lanFakeAgentCatalog is a controllers.AgentCatalog that records whether the
// real handler was actually reached. Only the Codex model/probe routes are
// exercised; the rest satisfy the interface.
type lanFakeAgentCatalog struct{ calls int }

func (c *lanFakeAgentCatalog) CachedReadiness(context.Context) (agentsvc.Readiness, error) {
	return agentsvc.Readiness{}, nil
}

func (c *lanFakeAgentCatalog) EnsureReadiness(context.Context, []string, domain.AgentReadinessPurpose) (agentsvc.Readiness, error) {
	return agentsvc.Readiness{}, nil
}

func (c *lanFakeAgentCatalog) List(context.Context) (agentsvc.Inventory, error) {
	return agentsvc.Inventory{}, nil
}

func (c *lanFakeAgentCatalog) Refresh(context.Context) (agentsvc.Inventory, error) {
	return agentsvc.Inventory{}, nil
}

func (c *lanFakeAgentCatalog) Probe(context.Context, string) (agentsvc.ProbeResult, error) {
	c.calls++
	return agentsvc.ProbeResult{}, nil
}

func (c *lanFakeAgentCatalog) Models(_ context.Context, agentID, _ string, _ bool) (ports.AgentModelCatalog, error) {
	c.calls++
	return ports.AgentModelCatalog{AgentID: agentID}, nil
}

func (c *lanFakeAgentCatalog) ModelCatalog(ctx context.Context, agentID, projectID string, refresh bool) (ports.AgentModelCatalog, error) {
	return c.Models(ctx, agentID, projectID, refresh)
}

func (c *lanFakeAgentCatalog) RevalidateModels(_ context.Context, agentID, _ string) (ports.AgentModelCatalog, error) {
	c.calls++
	return ports.AgentModelCatalog{AgentID: agentID}, nil
}

// TestLANListenerServesCodexModelRoutesFromRealRouter pins the actual bug: the
// LAN control block used to list the whole /api/v1/agents/codex prefix, so the
// model routes mobile calls answered 404 even though the router mounts them.
// A stub inner handler cannot prove that (it answers anything), so this drives
// the real AgentsController routes through the real LAN listener over a real
// socket and asserts the handler ran, while the credential routes stay blocked.
func TestLANListenerServesCodexModelRoutesFromRealRouter(t *testing.T) {
	catalog := &lanFakeAgentCatalog{}
	router := chi.NewRouter()
	router.Route("/api/v1", func(r chi.Router) {
		(&controllers.AgentsController{Catalog: catalog}).Register(r)
	})

	st := &authState{}
	armTestBearer(st, mobilebridge.HashPassword("secret12"))
	m := NewLANManager(router, st, 0, slog.Default())
	port, err := m.Start(0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.Stop(context.Background())

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/agents/codex/models?projectId=project%20one"},
		{http.MethodPost, "/api/v1/agents/codex/models/refresh"},
	} {
		req, _ := http.NewRequest(tc.method, fmt.Sprintf("http://127.0.0.1:%d%s", port, tc.path), nil)
		req.Header.Set("Authorization", "Bearer secret12")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: request failed: %v", tc.method, tc.path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s %s: got %d (%s) want 200 — LAN block must not swallow Codex model routes", tc.method, tc.path, resp.StatusCode, body)
		}
	}
	if catalog.calls != 2 {
		t.Fatalf("catalog calls = %d, want 2 — requests never reached the real handler", catalog.calls)
	}

	// The credential surface stays unreachable over LAN, even with a spoofed
	// loopback Host and valid auth.
	for _, path := range []string{
		"/api/v1/agents/codex/accounts",
		"/api/v1/agents/codex/accounts/events",
		"/api/v1/agents/codex/account-switches",
	} {
		req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
		req.Host = "127.0.0.1"
		req.Header.Set("Authorization", "Bearer secret12")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: request failed: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s: got %d want 404", path, resp.StatusCode)
		}
	}
}

func TestLANStopDrainsInFlightNonStreamRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	ctxErr := make(chan error, 1)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		ctxErr <- r.Context().Err()
		w.WriteHeader(http.StatusCreated)
	})
	st := &authState{}
	armTestBearer(st, mobilebridge.HashPassword("secret12"))
	m := NewLANManager(streamShutdownMiddleware(inner), st, 0, slog.Default())
	port, err := m.Start(0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	status := make(chan int, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/api/v1/sessions", port), nil)
		req.Header.Set("Authorization", "Bearer secret12")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			status <- 0
			return
		}
		resp.Body.Close()
		status <- resp.StatusCode
	}()
	<-started
	stopped := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		stopped <- m.Stop(ctx)
	}()
	time.Sleep(200 * time.Millisecond)
	close(release)
	if err := <-ctxErr; err != nil {
		t.Fatalf("in-flight request context cancelled by Stop: %v", err)
	}
	if got := <-status; got != http.StatusCreated {
		t.Fatalf("status = %d, want 201", got)
	}
	if err := <-stopped; err != nil {
		t.Fatalf("stop: %v", err)
	}
}

func TestLANStopEndsStreamRoute(t *testing.T) {
	started := make(chan struct{})
	ended := make(chan struct{})
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(ended)
	})
	st := &authState{}
	armTestBearer(st, mobilebridge.HashPassword("secret12"))
	m := NewLANManager(streamShutdownMiddleware(inner), st, 0, slog.Default())
	port, err := m.Start(0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/api/v1/events", port), nil)
	req.Header.Set("Authorization", "Bearer secret12")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer resp.Body.Close()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go m.Stop(ctx)
	select {
	case <-ended:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not end the stream route")
	}
}
