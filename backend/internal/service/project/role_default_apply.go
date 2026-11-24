package project

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

// RoleValue is the agent, model and effort a role runs with.
type RoleValue struct {
	Agent  string `json:"agent"`
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

// RoleDefaultChange says how applying the app default rewrites a project:
// "clear" empties its own model and effort so it inherits the default, "set"
// writes the default's values because a project-wide model or effort would
// otherwise still win over it.
type RoleDefaultChange string

// The two ways applying the default rewrites a project.
const (
	RoleDefaultClear RoleDefaultChange = "clear"
	RoleDefaultSet   RoleDefaultChange = "set"
)

// RoleDefaultDrift is one project whose role does not run with the app default.
type RoleDefaultDrift struct {
	ProjectID domain.ProjectID  `json:"projectId"`
	Name      string            `json:"name"`
	Current   RoleValue         `json:"current"`
	Change    RoleDefaultChange `json:"change"`
}

// RoleDefaultDriftReport lists, for one role, the app default and every
// project whose role runs with something else.
type RoleDefaultDriftReport struct {
	Role     domain.DefaultRole `json:"role"`
	Default  RoleValue          `json:"default"`
	Projects []RoleDefaultDrift `json:"projects"`
}

// ApplyRoleDefaultInput names the projects to bring in line with the app
// default; empty means every project the drift report lists.
type ApplyRoleDefaultInput struct {
	ProjectIDs []domain.ProjectID `json:"projectIds,omitempty"`
	// Actor names who made the change; set by the caller, never decoded.
	Actor string `json:"-"`
}

// RoleDefaultApplyFailure is one project the apply could not change.
type RoleDefaultApplyFailure struct {
	ProjectID domain.ProjectID `json:"projectId"`
	Error     string           `json:"error"`
}

// ApplyRoleDefaultResult reports which projects now follow the app default.
type ApplyRoleDefaultResult struct {
	Role    domain.DefaultRole        `json:"role"`
	Applied []domain.ProjectID        `json:"applied"`
	Failed  []RoleDefaultApplyFailure `json:"failed"`
}

// RoleDefaultDrift lists the projects whose role, at its next spawn, would not
// run with the app-wide default agent, model and effort (B847).
func (m *Service) RoleDefaultDrift(ctx context.Context, role domain.DefaultRole) (RoleDefaultDriftReport, error) {
	def, rows, err := m.roleDefaultInputs(ctx, role)
	if err != nil {
		return RoleDefaultDriftReport{}, err
	}
	report := RoleDefaultDriftReport{Role: role, Default: roleValue(def.Agent, domain.AgentConfig{Model: def.Model, Effort: def.Effort}), Projects: []RoleDefaultDrift{}}
	for _, row := range rows {
		if d, ok := m.roleDrift(row, role, def); ok {
			report.Projects = append(report.Projects, d)
		}
	}
	return report, nil
}

// ApplyRoleDefault rewrites each drifting project (of in.ProjectIDs, or all of
// them) through SetRoleSettings so its role runs with the app default. A
// project that cannot be changed (a managed lock, an invalid config) is
// reported and the rest still go ahead.
func (m *Service) ApplyRoleDefault(ctx context.Context, role domain.DefaultRole, in ApplyRoleDefaultInput) (ApplyRoleDefaultResult, error) {
	def, rows, err := m.roleDefaultInputs(ctx, role)
	if err != nil {
		return ApplyRoleDefaultResult{}, err
	}
	wanted := map[domain.ProjectID]bool{}
	for _, id := range in.ProjectIDs {
		wanted[id] = true
	}
	result := ApplyRoleDefaultResult{Role: role, Applied: []domain.ProjectID{}, Failed: []RoleDefaultApplyFailure{}}
	for _, row := range rows {
		d, ok := m.roleDrift(row, role, def)
		if !ok || (len(wanted) > 0 && !wanted[d.ProjectID]) {
			continue
		}
		patch := roleDefaultPatch(d.Change, def, d.Current.Agent != string(def.Agent))
		set := SetRoleSettingsInput{Actor: in.Actor}
		switch role {
		case domain.DefaultRoleOrchestrator:
			set.Orchestrator = patch
		case domain.DefaultRoleWorker:
			set.Worker = patch
		case domain.DefaultRoleReviewer:
			set.Reviewer = patch
		}
		if _, err := m.SetRoleSettings(ctx, d.ProjectID, set); err != nil {
			result.Failed = append(result.Failed, RoleDefaultApplyFailure{ProjectID: d.ProjectID, Error: err.Error()})
			continue
		}
		result.Applied = append(result.Applied, d.ProjectID)
	}
	m.logger.Info("app role default applied to projects", "role", string(role), "applied", len(result.Applied), "failed", len(result.Failed))
	return result, nil
}

// roleDefaultInputs reads the role's app default and every project.
func (m *Service) roleDefaultInputs(ctx context.Context, role domain.DefaultRole) (domain.RoleModelDefault, []domain.ProjectRecord, error) {
	if !role.Valid() {
		return domain.RoleModelDefault{}, nil, apierr.Invalid("INVALID_ROLE", "role must be orchestrator, worker or reviewer", nil)
	}
	app, err := m.appWorkflowDefaults(ctx)
	if err != nil {
		return domain.RoleModelDefault{}, nil, projectStoreError(err, "PROJECT_ROLE_DEFAULTS_FAILED", "Failed to read the app role defaults")
	}
	def := app.RoleModelDefault(role)
	if def.Agent == "" {
		return domain.RoleModelDefault{}, nil, apierr.Invalid("NO_ROLE_DEFAULT", "set an app default agent for the "+string(role)+" first", nil)
	}
	rows, err := m.store.ListProjects(ctx)
	if err != nil {
		return domain.RoleModelDefault{}, nil, projectStoreError(err, "PROJECTS_LIST_FAILED", "Failed to load projects")
	}
	return def, rows, nil
}

// roleDrift reports what row's role runs with when that is not the default.
func (m *Service) roleDrift(row domain.ProjectRecord, role domain.DefaultRole, def domain.RoleModelDefault) (RoleDefaultDrift, bool) {
	cfg := row.Config
	var agent domain.AgentHarness
	var effective domain.AgentConfig
	change := RoleDefaultClear
	if role == domain.DefaultRoleReviewer {
		// No reviewer entry follows the app default reviewer already.
		if len(cfg.Reviewers) == 0 {
			return RoleDefaultDrift{}, false
		}
		r := cfg.Reviewers[0]
		agent = domain.AgentHarness(r.Harness)
		effective = def.Fill(agent, r.AgentConfig)
	} else {
		ro := cfg.Worker
		if role == domain.DefaultRoleOrchestrator {
			ro = cfg.Orchestrator
		}
		agent = ro.Harness
		if agent == "" {
			agent = m.defaultHarness
		}
		effective = def.Fill(agent, cfg.RoleAgentConfig(agent, ro, m.defaultHarness))
		// After the clear, a project-wide model or effort would still win over
		// the inherited default; then the default is written explicitly.
		cleared := ro
		cleared.Harness, cleared.AgentConfig.Model, cleared.AgentConfig.Effort = def.Agent, "", ""
		if base := cfg.RoleAgentConfig(def.Agent, cleared, m.defaultHarness); base.Model != "" || base.Effort != "" {
			change = RoleDefaultSet
		}
	}
	current := roleValue(agent, effective)
	if current == roleValue(def.Agent, domain.AgentConfig{Model: def.Model, Effort: def.Effort}) {
		return RoleDefaultDrift{}, false
	}
	return RoleDefaultDrift{ProjectID: domain.ProjectID(row.ID), Name: projectDisplayName(row), Current: current, Change: change}, true
}

// roleDefaultPatch is the SetRoleSettings patch for one drifting project. The
// agent is written only when it differs, so an unpinned role stays unpinned.
func roleDefaultPatch(change RoleDefaultChange, def domain.RoleModelDefault, pinAgent bool) *RoleSettingsPatch {
	model, effort := "", ""
	if change == RoleDefaultSet {
		model, effort = def.Model, def.Effort
	}
	p := &RoleSettingsPatch{Model: &model, Effort: &effort}
	if pinAgent {
		agent := string(def.Agent)
		p.Agent = &agent
	}
	return p
}

func roleValue(agent domain.AgentHarness, cfg domain.AgentConfig) RoleValue {
	return RoleValue{Agent: string(agent), Model: cfg.Model, Effort: cfg.Effort}
}
