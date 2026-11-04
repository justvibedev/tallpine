package controllers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	projectsvc "tallpine.eu/tallpine/backend/internal/service/project"
)

type policyProjects struct{ known map[domain.ProjectID]bool }

func (p policyProjects) Get(_ context.Context, id domain.ProjectID) (projectsvc.GetResult, error) {
	if !p.known[id] {
		return projectsvc.GetResult{}, errors.New("not found")
	}
	return projectsvc.GetResult{}, nil
}

func policyRouter(t *testing.T, store *policytest.Store, managedJSON string) http.Handler {
	t.Helper()
	path := ""
	if managedJSON != "" {
		path = filepath.Join(t.TempDir(), "policy.json")
		if err := os.WriteFile(path, []byte(managedJSON), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	engine, err := agentpolicy.New(rules.All(), store, agentpolicy.WithManagedFile(path, os.ReadFile),
		agentpolicy.WithEnv(func(string) (string, bool) { return "", false }))
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	(&controllers.AgentPolicyController{Svc: engine, Projects: policyProjects{known: map[domain.ProjectID]bool{"p": true}}, Decisions: policyDecisions{}}).Register(r)
	return r
}

func doPolicy(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func capField(t *testing.T, rec *httptest.ResponseRecorder) controllers.AgentPolicyField {
	t.Helper()
	var got controllers.AgentPolicyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("%v: %s", err, rec.Body)
	}
	for _, r := range got.Rules {
		if r.Name == "permissionCap" {
			return r.Fields[0]
		}
	}
	t.Fatalf("no permissionCap rule: %s", rec.Body)
	return controllers.AgentPolicyField{}
}

const global = "/settings/agent-policy"

func TestAgentPolicyAPI_MetadataDrivenDefaultView(t *testing.T) {
	rec := doPolicy(policyRouter(t, &policytest.Store{}, ""), http.MethodGet, global, "")
	f := capField(t, rec)
	if f.Name != "max" || f.Kind != "enum" || f.Value != "none" || f.Default != "none" || f.Source != "default" || f.Locked || f.ProjectOverride != "tightenOnly" || len(f.Enum) != 3 || f.LabelKey == "" {
		t.Fatalf("%+v", f)
	}
	var got controllers.AgentPolicyResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.SchemaVersion != 2 || got.Rules[0].TitleKey == "" || got.Warnings == nil {
		t.Fatalf("%s", rec.Body)
	}
}

func TestAgentPolicyAPI_UpdateMergesAndValidates(t *testing.T) {
	h := policyRouter(t, &policytest.Store{}, "")
	rec := doPolicy(h, http.MethodPatch, global, `{"rules":{"permissionCap":{"max":"auto"}}}`)
	if f := capField(t, rec); rec.Code != http.StatusOK || f.Value != "auto" || f.Source != "user" {
		t.Fatalf("update = %d %s", rec.Code, rec.Body)
	}
	for _, bad := range []string{`{}`, `{"rules":{}}`, `{"rules":{"nope":{"max":"auto"}}}`, `{"rules":{"permissionCap":{"nope":"auto"}}}`, `{"rules":{"permissionCap":{"max":"accept-edits"}}}`} {
		if rec := doPolicy(h, http.MethodPatch, global, bad); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "AGENT_POLICY_INVALID") {
			t.Errorf("%s = %d %s, want 400", bad, rec.Code, rec.Body)
		}
	}
}

func TestAgentPolicyAPI_ManagedFieldIsLocked(t *testing.T) {
	store := &policytest.Store{}
	h := policyRouter(t, store, `{"schemaVersion":2,"rules":{"permissionCap":{"max":"accept_edits"}}}`)
	rec := doPolicy(h, http.MethodPatch, global, `{"rules":{"permissionCap":{"max":"none"}}}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "POLICY_LOCKED") || !strings.Contains(rec.Body.String(), "permissionCap.max") || len(store.Data) != 0 {
		t.Fatalf("locked update = %d %s data=%s", rec.Code, rec.Body, store.Data)
	}
	if f := capField(t, doPolicy(h, http.MethodGet, global, "")); f.Source != "managed" || !f.Locked || f.Value != "accept_edits" {
		t.Fatalf("%+v", f)
	}
}

func TestAgentPolicyAPI_ProjectOverrides(t *testing.T) {
	h := policyRouter(t, &policytest.Store{}, "")
	if rec := doPolicy(h, http.MethodGet, "/projects/missing/agent-policy", ""); rec.Code == http.StatusOK {
		t.Fatalf("an unknown project must not get a policy: %d", rec.Code)
	}
	doPolicy(h, http.MethodPatch, global, `{"rules":{"permissionCap":{"max":"auto"}}}`)
	rec := doPolicy(h, http.MethodPatch, "/projects/p/agent-policy", `{"rules":{"permissionCap":{"max":"accept_edits"}}}`)
	if f := capField(t, rec); rec.Code != http.StatusOK || f.Value != "accept_edits" || f.Source != "project" {
		t.Fatalf("tightening override = %d %s", rec.Code, rec.Body)
	}
	if rec := doPolicy(h, http.MethodPatch, "/projects/p/agent-policy", `{"rules":{"permissionCap":{"max":"none"}}}`); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "tighten") {
		t.Fatalf("a loosening override must be refused: %d %s", rec.Code, rec.Body)
	}
	if f := capField(t, doPolicy(h, http.MethodGet, global, "")); f.Value != "auto" || f.Source != "user" {
		t.Fatalf("the global view must not see the project's override: %+v", f)
	}
}

type policyDecisions struct{}

func (policyDecisions) SessionPolicyDecision(_ context.Context, id domain.SessionID) ([]byte, time.Time, bool, error) {
	if id != "p-1" {
		return nil, time.Time{}, false, nil
	}
	return []byte(`{"v":2,"rules":{"permissionCap":{"effective":"auto"}}}`), time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC), true, nil
}

func TestAgentPolicyAPI_SessionDecisionRecord(t *testing.T) {
	h := policyRouter(t, &policytest.Store{}, "")
	rec := doPolicy(h, http.MethodGet, "/sessions/p-1/agent-policy", "")
	var got controllers.SessionPolicyDecisionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK || got.SessionID != "p-1" || !strings.Contains(rec.Body.String(), `"effective":"auto"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := doPolicy(h, http.MethodGet, "/sessions/old-1/agent-policy", ""); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "POLICY_DECISION_NOT_FOUND") {
		t.Fatalf("a session with no record = %d %s", rec.Code, rec.Body)
	}
}

// A floor Security mode raised comes back through the API with source
// security_mode, and the view carries the switch itself.
func TestAgentPolicyAPI_SecurityModeFloorRoundTrips(t *testing.T) {
	h := policyRouter(t, &policytest.Store{}, "")
	if rec := doPolicy(h, http.MethodPatch, global, `{"rules":{"securityMode":{"enabled":true}}}`); rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var got controllers.AgentPolicyResponse
	if err := json.Unmarshal(doPolicy(h, http.MethodGet, global, "").Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.SecurityMode.Enabled || got.SecurityMode.Source != "user" {
		t.Fatalf("view: %+v", got.SecurityMode)
	}
	for _, r := range got.Rules {
		if r.Name != "protectState" {
			continue
		}
		if f := r.Fields[0]; f.Value != "on" || f.Source != "security_mode" {
			t.Fatalf("protectState.mode = %+v", f)
		}
		return
	}
	t.Fatal("no protectState rule")
}
