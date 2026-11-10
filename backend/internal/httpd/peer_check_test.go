package httpd

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/daemonsock"
	"tallpine.eu/tallpine/backend/internal/daemontoken"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	"tallpine.eu/tallpine/backend/internal/peeradmit"
)

func reportOnlyChecker(t *testing.T, inspect func(int) (peeradmit.Lineage, error)) *peeradmit.Checker {
	t.Helper()
	checker := peeradmit.New(peeradmit.Options{
		Mode: peeradmit.ModeReport, ModeReason: "test", Inspect: inspect,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go checker.Run(ctx)
	return checker
}

func flushChecker(t *testing.T, checker *peeradmit.Checker) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := checker.Flush(ctx); err != nil {
		t.Fatal(err)
	}
}

func peerCheckTestServer(t *testing.T, peers *peeradmit.Checker) *httptest.Server {
	t.Helper()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Reached", "yes")
		_, _ = io.WriteString(w, "served "+r.URL.Path)
	})
	srv := httptest.NewUnstartedServer(daemonAuthMiddleware(testDaemonToken, "", nil, nil, peers)(inner))
	srv.Config.ConnContext = daemonsock.ContextWithConn
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

type peerCheckRequest struct {
	method, path, token string
	origin              bool
}

func (req peerCheckRequest) send(t *testing.T, srv *httptest.Server) (int, http.Header, string) {
	t.Helper()
	r, err := http.NewRequest(req.method, srv.URL+req.path, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	if req.token != "" {
		r.Header.Set(daemontoken.Header, req.token)
	}
	if req.origin {
		r.Header.Set("Origin", "http://localhost")
		r.Header.Set("Access-Control-Request-Method", "GET")
	}
	resp, err := srv.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	resp.Header.Del("Date")
	return resp.StatusCode, resp.Header, string(body)
}

func TestExemptRoutesServedAndNotRecorded(t *testing.T) {
	checker := reportOnlyChecker(t, nil)
	srv := peerCheckTestServer(t, checker)
	for _, req := range []peerCheckRequest{
		{method: http.MethodGet, path: "/healthz"},
		{method: http.MethodGet, path: "/healthz", token: testDaemonToken},
		{method: http.MethodHead, path: "/readyz"},
		{method: http.MethodHead, path: "/readyz", token: testDaemonToken},
		{method: http.MethodOptions, path: "/api/v1/sessions", origin: true},
		{method: http.MethodOptions, path: "/api/v1/sessions", origin: true, token: testDaemonToken},
	} {
		status, header, _ := req.send(t, srv)
		if status != http.StatusOK || header.Get("X-Reached") != "yes" {
			t.Errorf("%s %s token=%v: status %d, reached %q", req.method, req.path, req.token != "", status, header.Get("X-Reached"))
		}
	}
	flushChecker(t, checker)
	report := checker.Report()
	if len(report.WouldRefuse) != 0 || len(report.Admitted) != 0 {
		t.Fatalf("exempt routes must not be classified: %+v", report)
	}
}

func TestTCPPeerClassifiedNotAdmitted(t *testing.T) {
	checker := reportOnlyChecker(t, nil)
	srv := peerCheckTestServer(t, checker)
	status, header, _ := peerCheckRequest{method: http.MethodGet, path: "/api/v1/sessions", token: testDaemonToken}.send(t, srv)
	if status != http.StatusOK || header.Get("X-Reached") != "yes" {
		t.Fatalf("report mode refused a TCP caller: %d", status)
	}
	flushChecker(t, checker)
	records := checker.Report().WouldRefuse
	if len(records) != 1 || records[0].Reason != peeradmit.ReasonTCPNoIdentity || records[0].TokenClass != "install" {
		t.Fatalf("records = %+v", records)
	}
}

func TestWrongTokenAndLANAreNotClassified(t *testing.T) {
	checker := reportOnlyChecker(t, nil)
	inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	handler := daemonAuthMiddleware(testDaemonToken, "", nil, nil, checker)(inner)

	wrong := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", http.NoBody)
	wrong.Header.Set(daemontoken.Header, "not-the-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, wrong)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d", rec.Code)
	}

	lan := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", http.NoBody)
	lan = lan.WithContext(requestscope.WithLAN(lan.Context()))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, lan)
	if rec.Code != http.StatusOK {
		t.Fatalf("LAN = %d", rec.Code)
	}

	flushChecker(t, checker)
	if records := checker.Report().WouldRefuse; len(records) != 0 {
		t.Fatalf("only verified local tokens are classified: %+v", records)
	}
}

