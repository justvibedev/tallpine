package cli

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/daemontoken"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd"
	"tallpine.eu/tallpine/backend/internal/sessiontoken"
)

// The CLI against the real router with enforcement on: the token file next
// to running.json is the only thing that makes requests succeed.
func TestCLISendsDaemonTokenFromRunFileDir(t *testing.T) {
	cfg := setConfigEnv(t)
	tokenPath := daemontoken.Path(cfg.runFile)
	token, err := daemontoken.Ensure(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, slog.New(slog.DiscardHandler), nil, httpd.APIDeps{}, httpd.ControlDeps{
		DaemonToken:     token,
		RequestShutdown: func() {},
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	c := &commandContext{deps: Deps{HTTPClient: srv.Client(), ProcessAlive: func(int) bool { return true }}}

	// No project service is wired, so an authenticated call reaches the
	// route and gets its 501; only auth failures produce 401.
	err = c.getJSON(context.Background(), "projects", &struct{}{})
	var apiErr apiResponseError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnauthorized {
		t.Fatalf("CLI request with the token file present was rejected: %v", err)
	}
	if err := c.requestShutdown(context.Background(), "", serverPort(t, srv.URL), cfg.runFile, false); err != nil {
		t.Fatalf("tallpine stop's /shutdown request was rejected: %v", err)
	}

	// Without the file the daemon's 401 reaches the user with its code.
	if err := os.Remove(tokenPath); err != nil {
		t.Fatal(err)
	}
	err = c.getJSON(context.Background(), "projects", &struct{}{})
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized || apiErr.ErrorBody.Code != "DAEMON_TOKEN_REQUIRED" {
		t.Fatalf("missing token file: err = %v, want 401 DAEMON_TOKEN_REQUIRED", err)
	}
}

func TestSetDaemonTokenOmitsHeaderWithoutFile(t *testing.T) {
	cfg := setConfigEnv(t)
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3101/api/v1/projects", nil)
	setDaemonToken(req, cfg.runFile)
	if values := req.Header.Values(daemontoken.Header); len(values) != 0 {
		t.Fatal("no token file must mean no header, not an empty one")
	}
}

type oneSessionScopes struct{ rec domain.SessionRecord }

func (s oneSessionScopes) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	return s.rec, id == s.rec.ID, nil
}

func (oneSessionScopes) GetReviewByID(context.Context, string) (domain.Review, bool, error) {
	return domain.Review{}, false, nil
}

// Inside an agent session the CLI authenticates with the session's scoped
// token, even though the install token file is readable, so the daemon's
// agent policy applies to every `tallpine` call the agent makes.
func TestCLIPrefersScopedSessionToken(t *testing.T) {
	cfg := setConfigEnv(t)
	install, err := daemontoken.Ensure(daemontoken.Path(cfg.runFile))
	if err != nil {
		t.Fatal(err)
	}
	rec := domain.SessionRecord{ID: "app-1", ProjectID: "app", Kind: domain.KindWorker}
	rec.Metadata.BrowserCapabilityVerifier = "launch-verifier"
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, slog.New(slog.DiscardHandler), nil,
		httpd.APIDeps{AgentScopes: oneSessionScopes{rec: rec}},
		httpd.ControlDeps{DaemonToken: install, RequestShutdown: func() {}}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	t.Setenv(sessiontoken.EnvVar, sessiontoken.New(install).IssueSession(rec.ID, "launch-verifier"))
	t.Setenv(sessiontoken.RunFileEnvVar, cfg.runFile)
	c := &commandContext{deps: Deps{HTTPClient: srv.Client(), ProcessAlive: func(int) bool { return true }}}

	// Its own session is in scope: the call reaches the (unwired) route.
	var apiErr apiResponseError
	err = c.getJSON(context.Background(), "sessions/app-1", &struct{}{})
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotImplemented {
		t.Fatalf("own session: err = %v, want the route's 501", err)
	}
	// Daemon control is not, though the install token sits next to running.json.
	if err := c.requestShutdown(context.Background(), "", serverPort(t, srv.URL), cfg.runFile, false); err == nil {
		t.Fatal("an agent's tallpine stop shut the daemon down")
	}
	err = c.getJSON(context.Background(), "settings", &struct{}{})
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusForbidden || apiErr.ErrorBody.Code != "AGENT_SCOPE_FORBIDDEN" {
		t.Fatalf("settings: err = %v, want 403 AGENT_SCOPE_FORBIDDEN", err)
	}
}

// Codex and DeepSeek Harness drop environment variables named like credentials
// (KEY, TOKEN, SECRET, PASSWORD) from the commands they run, so TALLPINE_SESSION_TOKEN
// never reached the CLI there and it fell back to the install token. The alias
// pair carries the same scoped token; the daemon must still apply the agent policy.
func TestCLIUsesScopedTokenWhenAHarnessStrippedTheTokenVariables(t *testing.T) {
	cfg := setConfigEnv(t)
	install, err := daemontoken.Ensure(daemontoken.Path(cfg.runFile))
	if err != nil {
		t.Fatal(err)
	}
	rec := domain.SessionRecord{ID: "app-1", ProjectID: "app", Kind: domain.KindWorker}
	rec.Metadata.BrowserCapabilityVerifier = "launch-verifier"
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, slog.New(slog.DiscardHandler), nil,
		httpd.APIDeps{AgentScopes: oneSessionScopes{rec: rec}},
		httpd.ControlDeps{DaemonToken: install, RequestShutdown: func() {}}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	t.Setenv(sessiontoken.AltEnvVar, sessiontoken.New(install).IssueSession(rec.ID, "launch-verifier"))
	t.Setenv(sessiontoken.AltRunFileEnvVar, cfg.runFile)
	c := &commandContext{deps: Deps{HTTPClient: srv.Client(), ProcessAlive: func(int) bool { return true }}}

	// Its own session is in scope: the call reaches the (unwired) route.
	var apiErr apiResponseError
	err = c.getJSON(context.Background(), "sessions/app-1", &struct{}{})
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotImplemented {
		t.Fatalf("own session: err = %v, want the route's 501", err)
	}
	// Daemon control is not, though the install token sits next to running.json.
	if err := c.requestShutdown(context.Background(), "", serverPort(t, srv.URL), cfg.runFile, false); err == nil {
		t.Fatal("an agent's tallpine stop shut the daemon down")
	}
	err = c.getJSON(context.Background(), "settings", &struct{}{})
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusForbidden || apiErr.ErrorBody.Code != "AGENT_SCOPE_FORBIDDEN" {
		t.Fatalf("settings: err = %v, want 403 AGENT_SCOPE_FORBIDDEN", err)
	}
}

// The scoped token belongs to the daemon that launched the agent. When the
// agent points tallpine at another daemon (an isolated dev daemon with its own
// TALLPINE_RUN_FILE), the CLI uses that daemon's install token, not a token the
// other daemon cannot verify.
func TestCLISendsScopedTokenOnlyToItsIssuingDaemon(t *testing.T) {
	cfg := setConfigEnv(t)
	install, err := daemontoken.Ensure(daemontoken.Path(cfg.runFile))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, slog.New(slog.DiscardHandler), nil,
		httpd.APIDeps{AgentScopes: oneSessionScopes{}}, httpd.ControlDeps{DaemonToken: install, RequestShutdown: func() {}}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	// A token another daemon issued, bound to that daemon's run file.
	t.Setenv(sessiontoken.EnvVar, sessiontoken.New("issuing-daemon-install-token").IssueSession("app-1", "v"))
	t.Setenv(sessiontoken.RunFileEnvVar, filepath.Join(t.TempDir(), "running.json"))
	c := &commandContext{deps: Deps{HTTPClient: srv.Client(), ProcessAlive: func(int) bool { return true }}}

	// The install token from this daemon's run-file dir authenticates: the
	// unwired route answers 501, not 401 SESSION_TOKEN_INVALID.
	var apiErr apiResponseError
	err = c.getJSON(context.Background(), "settings", &struct{}{})
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotImplemented {
		t.Fatalf("call to a daemon that did not issue the token: err = %v, want the route's 501", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3101/api/v1/projects", nil)
	setDaemonToken(req, cfg.runFile)
	if got := req.Header.Get(daemontoken.Header); got != install {
		t.Fatalf("header = %q, want this daemon's install token", got)
	}
	// Without a binding the token is never sent either.
	t.Setenv(sessiontoken.RunFileEnvVar, "")
	req = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3101/api/v1/projects", nil)
	setDaemonToken(req, cfg.runFile)
	if got := req.Header.Get(daemontoken.Header); got != install {
		t.Fatalf("unbound token: header = %q, want the install token", got)
	}
}

// A daemon restart rotates the token: the CLI, which re-reads the file per
// call, recovers; a client still holding the old token is refused.
func TestCLIRecoversAfterTokenRotationAndOldTokenRejected(t *testing.T) {
	cfg := setConfigEnv(t)
	tokenPath := daemontoken.Path(cfg.runFile)
	oldToken, _, err := daemontoken.RotateOnStart(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	newToken, _, err := daemontoken.RotateOnStart(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, slog.New(slog.DiscardHandler), nil, httpd.APIDeps{}, httpd.ControlDeps{
		DaemonToken:     newToken,
		RequestShutdown: func() {},
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	c := &commandContext{deps: Deps{HTTPClient: srv.Client(), ProcessAlive: func(int) bool { return true }}}

	var apiErr apiResponseError
	err = c.getJSON(context.Background(), "projects", &struct{}{})
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnauthorized {
		t.Fatalf("CLI call after rotation was rejected: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3101/api/v1/projects", nil)
	req.Header.Set(daemontoken.Header, oldToken)
	rec := httptest.NewRecorder()
	httpd.NewRouterWithControl(config.Config{}, slog.New(slog.DiscardHandler), nil, httpd.APIDeps{}, httpd.ControlDeps{DaemonToken: newToken}).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("old token status = %d, want 401", rec.Code)
	}
}
