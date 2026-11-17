package settings

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// workflowDefaultsStore is the optional store ability to save the app-wide
// workflow defaults.
type workflowDefaultsStore interface {
	SetWorkflowDefaults(ctx context.Context, d domain.WorkflowDefaults, now time.Time) error
}

// ErrInvalidAppInstructions refuses app rules over the cap or switches for an
// unknown role.
var ErrInvalidAppInstructions = errors.New("invalid app instructions")

// WorkflowDefaultsUpdate changes any subset of the defaults; nil keeps a value.
type WorkflowDefaultsUpdate struct {
	AutoReview      *bool
	PlanningMode    *domain.PlanningMode
	PlanAutoApprove *bool
	// ReviewerHarness and ReviewerModel set the app-wide default reviewer; an
	// empty harness clears it (and its model).
	ReviewerHarness *domain.ReviewerHarness
	ReviewerModel   *string
	// Worker* and Orchestrator* set the agent, model and effort a new project
	// starts with for that role; an empty agent clears its model and effort.
	WorkerAgent        *domain.AgentHarness
	WorkerModel        *string
	WorkerEffort       *string
	OrchestratorAgent  *domain.AgentHarness
	OrchestratorModel  *string
	OrchestratorEffort *string
	// WorkerRules, OrchestratorRules and ReviewerRules set the app-wide rules
	// of that role; SkillRoles replaces the app skill switches (B740).
	WorkerRules       *string
	OrchestratorRules *string
	ReviewerRules     *string
	SkillRoles        *domain.SkillRoles
}

// WorkflowDefaults reports the app-wide workflow defaults. An unreadable store
// gives the built-in defaults (everything off), so a read failure never turns
// automation on.
func (s *Service) WorkflowDefaults(ctx context.Context) domain.WorkflowDefaults {
	snapshot, err := s.store.GetAppSettings(ctx)
	if err != nil {
		return domain.DefaultWorkflowDefaults()
	}
	return snapshot.WorkflowDefaults.Normalized()
}

// SetWorkflowDefaults changes any subset of the app-wide workflow defaults.
func (s *Service) SetWorkflowDefaults(ctx context.Context, update WorkflowDefaultsUpdate) (Snapshot, error) {
	st, ok := s.store.(workflowDefaultsStore)
	if !ok {
		return Snapshot{}, ports.ErrChatUnsupported
	}
	current, err := s.store.GetAppSettings(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	next := current.WorkflowDefaults.Normalized()
	if update.AutoReview != nil {
		next.AutoReview = *update.AutoReview
	}
	if update.PlanningMode != nil {
		next.PlanningMode = *update.PlanningMode
	}
	if update.PlanAutoApprove != nil {
		next.PlanAutoApprove = *update.PlanAutoApprove
	}
	if update.ReviewerHarness != nil {
		// A model belongs to its harness: a changed harness drops the old model
		// unless the same update names a new one.
		if *update.ReviewerHarness != next.ReviewerHarness && update.ReviewerModel == nil {
			next.ReviewerModel = ""
		}
		next.ReviewerHarness = *update.ReviewerHarness
	}
	if update.ReviewerModel != nil {
		next.ReviewerModel = *update.ReviewerModel
	}
	if next.ReviewerHarness == "" {
		next.ReviewerModel = ""
	}
	applyRoleDefault(&next.WorkerAgent, &next.WorkerModel, &next.WorkerEffort, update.WorkerAgent, update.WorkerModel, update.WorkerEffort)
	applyRoleDefault(&next.OrchestratorAgent, &next.OrchestratorModel, &next.OrchestratorEffort, update.OrchestratorAgent, update.OrchestratorModel, update.OrchestratorEffort)
	applyInstructions(&next.AppInstructions, update)
	if err := next.Validate(); err != nil {
		return Snapshot{}, fmt.Errorf("%w: %w", ErrInvalidAppInstructions, err)
	}
	if err := st.SetWorkflowDefaults(ctx, next.Normalized(), s.now()); err != nil {
		return Snapshot{}, err
	}
	return s.Get(ctx)
}

// applyRoleDefault merges one role's update. Like the reviewer, a model and
// effort belong to their agent: a changed agent drops the old ones unless the
// same update names new ones, and an empty agent clears both.
func applyRoleDefault(agent *domain.AgentHarness, model, effort *string, ua *domain.AgentHarness, um, ue *string) {
	if ua != nil {
		if *ua != *agent {
			if um == nil {
				*model = ""
			}
			if ue == nil {
				*effort = ""
			}
		}
		*agent = *ua
	}
	if um != nil {
		*model = *um
	}
	if ue != nil {
		*effort = *ue
	}
	if *agent == "" {
		*model, *effort = "", ""
	}
}

// applyInstructions merges the app rules and skill switches of update.
func applyInstructions(a *domain.AppInstructions, update WorkflowDefaultsUpdate) {
	for _, f := range []struct {
		dst *string
		src *string
	}{{&a.WorkerRules, update.WorkerRules}, {&a.OrchestratorRules, update.OrchestratorRules}, {&a.ReviewerRules, update.ReviewerRules}} {
		if f.src != nil {
			*f.dst = strings.TrimSpace(*f.src)
		}
	}
	if update.SkillRoles != nil {
		a.SkillRoles = pruneSkillRoles(*update.SkillRoles)
	}
}

// pruneSkillRoles drops roles with no switches, so an emptied role reads back
// as unset.
func pruneSkillRoles(in domain.SkillRoles) domain.SkillRoles {
	out := domain.SkillRoles{}
	for role, skills := range in {
		if len(skills) > 0 {
			out[role] = skills
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
