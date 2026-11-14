package controllers

import (
	"net/http"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
)

func hasRoleDefaults(req UpdateWorkflowDefaultsRequest) bool {
	return req.WorkerAgent != nil || req.WorkerModel != nil || req.WorkerEffort != nil ||
		req.OrchestratorAgent != nil || req.OrchestratorModel != nil || req.OrchestratorEffort != nil
}

// setRoleDefaults validates the worker and orchestrator defaults of a request
// into update, writing a 400 and returning false when one is invalid.
func (c *SettingsController) setRoleDefaults(w http.ResponseWriter, r *http.Request, req UpdateWorkflowDefaultsRequest, update *settingssvc.WorkflowDefaultsUpdate) bool {
	for _, role := range []struct {
		name         string
		agent, model *string
		effort       *string
		dstA         **domain.AgentHarness
		dstM, dstE   **string
	}{
		{"worker", req.WorkerAgent, req.WorkerModel, req.WorkerEffort, &update.WorkerAgent, &update.WorkerModel, &update.WorkerEffort},
		{"orchestrator", req.OrchestratorAgent, req.OrchestratorModel, req.OrchestratorEffort, &update.OrchestratorAgent, &update.OrchestratorModel, &update.OrchestratorEffort},
	} {
		if role.agent != nil {
			h := domain.AgentHarness(*role.agent)
			if h != "" && !h.IsKnown() {
				envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
					"WORKFLOW_DEFAULTS_INVALID", role.name+"Agent must be a supported agent or empty", nil)
				return false
			}
			*role.dstA = &h
		}
		if role.effort != nil && *role.effort != "" && !domain.ValidEffort(*role.effort) {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
				"WORKFLOW_DEFAULTS_INVALID", role.name+"Effort is not a known effort level", nil)
			return false
		}
		if role.model != nil && len(*role.model) > 200 {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
				"WORKFLOW_DEFAULTS_INVALID", role.name+"Model is too long", nil)
			return false
		}
		*role.dstM, *role.dstE = role.model, role.effort
	}
	return true
}
