package settingswrite

import (
	"encoding/json"
	"testing"

	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	"tallpine.eu/tallpine/backend/internal/settingsreg"
)

// An agent may turn Security mode on, never off; allowDocker is the user's
// alone. Dropping the securityModeKey guard makes the "off" case pass.
func TestAgentMayOnlyTurnSecurityModeOn(t *testing.T) {
	orch := requestscope.Agent{Role: requestscope.RoleOrchestrator, ProjectID: "app"}
	reg := settingsreg.Default()
	enabled, _ := reg.Lookup("policy.securityMode.enabled")
	docker, _ := reg.Lookup("policy.securityMode.allowDocker")
	cur := json.RawMessage(`false`)
	if why := agentMayWrite(orch, enabled, json.RawMessage(`true`), json.RawMessage(`false`)); why == "" {
		t.Error("an agent turned Security mode off")
	}
	if why := agentMayWrite(orch, enabled, cur, json.RawMessage(`true`)); why != "" {
		t.Errorf("an agent may turn it on: %s", why)
	}
	if why := agentMayWrite(orch, docker, cur, json.RawMessage(`true`)); why == "" {
		t.Error("an agent set allowDocker")
	}
}
