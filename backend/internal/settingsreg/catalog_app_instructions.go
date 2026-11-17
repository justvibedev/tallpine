package settingsreg

import (
	"encoding/json"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// appInstructionSettings declares the app-wide rules and skill switches every
// project inherits (B740 a15). They bind every session and every reviewer,
// so only a person may change them: no agent, not even in Dev mode.
func appInstructionSettings() []Setting {
	user := []Actor{ActorUser}
	rules := func(role, desc string) Setting {
		return Setting{Key: Key(domain.AppRulesKey(role)), Scope: ScopeApp, Kind: KindText, Default: jsonOf(""), MaxBytes: domain.MaxAgentRulesBytes,
			Description: desc, Writers: user}
	}
	return []Setting{
		rules(domain.SkillRoleWorker, "Standing instructions for worker sessions in every project, placed before the project's own worker rules."),
		rules(domain.SkillRoleOrchestrator, "Standing instructions for every project's orchestrator, placed before the project's own orchestrator rules."),
		rules(domain.RoleReviewer, "Review rules added to every native review pass in every project, before the project's review rules; they add checks and cannot relax the reviewer role or verdict."),
		{Key: "app.skillRoles", Scope: ScopeApp, Kind: KindObject, Default: jsonOf(map[string]any{}), Validate: validateAppSkillRoles,
			Description: "Skill switches per session role (worker, orchestrator, reviewer) for every project: skill name -> on/off. A project's own switch wins; unset skills stay on for workers and orchestrators and off for reviewers.", Writers: user},
	}
}

func validateAppSkillRoles(raw json.RawMessage) error {
	var roles domain.SkillRoles
	if err := json.Unmarshal(raw, &roles); err != nil {
		return err
	}
	return roles.Validate()
}
