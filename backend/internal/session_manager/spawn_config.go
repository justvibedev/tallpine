package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/deepseekharnessacp"
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func (m *Manager) markAutomationLaunchCompleted(ctx context.Context, id domain.SessionID) error {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("spawn: automation session %s disappeared before launch completion", id)
	}
	if rec.AutomationRunID == nil || rec.AutomationLaunchCompleted {
		return nil
	}
	rec.AutomationLaunchCompleted = true
	rec.UpdatedAt = m.clock()
	return m.store.UpdateSession(ctx, rec)
}

func replayClientRequest(rec domain.SessionRecord, hash string) (domain.SessionRecord, error) {
	if rec.ClientRequestHash != hash {
		return domain.SessionRecord{}, ErrClientRequestConflict
	}
	if !rec.ClientRequestCommitted {
		return domain.SessionRecord{}, ErrClientRequestIncomplete
	}
	return rec, nil
}

// resolveTUIAgentConfig validates a terminal launch against the model
// catalog. Claude Code requires it. The Codex CLI validates its own model and
// effort, so for Codex the catalog only refines the launch: when it is
// unavailable, stale, lacks a custom model or lists other efforts, the session
// launches with the unvalidated config, as it would without a catalog.
func (m *Manager) resolveTUIAgentConfig(ctx context.Context, decision agentpolicy.Decision, cfg ports.SpawnConfig, project domain.ProjectConfig, unvalidated ports.AgentConfig) (ports.AgentConfig, error) {
	resolved, err := m.resolveAgentConfig(ctx, decision, cfg, project)
	// Only the spawn's own cancellation stops the launch; a catalog lookup's
	// internal timeout is one more way the catalog can be unavailable.
	if err == nil || cfg.Harness != domain.HarnessCodex || ctx.Err() != nil {
		return resolved, err
	}
	m.logger.Warn("spawn: Codex model catalog could not validate the launch; leaving it to the CLI",
		"model", unvalidated.Model, "effort", unvalidated.Effort, "error", err)
	return unvalidated, nil
}

func (m *Manager) resolveAgentConfig(ctx context.Context, decision agentpolicy.Decision, cfg ports.SpawnConfig, project domain.ProjectConfig) (ports.AgentConfig, error) {
	return m.resolveAgentConfigFor(ctx, decision, cfg, project, false)
}

// resolveChatAgentConfig is resolveAgentConfig for a Chat launch. Chat treats a
// full model ID its session does not offer as optional and silently keeps
// Claude Code's own model, so an ID absent from the catalog is refused here
// rather than accepted on the provider's word.
func (m *Manager) resolveChatAgentConfig(ctx context.Context, decision agentpolicy.Decision, cfg ports.SpawnConfig, project domain.ProjectConfig) (ports.AgentConfig, error) {
	return m.resolveAgentConfigFor(ctx, decision, cfg, project, true)
}

