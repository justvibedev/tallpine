package settingswrite

import (
	"context"
	"encoding/json"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
	"tallpine.eu/tallpine/backend/internal/settingsreg"
)

// setWorkflowDefault writes one app-wide workflow default (B525).
func (w *Writer) setWorkflowDefault(ctx context.Context, key settingsreg.Key, raw json.RawMessage, plan []planned) error {
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
