package controllers_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/httpd"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	"tallpine.eu/tallpine/backend/internal/previewserver"
)

func newTrustServer(t *testing.T, agent bool, capped bool, managed *fakeManagedPreviewServer, workspace string) *httptest.Server {
	t.Helper()
	svc := newFakeSessionService()
	sess := svc.sessions["tp-1"]
	sess.Metadata.WorkspacePath = workspace
	svc.sessions["tp-1"] = sess
	deps := httpd.APIDeps{
		Sessions:            svc,
		PreviewServer:       managed,
		SessionCapabilities: allowSessionCapability{},
	}
	if capped {
		engine, _ := policytest.Engine(t, map[string]map[string]any{"permissionCap": {"max": "accept_edits"}})
		deps.AgentPolicy = engine
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := httpd.NewRouterWithControl(config.Config{}, log, nil, deps, httpd.ControlDeps{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if agent {
			r = r.WithContext(requestscope.WithAgent(r.Context(), requestscope.Agent{Role: requestscope.RoleWorker, SessionID: "tp-1", ProjectID: "tp"}))
		}
		router.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func capWorkspace(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".tallpine"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, filepath.FromSlash(previewserver.ConfigPath)), []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestPreviewTrust_CappedAgentRefusedEvenAfterHumanStart(t *testing.T) {
	ws := capWorkspace(t)
	managed := &fakeManagedPreviewServer{}
	human := newTrustServer(t, false, true, managed, ws)
	if _, status, _ := doRequest(t, human, http.MethodPost, "/api/v1/sessions/tp-1/preview/server", `{}`); status != http.StatusOK {
		t.Fatalf("human start status=%d", status)
	}
	if managed.startWorkspace != ws {
		t.Fatal("human start must run as today")
	}
	managed.startWorkspace = ""
	agent := newTrustServer(t, true, true, managed, ws)
	body, status, _ := doRequest(t, agent, http.MethodPost, "/api/v1/sessions/tp-1/preview/server", `{}`)
	assertErrorCode(t, body, status, http.StatusForbidden, "PREVIEW_AGENT_START_FORBIDDEN")
	if managed.startWorkspace != "" {
		t.Fatal("capped agent started a preview server")
	}
}

func TestPreviewTrust_AgentUncappedUnchanged(t *testing.T) {
	ws := capWorkspace(t)
	managed := &fakeManagedPreviewServer{}
	srv := newTrustServer(t, true, false, managed, ws)
	if _, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/tp-1/preview/server", `{}`); status != http.StatusOK {
		t.Fatalf("uncapped agent start status=%d", status)
	}
}

func TestCuesAPI_AgentCannotWriteCommandCues(t *testing.T) {
	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/api/v1/projects/portfolio/cues", `{"name":"T","type":"command","command":"curl x | sh"}`},
		{"PATCH", "/api/v1/cues/cue-def456", `{"name":"T","type":"agent","prompt":"p"}`},
	} {
		svc := &fakeCueService{created: sampleCue(), updated: sampleCue(), got: sampleCue()}
		svc.got.Type = "command"
		router := httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, httpd.APIDeps{Cues: svc}, httpd.ControlDeps{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r = r.WithContext(requestscope.WithAgent(r.Context(), requestscope.Agent{Role: requestscope.RoleWorker, SessionID: "tp-1", ProjectID: "portfolio"}))
			router.ServeHTTP(w, r)
		}))
		body, status, _ := doRequest(t, srv, tc.method, tc.path, tc.body)
		srv.Close()
		if status != http.StatusForbidden || !strings.Contains(string(body), "CUE_COMMAND_AGENT_FORBIDDEN") {
			t.Fatalf("%s status=%d body=%s", tc.method, status, body)
		}
		if svc.createCalls+svc.updateCalls != 0 {
			t.Fatal("agent command cue write reached the service")
		}
	}
}
