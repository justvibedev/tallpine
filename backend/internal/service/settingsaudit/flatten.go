package settingsaudit

import (
	"encoding/json"

	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/settingsreg"
)

// ProjectConfig flattens a project config into its project.* keys.
func ProjectConfig(cfg domain.ProjectConfig) Values {
	return JSONObject("project.", cfg)
}

// PolicyLayer flattens the values one layer (user or project) sets in an
// agent-policy view into policy.* keys; a field the layer does not set is null,
// so setting and clearing an override are both recorded.
func PolicyLayer(view agentpolicy.View, layer agentpolicy.Source) Values {
	out := Values{}
	for _, rule := range view.Rules {
		for _, f := range rule.Fields {
			key := settingsreg.Key("policy." + rule.Name + "." + f.Name)
			out[key] = json.RawMessage("null")
			if f.Source == layer {
				out[key] = f.Value
			}
		}
	}
	return out
}

// JSONObject flattens v's top-level JSON fields under prefix; a field omitted
// as empty counts as null, as on the other side of a diff.
func JSONObject(prefix string, v any) Values {
	raw, err := json.Marshal(v)
	if err != nil {
		return Values{}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return Values{}
	}
	out := make(Values, len(fields))
	for name, value := range fields {
		out[settingsreg.Key(prefix+name)] = value
	}
	return out
}

// AppSettings flattens the app settings snapshot into its app.* keys.
func AppSettings(s settingssvc.Snapshot) Values {
	raw := func(v any) json.RawMessage {
		b, _ := json.Marshal(v)
		return b
	}
	return Values{
		"app.defaultSessionMode":                      raw(s.DefaultSessionMode),
		"app.harnessDefaults":                         raw(s.HarnessDefaults),
		"app.chatHibernation.enabled":                 raw(s.ChatHibernation.Enabled),
		"app.chatHibernation.workerIdleSeconds":       raw(int64(s.ChatHibernation.WorkerIdle.Seconds())),
		"app.chatHibernation.orchestratorIdleSeconds": raw(int64(s.ChatHibernation.OrchestratorIdle.Seconds())),
		"app.versionRefresh":                          raw(s.VersionRefresh),
		"app.dynamicOrchestratorContext":              raw(s.DynamicOrchestratorContext),
		"app.workflowDefaults.autoReview":             raw(s.WorkflowDefaults.AutoReview),
		"app.workflowDefaults.planningMode":           raw(s.WorkflowDefaults.PlanningMode),
		"app.workflowDefaults.planAutoApprove":        raw(s.WorkflowDefaults.PlanAutoApprove),
		"app.workflowDefaults.reviewerHarness":        raw(s.WorkflowDefaults.ReviewerHarness),
		"app.workflowDefaults.reviewerModel":          raw(s.WorkflowDefaults.ReviewerModel),
		"app.workflowDefaults.workerAgent":            raw(s.WorkflowDefaults.WorkerAgent),
		"app.workflowDefaults.workerModel":            raw(s.WorkflowDefaults.WorkerModel),
		"app.workflowDefaults.workerEffort":           raw(s.WorkflowDefaults.WorkerEffort),
		"app.workflowDefaults.orchestratorAgent":      raw(s.WorkflowDefaults.OrchestratorAgent),
		"app.workflowDefaults.orchestratorModel":      raw(s.WorkflowDefaults.OrchestratorModel),
		"app.workflowDefaults.orchestratorEffort":     raw(s.WorkflowDefaults.OrchestratorEffort),
		"app.workerRules":                             raw(s.WorkflowDefaults.WorkerRules),
		"app.orchestratorRules":                       raw(s.WorkflowDefaults.OrchestratorRules),
		"app.reviewerRules":                           raw(s.WorkflowDefaults.ReviewerRules),
		"app.skillRoles":                              raw(skillRolesOrEmpty(s.WorkflowDefaults.SkillRoles)),
		"app.storageCompaction":                       raw(s.StorageCompaction),
		"app.providerEventRetentionDays":              raw(s.ProviderEventRetentionDays),
		"app.daemon.peerCheck":                        raw(settingssvc.PeerCheckOrDefault(s.PeerCheck)),
		"app.machineLoad.maxReviewers":                raw(s.MachineMaxReviewers),
		"app.machineLoad.loadPercent":                 raw(s.MachineLoadPercent),
		// Mobile state lives in its own file; only the managed switch is visible
		// here, and it reads "allowed" unless the managed file says otherwise.
		"app.mobile.enabled": raw(true),
	}
}

// skillRolesOrEmpty reads unset switches as {}, the setting's default.
func skillRolesOrEmpty(r domain.SkillRoles) domain.SkillRoles {
	if r == nil {
		return domain.SkillRoles{}
	}
	return r
}