func (m *Manager) resolveAgentConfigFor(ctx context.Context, decision agentpolicy.Decision, cfg ports.SpawnConfig, project domain.ProjectConfig, chat bool) (ports.AgentConfig, error) {
	base := effectiveAgentConfigFor(cfg.Harness, cfg.Kind, cfg.Role, project)
	requested := cfg.AgentConfig
	resolved := applySpawnAgentConfigUnder(decision, cfg.Harness, base, requested)
	modelChangedWithoutExplicitEffort := requested.Model != "" && requested.Model != base.Model &&
		!cfg.EffortOverride && requested.Effort == ""
	if cfg.EffortOverride {
		resolved.Effort = requested.Effort
	}
	if cfg.Harness == domain.HarnessDeepSeek {
		// Harness advertises a fixed off/low/high/max set (shared with Chat in
		// deepseekharnessacp), so no model catalog is consulted.
		if err := deepseekharnessacp.ValidateEffort(resolved.Effort); err != nil {
			if !cfg.EffortOverride {
				// Inherited project/role effort clears silently, as for other harnesses.
				resolved.Effort = ""
				return resolved, nil
			}
			return ports.AgentConfig{}, fmt.Errorf("%w for harness %q: %w", ports.ErrUnsupportedEffort, cfg.Harness, err)
		}
		return resolved, nil
	}
	if cfg.Harness != domain.HarnessCodex && cfg.Harness != domain.HarnessClaudeCode {
		// Only a flagged explicit override (the spawn API and delegation mark
		// request-carried effort) fails loudly: the caller asked for something
		// the harness cannot honor. Unflagged or inherited effort still clears
		// silently so role/project defaults keep working for every harness.
		if cfg.EffortOverride && strings.TrimSpace(requested.Effort) != "" {
			return ports.AgentConfig{}, fmt.Errorf("%w for harness %q: effort is only supported for codex, claude-code and deepseek-harness agents", ports.ErrUnsupportedEffort, cfg.Harness)
		}
		resolved.Effort = ""
		return resolved, nil
	}
	modelID := strings.TrimSpace(resolved.Model)
	validateClaudeModel := cfg.Harness == domain.HarnessClaudeCode && modelID != ""
	if resolved.Effort == "" && !validateClaudeModel {
		return resolved, nil
	}
	if m.modelCatalog == nil {
		if validateClaudeModel {
			return ports.AgentConfig{}, fmt.Errorf("%w: model catalog is unavailable", ports.ErrModelCapabilitiesUnavailable)
		}
		return resolved, nil
	}
	// Validate against the saved catalog first so a launch does not wait on a
	// live model discovery. Rediscover when the saved catalog cannot answer. A
	// stale Claude catalog may still answer from its last-known-good entries;
	// other stale, updating or failed catalogs are refreshed first.
	catalog, err := m.modelCatalog.Models(ctx, string(cfg.Harness), string(cfg.ProjectID), false)
	validationCatalog := catalog
	if cfg.Harness == domain.HarnessClaudeCode && catalog.Stale {
		validationCatalog.Stale = false
		validationCatalog.RefreshState = "idle"
	}
	if err != nil || !savedCatalogCovers(validationCatalog, modelID, resolved.Effort) {
		catalog, err = m.modelCatalog.Models(ctx, string(cfg.Harness), string(cfg.ProjectID), true)
	}
	if err != nil {
		if modelChangedWithoutExplicitEffort {
			resolved.Effort = ""
		}
		if validateClaudeModel || resolved.Effort != "" {
			return ports.AgentConfig{}, fmt.Errorf("%w: %w", ports.ErrModelCapabilitiesUnavailable, err)
		}
		return resolved, nil
	}
	if modelID == "" {
		for _, item := range catalog.Models {
			if item.IsDefault {
				modelID = item.ID
				break
			}
		}
	}
	var selected *ports.AgentModelInfo
	for i := range catalog.Models {
		if catalog.Models[i].ID == modelID {
			selected = &catalog.Models[i]
			break
		}
	}
	exactMatch := selected != nil
	if selected == nil && cfg.Harness == domain.HarnessClaudeCode {
		// The catalog may list only concrete models; a family alias the CLI
		// accepts ("sonnet") is valid when the catalog has a model of that
		// family, whether or not that model takes an effort setting.
		selected = newestClaudeFamilyModel(catalog.Models, modelID, false)
	}
	// An expired access token Claude Code renews itself must not block the
	// launch. With no account-scoped list (the built-in fallback, no last
	// success) an unlisted model is left to the CLI; against the account's own
	// last-known list it is more likely a typo, so it still fails below.
	unlistedToCLI := cfg.Harness == domain.HarnessClaudeCode && catalog.Stale &&
		catalog.WarningCode == ports.ModelCatalogWarningAuthExpired && catalog.LastSuccessAt == nil
	if unlistedToCLI && validateClaudeModel && selected == nil {
		// The CLI owns per-model capabilities, but a level no Claude model
		// accepts is still refused here rather than after the session starts.
		if resolved.Effort != "" && !containsString(claudeEffortLevels, resolved.Effort) {
			return ports.AgentConfig{}, fmt.Errorf("%w %q for model %q", ports.ErrUnsupportedEffort, resolved.Effort, modelID)
		}
		return resolved, nil
	}
	if validateClaudeModel && selected == nil && !chat && domain.AcceptsUnlistedClaudeModelID(modelID, catalogModelIDs(catalog.Models)) {
		// The account's list can lag a release that Claude Code already serves
		// (its chat offers it first). A well-formed ID the list lacks is left to
		// the CLI, which owns per-model capabilities; a malformed one is a typo
		// and falls through to the refusals below.
		if modelChangedWithoutExplicitEffort {
			// An inherited effort was chosen for another model.
			resolved.Effort = ""
		}
		if resolved.Effort != "" && !containsString(claudeEffortLevels, resolved.Effort) {
			return ports.AgentConfig{}, fmt.Errorf("%w %q for model %q", ports.ErrUnsupportedEffort, resolved.Effort, modelID)
		}
		return resolved, nil
	}
	if validateClaudeModel && selected == nil && catalog.Stale {
		return ports.AgentConfig{}, fmt.Errorf("%w: model %q is not in the stale provider catalog and refresh did not produce a fresh one", ports.ErrModelCapabilitiesUnavailable, modelID)
	}
	if validateClaudeModel && selected == nil {
		return ports.AgentConfig{}, fmt.Errorf("%w: model %q is not in the active provider catalog", ErrUnsupportedModel, modelID)
	}
	if exactMatch && selected.RequiresVersion != "" {
		// Launching would only fail on the first turn with the provider's
		// claude_code_version_too_old rejection; say so before spawning.
		// Family aliases are never gated: the CLI resolves them itself.
		return ports.AgentConfig{}, fmt.Errorf("%w: model %q: %s", ErrUnsupportedModel, modelID, selected.UnavailableReason)
	}
	if exactMatch && len(selected.Efforts) == 0 && cfg.Harness == domain.HarnessClaudeCode {
		// A family alias ("opus") carries no capabilities of its own; the CLI
		// resolves it to the family's newest model, which the picker shows.
		if concrete := newestClaudeFamilyModel(catalog.Models, modelID, true); concrete != nil {
			selected = concrete
		}
	}
	if modelChangedWithoutExplicitEffort {
		if selected == nil || !containsString(selected.Efforts, base.Effort) {
			resolved.Effort = ""
		}
	}
	if resolved.Effort == "" {
		return resolved, nil
	}
	if selected == nil || (catalog.Stale && cfg.Harness != domain.HarnessClaudeCode) {
		return ports.AgentConfig{}, fmt.Errorf("%w for model %q", ports.ErrModelCapabilitiesUnavailable, modelID)
	}
	if resolved.Effort != "" && !containsString(selected.Efforts, resolved.Effort) {
		return ports.AgentConfig{}, fmt.Errorf("%w %q for model %q", ports.ErrUnsupportedEffort, resolved.Effort, modelID)
	}
	return resolved, nil
}

