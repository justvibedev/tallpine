package settings_test

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
)

func TestRoleDefaultsUpdateRules(t *testing.T) {
	st := &wdStore{d: domain.WorkflowDefaults{WorkerAgent: domain.HarnessClaudeCode, WorkerModel: "m", WorkerEffort: "high"}}
	svc := settingssvc.New(st, nil, settingssvc.Offering{}, nil)
	ctx := context.Background()
	codex := domain.HarnessCodex
	if _, err := svc.SetWorkflowDefaults(ctx, settingssvc.WorkflowDefaultsUpdate{WorkerAgent: &codex}); err != nil {
		t.Fatal(err)
	}
	if st.d.WorkerAgent != codex || st.d.WorkerModel != "" || st.d.WorkerEffort != "" {
		t.Fatalf("agent change kept the old model/effort: %+v", st.d)
	}
	none := domain.AgentHarness("")
	model := "x"
	if _, err := svc.SetWorkflowDefaults(ctx, settingssvc.WorkflowDefaultsUpdate{WorkerAgent: &none, WorkerModel: &model}); err != nil {
		t.Fatal(err)
	}
	if st.d.WorkerAgent != "" || st.d.WorkerModel != "" {
		t.Fatalf("empty agent must clear the model: %+v", st.d)
	}
	// Orchestrator is independent of the worker.
	opus := "claude-opus-5-5"
	claude := domain.HarnessClaudeCode
	if _, err := svc.SetWorkflowDefaults(ctx, settingssvc.WorkflowDefaultsUpdate{OrchestratorAgent: &claude, OrchestratorModel: &opus}); err != nil {
		t.Fatal(err)
	}
	if st.d.OrchestratorModel != opus || st.d.WorkerAgent != "" {
		t.Fatalf("stored %+v", st.d)
	}
}
