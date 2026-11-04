package controllers_test

import (
	"net/http"
	"strings"
	"testing"
)

// Through the real router and controllers: with Dev mode on an orchestrator
// reads and acts across projects; a worker stays scoped; no agent changes
// Dev mode, however the key is spelled; a write that merely mentions it works.
func TestDevModeOrchestratorActsAcrossProjects(t *testing.T) {
	f := newScopeFixtureWith(t, map[string]map[string]any{"devMode": {"enabled": true}})
	orch, worker := f.as("app-orch"), f.as("app-1")

	if got := strings.Join(f.listedIDs(orch, "/api/v1/sessions?project=web"), ","); got != "web-1,web-orch" {
		t.Errorf("dev orchestrator lists %q, want web-1,web-orch", got)
	}
	if status, body := f.do(orch, "POST", "/api/v1/sessions", `{"projectId":"web"}`); status != http.StatusCreated {
		t.Errorf("spawn in another project = %d %s", status, body)
	}
	if status, body := f.do(orch, "POST", "/api/v1/sessions/cleanup?project=web", ""); status != http.StatusOK {
		t.Errorf("cleanup of another project = %d %s", status, body)
	}
	if status, body := f.do(worker, "POST", "/api/v1/sessions", `{"projectId":"web"}`); status != http.StatusForbidden {
		t.Errorf("worker spawn = %d %s, want 403", status, body)
	}
	if got := strings.Join(f.listedIDs(worker, "/api/v1/orchestrators"), ","); got != "app-orch" {
		t.Errorf("worker sees %q, want app-orch only", got)
	}
	// Protect state may be set; Dev mode may not, even with an escaped key.
	if status, body := f.do(orch, "PATCH", "/api/v1/settings/agent-policy", `{"rules":{"protectState":{"mode":"off"}}}`); status != http.StatusOK {
		t.Errorf("protectState update = %d %s", status, body)
	}
	for _, body := range []string{`{"rules":{"devMode":{"enabled":false}}}`, `{"rules":{"dev\u004dode":{"enabled":false}}}`} {
		if status, out := f.do(orch, "PATCH", "/api/v1/settings/agent-policy", body); status != http.StatusForbidden {
			t.Errorf("devMode update %s = %d %s, want 403", body, status, out)
		}
	}
	if status, body := f.do(orch, "POST", "/api/v1/sessions", `{"displayName":"fix devMode docs"}`); status != http.StatusCreated {
		t.Errorf("spawn mentioning devMode = %d %s", status, body)
	}
}

func TestDevModeOffKeepsOrchestratorInItsProject(t *testing.T) {
	f := newScopeFixtureWith(t, map[string]map[string]any{})
	orch := f.as("app-orch")
	if status, body := f.do(orch, "POST", "/api/v1/sessions", `{"projectId":"web"}`); status != http.StatusForbidden {
		t.Errorf("spawn in another project = %d %s, want 403", status, body)
	}
	if status, body := f.do(orch, "POST", "/api/v1/sessions/cleanup?project=web", ""); status != http.StatusForbidden {
		t.Errorf("cleanup of another project = %d %s, want 403", status, body)
	}
}

// A Dev-mode orchestrator discovers every project and merges another project's
// PR; with Dev mode off both stay refused.
func TestDevModeOrchestratorReachesOtherProjectsPRsAndList(t *testing.T) {
	merge := `{"prUrl":"https://github.com/o/web/pull/9","expectedHeadSha":"abc"}`
	on := newScopeFixtureWith(t, map[string]map[string]any{"devMode": {"enabled": true}})
	off := newScopeFixtureWith(t, map[string]map[string]any{})
	if got := strings.Join(on.listedIDs(on.as("app-orch"), "/api/v1/projects"), ","); got != "app,web" {
		t.Errorf("dev projects = %q, want app,web", got)
	}
	if got := strings.Join(off.listedIDs(off.as("app-orch"), "/api/v1/projects"), ","); got != "app" {
		t.Errorf("off projects = %q, want app", got)
	}
	if status, body := on.do(on.as("app-orch"), "POST", "/api/v1/prs/9/merge", merge); status != http.StatusOK {
		t.Errorf("dev merge of another project's PR = %d %s", status, body)
	}
	if status, body := off.do(off.as("app-orch"), "POST", "/api/v1/prs/9/merge", merge); status != http.StatusForbidden {
		t.Errorf("off merge = %d %s, want 403", status, body)
	}
}
