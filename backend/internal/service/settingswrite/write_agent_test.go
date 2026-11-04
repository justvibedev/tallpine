package settingswrite

import (
	"encoding/json"
	"testing"

	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	"tallpine.eu/tallpine/backend/internal/settingsreg"
)

func TestDevModeAgentWrites(t *testing.T) {
	reg := settingsreg.Default()
	protect, _ := reg.Lookup("policy.protectState.mode")
	dev, _ := reg.Lookup("policy.devMode.enabled")
	off, on := json.RawMessage(`"off"`), json.RawMessage(`"on"`)

	orch := requestscope.Agent{Role: requestscope.RoleOrchestrator, ProjectID: "app"}
	if agentMayWrite(orch, protect, on, off) == "" {
		t.Error("without Dev mode an agent must not loosen protectState")
	}
	if agentMayChange(orch, "web") == nil {
		t.Error("without Dev mode an agent must not change another project")
	}
	orch.DevMode = true
	if why := agentMayWrite(orch, protect, on, off); why != "" {
		t.Errorf("Dev mode: protectState write refused: %s", why)
	}
	if err := agentMayChange(orch, "web"); err != nil {
		t.Errorf("Dev mode: other project refused: %v", err)
	}
	if agentMayWrite(orch, dev, json.RawMessage(`true`), json.RawMessage(`false`)) == "" {
		t.Error("an agent must never change Dev mode itself")
	}
	env, ok := reg.Lookup("project.env")
	if !ok || !env.Sensitive {
		t.Fatal("project.env must be a sensitive setting")
	}
	cur, drop := json.RawMessage(`{"TOKEN":"s3cret"}`), json.RawMessage(`{"OTHER":"1"}`)
	if agentMayWrite(orch, env, cur, drop) == "" {
		t.Error("Dev mode: dropping a hidden project.env entry must be refused")
	}
	if why := agentMayWrite(orch, env, cur, json.RawMessage(`{"TOKEN":"s3cret","OTHER":"1"}`)); why != "" {
		t.Errorf("Dev mode: adding a project.env entry refused: %s", why)
	}
	worker := requestscope.Agent{Role: requestscope.RoleWorker, ProjectID: "app", DevMode: true}
	if agentMayChange(worker, "app") == nil {
		t.Error("a worker never changes settings")
	}
}
