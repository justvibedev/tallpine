package project

import (
	"context"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

// RoleSettingsPatch is one role's harness, model and effort. A nil field is
// left unchanged; an empty string clears it back to the inherited default.
type RoleSettingsPatch struct {
	Agent  *string `json:"agent,omitempty"`
	Model  *string `json:"model,omitempty"`
	Effort *string `json:"effort,omitempty"`
}

func (p *RoleSettingsPatch) empty() bool {
	return p == nil || (p.Agent == nil && p.Model == nil && p.Effort == nil)
}

// SetRoleSettingsInput is the body of PATCH /api/v1/projects/{id}/role-settings:
// the agent, model and effort of the orchestrator, worker and reviewer roles
// and nothing else. It is the one project config write a phone may make, so it
// carries no env, post-create, symlink or repository field.
type SetRoleSettingsInput struct {
	Orchestrator *RoleSettingsPatch `json:"orchestrator,omitempty"`
	Worker       *RoleSettingsPatch `json:"worker,omitempty"`
	// Reviewer edits the project's first reviewer; later reviewers are kept.
	Reviewer *RoleSettingsPatch `json:"reviewer,omitempty"`
	// Actor names who made the change; set by the controller, never decoded.
	Actor string `json:"-"`
}

// SetRoleSettings merges the patch into the stored config under configMu, so a
// concurrent narrow write (rules) cannot be lost, and every other config field
// is kept as stored.
func (m *Service) SetRoleSettings(ctx context.Context, id domain.ProjectID, in SetRoleSettingsInput) (Project, error) {
	if err := validateProjectID(id); err != nil {
		return Project{}, err
	}
	if in.Orchestrator.empty() && in.Worker.empty() && in.Reviewer.empty() {
		return Project{}, apierr.Invalid("INVALID_ROLE_SETTINGS", "Set agent, model or effort for the orchestrator, worker or reviewer", nil)
	}
	for role, p := range map[string]*RoleSettingsPatch{"orchestrator": in.Orchestrator, "worker": in.Worker, "reviewer": in.Reviewer} {
		if err := validateRolePatch(role, p); err != nil {
			return Project{}, apierr.Invalid("INVALID_ROLE_SETTINGS", err.Error(), nil)
		}
	}
	m.configMu.Lock()
	defer m.configMu.Unlock()
	app, err := m.appWorkflowDefaults(ctx)
	if err != nil {
		return Project{}, projectStoreError(err, "PROJECT_CONFIG_UPDATE_FAILED", "Failed to read the app default reviewer")
	}
	// The read-merge-write runs inside one store transaction (writing only the
	// config column), so a concurrent config, settings or permissions write is
	// never overwritten with a stale copy. mutate's own errors are kept apart
	// from store errors so they reach the caller as API errors.
	var before, after domain.ProjectConfig
	var refused error
	row, found, err := m.store.UpdateProjectConfig(ctx, string(id), func(row domain.ProjectRecord) (domain.ProjectConfig, error) {
		before = row.Config
		next, err := m.mergeRoleSettings(row, in, app)
		if err == nil {
			err = m.managed.Refuse(m.managed.LockedProjectChanges(before, next)...)
		}
		if err != nil {
			refused = err
			return domain.ProjectConfig{}, err
		}
		after = next
		return next, nil
	})
	if refused != nil {
		return Project{}, refused
	}
	if err != nil {
		return Project{}, projectStoreError(err, "PROJECT_CONFIG_UPDATE_FAILED", "Failed to update project role settings")
	}
	if !found {
		return Project{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	actor := in.Actor
	if actor == "" {
		actor = "user"
	}
	m.recordConfig(ctx, id, before, after)
	m.logger.Info("project role settings changed", "project", string(id), "actor", actor)
	return m.projectFromRow(ctx, row), nil
}

// mergeRoleSettings applies the patch to the stored config and validates the
// result as the full-config writers do.
func (m *Service) mergeRoleSettings(row domain.ProjectRecord, in SetRoleSettingsInput, app domain.WorkflowDefaults) (domain.ProjectConfig, error) {
	next := row.Config
	applyRolePatch(&next.Orchestrator, in.Orchestrator)
	applyRolePatch(&next.Worker, in.Worker)
	if !in.Reviewer.empty() {
		// Scratch projects take no reviewers; refuse before storing an entry
		// every later full-config save would reject.
		if row.Kind.WithDefault() == domain.ProjectKindScratch {
			return next, apierr.Invalid("INVALID_ROLE_SETTINGS", "scratch projects do not support reviewers", nil)
		}
		reviewers := append([]domain.ReviewerConfig(nil), next.Reviewers...)
		if len(reviewers) == 0 {
			// With no entry the reviewer follows the app default, else the worker; creating one must not
			// change who reviews, so its harness is what that default resolves to.
			harness, inherited := next.InheritedReviewer(app)
			p := in.Reviewer
			picksAgent := p.Agent != nil && *p.Agent != "" && domain.ReviewerHarness(*p.Agent) != harness
			if harness == "" && !picksAgent {
				return next, apierr.Invalid("INVALID_ROLE_SETTINGS", "reviewer: choose an agent; this project has no default reviewer to follow", nil)
			}
			// The entry then replaces the default, so it starts from the model and
			// effort that default ran with, unless another agent is picked (the
			// worker's model would not apply to it).
			if picksAgent {
				reviewers = []domain.ReviewerConfig{{}}
			} else {
				reviewers = []domain.ReviewerConfig{{Harness: harness, AgentConfig: inherited}}
			}
		}
		r := &reviewers[0]
		if p := in.Reviewer; p.Agent != nil {
			r.Harness = domain.ReviewerHarness(*p.Agent)
		}
		if p := in.Reviewer; p.Model != nil {
			r.AgentConfig.Model = *p.Model
		}
		if p := in.Reviewer; p.Effort != nil {
			r.AgentConfig.Effort = *p.Effort
		}
		if r.Harness == "" {
			return next, apierr.Invalid("INVALID_ROLE_SETTINGS", "reviewer: an agent is required", nil)
		}
		next.Reviewers = reviewers
	}
	if err := next.Validate(); err != nil {
		return next, apierr.Invalid("INVALID_PROJECT_CONFIG", err.Error(), nil)
	}
	return next, nil
}

func applyRolePatch(ro *domain.RoleOverride, p *RoleSettingsPatch) {
	if p == nil {
		return
	}
	if p.Agent != nil {
		ro.Harness = domain.AgentHarness(*p.Agent)
	}
	if p.Model != nil {
		ro.AgentConfig.Model = *p.Model
	}
	if p.Effort != nil {
		ro.AgentConfig.Effort = *p.Effort
	}
}

// validateRolePatch refuses an unknown harness or effort level. The empty
// string clears a field and is always accepted.
func validateRolePatch(role string, p *RoleSettingsPatch) error {
	if p == nil {
		return nil
	}
	if p.Agent != nil && *p.Agent != "" {
		known := domain.AgentHarness(*p.Agent).IsKnown()
		if role == "reviewer" {
			known = domain.ReviewerHarness(*p.Agent).IsKnown()
		}
		if !known {
			return fmt.Errorf("%s.agent: unknown harness %q", role, *p.Agent)
		}
	}
	if p.Effort != nil && *p.Effort != "" && !domain.ValidEffort(*p.Effort) {
		return fmt.Errorf("%s.effort: %q is not a known effort level", role, *p.Effort)
	}
	if p.Model != nil && len(*p.Model) > 200 {
		return fmt.Errorf("%s.model is too long", role)
	}
	return nil
}

// appWorkflowDefaults reads the app-wide workflow defaults. A store without
// them gives none; a failed read is an error, because the result decides which
// reviewer a new entry is pinned to.
func (m *Service) appWorkflowDefaults(ctx context.Context) (domain.WorkflowDefaults, error) {
	if s, ok := m.store.(interface {
		WorkflowDefaults(context.Context) (domain.WorkflowDefaults, error)
	}); ok {
		return s.WorkflowDefaults(ctx)
	}
	return domain.WorkflowDefaults{}, nil
}
