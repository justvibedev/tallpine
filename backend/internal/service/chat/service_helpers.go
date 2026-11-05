package chat

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// permissionConfigOptions annotates only provider controls whose semantics are
// known. In particular, plan and dontAsk are not Tallpine approval policies.
//
// Kiro's `mode` option lists its custom agents, and Tallpine fixes the agent at
// launch (the profile that carries the permission rules). Offering that list
// would replace the approval picker, so the option is withheld and the chat is
// governed by Tallpine's four approval modes.
func permissionConfigOptions(harness domain.AgentHarness, options []ports.ChatConfigOption) []ports.ChatConfigOption {
	out := make([]ports.ChatConfigOption, 0, len(options))
	for _, option := range options {
		if withheldConfigOption(harness, option.ID, option.Category) {
			continue
		}
		out = append(out, option)
	}
	for i := range out {
		out[i].Choices = append([]ports.ChatConfigOptionChoice(nil), out[i].Choices...)
		for j := range out[i].Choices {
			choice := &out[i].Choices[j]
			choice.PermissionMode = ""
			if out[i].ID != "mode" {
				continue
			}
			if harness != domain.HarnessClaudeCode {
				continue
			}
			switch choice.Value {
			case "manual", "default":
				choice.PermissionMode = domain.PermissionModeDefault
			case "acceptEdits":
				choice.PermissionMode = domain.PermissionModeAcceptEdits
			case "auto":
				choice.PermissionMode = domain.PermissionModeAuto
			case "bypassPermissions":
				choice.PermissionMode = domain.PermissionModeBypassPermissions
			}
		}
	}
	return out
}

// withheldConfigOption reports whether a harness's config option is one
// Tallpine never offers or accepts: Kiro's agent selector, whose agent is fixed
// at launch. The catalog filter and SetConfigOption share this one predicate.
func withheldConfigOption(harness domain.AgentHarness, id, category string) bool {
	return harness == domain.HarnessKiro && (id == "mode" || category == "mode")
}

func truncateUTF8Head(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	end := limit
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end]
}

// InterruptedAwakeAgents exposes only a positively observed transport loss.
// It does not infer failure from inactivity, lack of network, or a missing PID.
func (s *Service) InterruptedAwakeAgents() []domain.AwakeAgentTarget {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := []domain.AwakeAgentTarget{}
	for owner, target := range s.awakeInterrupted {
		cfg, ok := s.startConfigs[owner]
		if ok && cfg.ExpectedControllerOwner == target.Owner && s.ownerControllers[owner] == nil {
			result = append(result, target)
		}
	}
	return result
}

// SetProjects binds the project reader after construction, for callers that
// build the service before the project store exists.
func (s *Service) SetProjects(projects ProjectReader) { s.projects = projects }

// checkModelPolicy applies the project's model denylist to an in-chat model
// choice, the same check spawn applies (domain.ProjectConfig.CheckModel). An
// empty model keeps the agent's default and is not checked. A project whose
// config cannot be read is refused rather than assumed permissive.
func (s *Service) checkModelPolicy(ctx context.Context, id domain.SessionID, model string) error {
	if s.projects == nil || strings.TrimSpace(model) == "" {
		return nil
	}
	record, found, err := s.sessions.GetSession(ctx, id)
	if err != nil || !found {
		return fmt.Errorf("%w: session %s: %w", ErrModelPolicyUnavailable, id, cmp.Or(err, errors.New("not found")))
	}
	return s.checkProjectModelPolicy(ctx, record.ProjectID, model)
}

// checkProjectModelPolicy is the policy check once the project is known; a
// reviewer chat resolves it from its own conversation.
func (s *Service) checkProjectModelPolicy(ctx context.Context, projectID domain.ProjectID, model string) error {
	if s.projects == nil || strings.TrimSpace(model) == "" {
		return nil
	}
	project, found, err := s.projects.GetProject(ctx, string(projectID))
	if err != nil || !found {
		return fmt.Errorf("%w: project %s: %w", ErrModelPolicyUnavailable, projectID, cmp.Or(err, errors.New("not found")))
	}
	return project.Config.CheckModel(model)
}
