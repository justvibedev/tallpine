package settingswrite

import (
	"context"
	"encoding/json"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
	"tallpine.eu/tallpine/backend/internal/settingsreg"
)

// setWorkflowDefault writes one app-wide workflow default (B525).
func (w *Writer) setWorkflowDefault(ctx context.Context, key settingsreg.Key, raw json.RawMessage, plan []planned) error {
	// The app rules and skill switches (B740) live in the same stored object.
	if !strings.HasPrefix(string(key), "app.workflowDefaults.") {
		return w.setAppInstruction(ctx, key, raw)
	}
	setter, ok := w.AppSettings.(interface {
		SetWorkflowDefaults(ctx context.Context, update settingssvc.WorkflowDefaultsUpdate) (settingssvc.Snapshot, error)
	})
	if !ok {
		return appError(ports.ErrChatUnsupported)
	}
	var update settingssvc.WorkflowDefaultsUpdate
	switch key {
	case "app.workflowDefaults.planningMode":
		var mode domain.PlanningMode
		_ = json.Unmarshal(raw, &mode)
		update.PlanningMode = &mode
	case "app.workflowDefaults.planAutoApprove":
		var on bool
		_ = json.Unmarshal(raw, &on)
		update.PlanAutoApprove = &on
	case "app.workflowDefaults.reviewerHarness":
		var h domain.ReviewerHarness
		_ = json.Unmarshal(raw, &h)
		update.ReviewerHarness = &h
		// A batch that also sets the model applies both together, so the
		// outcome does not depend on which key comes first.
		for _, p := range plan {
			if p.def.Key == "app.workflowDefaults.reviewerModel" {
				var m string
				_ = json.Unmarshal(p.value, &m)
				update.ReviewerModel = &m
			}
		}
	case "app.workflowDefaults.reviewerModel":
		var m string
		_ = json.Unmarshal(raw, &m)
		update.ReviewerModel = &m
	case "app.workflowDefaults.workerAgent", "app.workflowDefaults.workerModel", "app.workflowDefaults.workerEffort":
		fillRoleUpdate(&update.WorkerAgent, &update.WorkerModel, &update.WorkerEffort, "worker", key, raw, plan)
	case "app.workflowDefaults.orchestratorAgent", "app.workflowDefaults.orchestratorModel", "app.workflowDefaults.orchestratorEffort":
		fillRoleUpdate(&update.OrchestratorAgent, &update.OrchestratorModel, &update.OrchestratorEffort, "orchestrator", key, raw, plan)
	default:
		var on bool
		_ = json.Unmarshal(raw, &on)
		update.AutoReview = &on
	}
	if _, err := setter.SetWorkflowDefaults(ctx, update); err != nil {
		return appError(err)
	}
	return nil
}

// setDynamicContext writes the orchestrator research-mode toggle.
func (w *Writer) setDynamicContext(ctx context.Context, raw json.RawMessage) error {
	var on bool
	_ = json.Unmarshal(raw, &on)
	setter, ok := w.AppSettings.(interface {
		SetDynamicOrchestratorContext(ctx context.Context, enabled bool) (settingssvc.Snapshot, error)
	})
	if !ok {
		return appError(ports.ErrChatUnsupported)
	}
	if _, err := setter.SetDynamicOrchestratorContext(ctx, on); err != nil {
		return appError(err)
	}
	return nil
}

// fillRoleUpdate sets one role's update from key. Writing the agent also takes
// the model and effort of the same batch, so the outcome does not depend on
// which key comes first (a changed agent would otherwise drop them).
func fillRoleUpdate(agent **domain.AgentHarness, model, effort **string, role string, key settingsreg.Key, raw json.RawMessage, plan []planned) {
	prefix := "app.workflowDefaults." + role
	setString := func(dst **string, v json.RawMessage) {
		var s string
		_ = json.Unmarshal(v, &s)
		*dst = &s
	}
	switch string(key) {
	case prefix + "Agent":
		var h domain.AgentHarness
		_ = json.Unmarshal(raw, &h)
		*agent = &h
		for _, p := range plan {
			switch string(p.def.Key) {
			case prefix + "Model":
				setString(model, p.value)
			case prefix + "Effort":
				setString(effort, p.value)
			}
		}
	case prefix + "Model":
		setString(model, raw)
	case prefix + "Effort":
		setString(effort, raw)
	}
}

// setAppInstruction writes one app-wide rules text or the app skill switches
// (B740 a15); they live with the workflow defaults.
func (w *Writer) setAppInstruction(ctx context.Context, key settingsreg.Key, raw json.RawMessage) error {
	setter, ok := w.AppSettings.(interface {
		SetWorkflowDefaults(ctx context.Context, update settingssvc.WorkflowDefaultsUpdate) (settingssvc.Snapshot, error)
	})
	if !ok {
		return appError(ports.ErrChatUnsupported)
	}
	var update settingssvc.WorkflowDefaultsUpdate
	if key == "app.skillRoles" {
		var roles domain.SkillRoles
		_ = json.Unmarshal(raw, &roles)
		update.SkillRoles = &roles
	} else {
		var text string
		_ = json.Unmarshal(raw, &text)
		switch key {
		case "app.workerRules":
			update.WorkerRules = &text
		case "app.orchestratorRules":
			update.OrchestratorRules = &text
		default:
			update.ReviewerRules = &text
		}
	}
	if _, err := setter.SetWorkflowDefaults(ctx, update); err != nil {
		return appError(err)
	}
	return nil
}