func TestReportModeChangesNoResponse(t *testing.T) {
	requests := []peerCheckRequest{
		{method: http.MethodGet, path: "/api/v1/sessions", token: testDaemonToken},
		{method: http.MethodPost, path: "/shutdown", token: testDaemonToken},
		{method: http.MethodGet, path: "/api/v1/sessions"},
		{method: http.MethodGet, path: "/api/v1/sessions", token: "wrong"},
		{method: http.MethodGet, path: "/healthz"},
	}
	broken := reportOnlyChecker(t, func(int) (peeradmit.Lineage, error) { panic("inspect failed") })
	withoutCheck := peerCheckTestServer(t, nil)
	withCheck := peerCheckTestServer(t, reportOnlyChecker(t, nil))
	withBrokenCheck := peerCheckTestServer(t, broken)
	for _, req := range requests {
		baseStatus, baseHeader, baseBody := req.send(t, withoutCheck)
		for name, srv := range map[string]*httptest.Server{"report": withCheck, "broken": withBrokenCheck} {
			status, header, body := req.send(t, srv)
			if status != baseStatus || body != baseBody || header.Get("Content-Type") != baseHeader.Get("Content-Type") ||
				header.Get("X-Reached") != baseHeader.Get("X-Reached") {
				t.Errorf("%s %s under %s: got %d %q, want %d %q", req.method, req.path, name, status, body, baseStatus, baseBody)
			}
		}
	}
}

func TestScopedTokenClassifiedAfterVerification(t *testing.T) {
	store, tokens := newAgentScopeFixture()
	checker := reportOnlyChecker(t, nil)
	scopes := newAgentScopes(tokens, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	scopes.peers = checker
	inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	handler := daemonAuthMiddleware(testDaemonToken, "", nil, scopes, checker)(inner)

	do := func(token string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/app-1", http.NoBody)
		req.Header.Set(daemontoken.Header, token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := do(store.tokenFor(tokens, "app-1")); got != http.StatusOK {
		t.Fatalf("valid scoped token = %d", got)
	}
	if got := do(tokens.IssueSession("app-1", "forged-verifier")); got != http.StatusUnauthorized {
		t.Fatalf("stale scoped token = %d", got)
	}
	flushChecker(t, checker)
	records := checker.Report().WouldRefuse
	if len(records) != 1 || records[0].TokenClass != "session" || records[0].Count != 1 {
		t.Fatalf("only the verified token must be classified: %+v", records)
	}
}

func TestRealServerReadsSocketPeerIntoContext(t *testing.T) {
	runPath := shortRunPath(t)
	checker := reportOnlyChecker(t, nil)
	cfg := config.Config{Host: "127.0.0.1", Port: 0, ShutdownTimeout: 5 * time.Second, RunFilePath: runPath}
	srv, err := NewWithDeps(cfg, discardLogger(), nil, APIDeps{PeerCheck: checker})
	if err != nil {
		t.Fatal(err)
	}
	if srv.sock == nil {
		t.Skip("no daemon socket on this platform")
	}
	token, err := daemontoken.Read(daemontoken.Path(runPath))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	client, base := daemonClient(t, srv, 2*time.Second)
	waitForHealth(t, client, base)
	req, err := http.NewRequest(http.MethodGet, base+"/api/v1/sessions", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(daemontoken.Header, token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	flushChecker(t, checker)
	records := checker.Report().WouldRefuse
	if len(records) != 1 || records[0].Reason == peeradmit.ReasonTCPNoIdentity || records[0].LastPID != os.Getpid() {
		t.Fatalf("server must read the real socket peer: %+v", records)
	}
}
