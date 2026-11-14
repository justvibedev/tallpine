package settingswrite

import (
	"context"
	"encoding/json"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
	"tallpine.eu/tallpine/backend/internal/settingsreg"
)

// A role's agent, model and effort are written together whichever key comes first.
func TestRoleDefaultWrittenTogether(t *testing.T) {
	mk := func(k, v string) planned {
		return planned{def: settingsreg.Setting{Key: settingsreg.Key("app.workflowDefaults.orchestrator" + k)}, value: json.RawMessage(v)}
	}
	a, m, e := mk("Agent", `"claude-code"`), mk("Model", `"claude-opus-5-5"`), mk("Effort", `"medium"`)
	for name, plan := range map[string][]planned{"agent first": {a, m, e}, "model first": {m, e, a}, "effort first": {e, a, m}} {
		st := &reviewerDefaultStore{d: domain.WorkflowDefaults{OrchestratorAgent: domain.HarnessCodex, OrchestratorModel: "old"}}
		w := &Writer{AppSettings: settingssvc.New(st, nil, settingssvc.Offering{}, nil)}
		for _, p := range plan {
			if err := w.setWorkflowDefault(context.Background(), p.def.Key, p.value, plan); err != nil {
				t.Fatal(err)
			}
		}
		if st.d.OrchestratorAgent != domain.HarnessClaudeCode || st.d.OrchestratorModel != "claude-opus-5-5" || st.d.OrchestratorEffort != "medium" {
			t.Fatalf("%s: stored %+v", name, st.d)
		}
	}
}