var (
	claudeEffortLevels = []string{"low", "medium", "high", "xhigh", "max"}
)

// newestClaudeFamilyModel returns the newest concrete catalog model of the
// family a bare alias such as "opus" or "opus[1m]" names, or nil when alias is
// not a family alias or the catalog has no such model. requireEfforts limits the
// search to models that report effort levels (effort validation); alias
// acceptance passes false so effort-less families still resolve.
func newestClaudeFamilyModel(models []ports.AgentModelInfo, alias string, requireEfforts bool) *ports.AgentModelInfo {
	candidates := make([]domain.ClaudeFamilyCandidate, len(models))
	for i, m := range models {
		candidates[i] = domain.ClaudeFamilyCandidate{ID: m.ID, Label: m.Label, Skip: requireEfforts && len(m.Efforts) == 0}
	}
	if i := domain.NewestClaudeFamilyModel(candidates, alias); i >= 0 {
		return &models[i]
	}
	return nil
}

func containsString(values []string, value string) bool {
	if value == "" {
		return false
	}
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// inheritedSpawnPermissions derives a worker override from its requesting chat
// orchestrator. The request supplies identity only: the stored conversation
// settings remain the authority for the permission policy.
func (m *Manager) inheritedSpawnPermissions(ctx context.Context, projectID domain.ProjectID, parentID domain.SessionID) (domain.PermissionMode, error) {
	parent, ok, err := m.store.GetSession(ctx, parentID)
	if err != nil {
		return "", fmt.Errorf("load parent session %s: %w", parentID, err)
	}
	if !ok || parent.ProjectID != projectID || parent.Kind != domain.KindOrchestrator {
		// TALLPINE_SESSION_ID is available in every session, not only orchestrators.
		// A worker (or a stale/cross-project value) must preserve the historical
		// project-default spawn behavior rather than gain an inherited policy.
		return "", nil
	}
	conversations, ok := m.store.(conversationSettingsStore)
	if !ok {
		return "", nil
	}
	conversation, err := conversations.ConversationForSession(ctx, parentID)
	if errors.Is(err, domain.ErrNoConversation) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load parent conversation %s: %w", parentID, err)
	}
	return conversation.Settings.ApprovalMode, nil
}

// loadProject loads the project record so spawn can resolve its per-project
// config (harness/agent overrides, env, branch, rules, provisioning). A missing
// project yields a zero record rather than an error: the project may be
// unregistered yet still have live sessions, and an empty config simply means
// every field falls back to its default.
func (m *Manager) loadProject(ctx context.Context, projectID domain.ProjectID) (domain.ProjectRecord, error) {
	if projectID == "" {
		return domain.ProjectRecord{}, nil
	}
	row, ok, err := m.store.GetProject(ctx, string(projectID))
	if err != nil {
		return domain.ProjectRecord{}, fmt.Errorf("load project: %w", err)
	}
	if !ok {
		return domain.ProjectRecord{}, nil
	}
	return row, nil
}

func catalogModelIDs(models []ports.AgentModelInfo) []string {
	ids := make([]string, 0, len(models))
	for _, m := range models {
		ids = append(ids, m.ID)
	}
	return ids
}
