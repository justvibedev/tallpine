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
func (w *Writer) setWorkflowDefault(ctx context.Context, key settingsreg.Key, raw json.RawMessage) error {
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
