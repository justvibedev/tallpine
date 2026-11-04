package chat

import (
	"context"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/contextwindow"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// ModelCatalogReader reads the saved model catalog for a harness and project.
type ModelCatalogReader = contextwindow.CatalogReader

// SetModelCatalog late-binds the catalog the percent compaction window reads
// the launching model's context window from.
func (s *Service) SetModelCatalog(catalog ModelCatalogReader) { s.modelCatalog = catalog }

// previousCompactWindow is the model window an earlier start resolved a percent
// choice against: the conversation's own model, before any switch.
func (s *Service) previousCompactWindow(ctx context.Context, cfg StartConfig, conversation domain.ConversationRecord) int64 {
	cfg.ProviderSwitch = nil
	return s.compactModelWindow(ctx, cfg, conversation, conversation.Settings.Model)
}

// compactModelWindow is the capacity of the model a chat is starting on, which
// a percent compaction window applies to. 0 when unknown: the percent is then
// left to the agent's own default rather than resolved against another model.
// The agent's reported window is never used: after Tallpine sets a compaction env the
// agent reports that limit as its window, so each restart would shrink it again.
func (s *Service) compactModelWindow(ctx context.Context, cfg StartConfig, _ domain.ConversationRecord, model string) int64 {
	capacity, _ := s.modelCapacity(ctx, cfg.Harness, cfg.ProjectID, model)
	return capacity
}

// applyLaunchCompaction settles the compaction window a chat starts with: it
// drops a stale window Tallpine set for an earlier start, handles a provider switch,
// resolves the session's choice (else the Settings default) against the launch
// model's capacity, and sets the Claude Code env. cfg and conversation are
// updated in place.
func (s *Service) applyLaunchCompaction(ctx context.Context, cfgp *StartConfig, conversation *domain.ConversationRecord) {
	cfg := *cfgp
	dropStaleCompactWindow(&cfg)
	// A switch to Claude Code sets the window, Auto (empty) included.
	if cfg.ProviderSwitch != nil && contextwindow.AutoCompactEnvKey(cfg.Harness) != "" {
		// The switch's config is the source's, which may still carry the window
		// Tallpine set for an earlier Claude Code start. Drop that value so the new
		// choice, Auto included, is what starts; a project's own value stays.
		if previous := domain.AutoCompactTokenCount(conversation.Settings.AutoCompactWindow, s.previousCompactWindow(ctx, cfg, *conversation)); previous > 0 &&
			cfg.Env[contextwindow.AutoCompactEnvKey(cfg.Harness)] == fmt.Sprint(previous) {
			cfg.Env = cloneStartConfig(cfg).Env
			delete(cfg.Env, contextwindow.AutoCompactEnvKey(cfg.Harness))
		}
		conversation.Settings.AutoCompactWindow = cfg.ProviderSwitch.AutoCompactWindow
	}
	// Claude Code reads its compaction window when it starts. Auto leaves any
	// window the project's environment sets. The map is copied because the
	// caller's may be the one saved for later restarts.
	window := conversation.Settings.AutoCompactWindow
	if cfg.ProviderSwitch == nil {
		// No choice of its own: the Settings default for this harness applies,
		// and an orchestrator's own default (see effectiveCompactChoice).
		window = s.effectiveCompactChoice(ctx, cfg.Harness, cfg.Kind, cfg.ProjectID, window)
	}
	launchModel := cfg.Model
	if cfg.ProviderConversationID != "" && conversation.Settings.Model != "" {
		launchModel = conversation.Settings.Model
	}
	var modelWindow int64
	if strings.HasSuffix(window, "%") {
		modelWindow = s.compactModelWindow(ctx, cfg, *conversation, launchModel)
	}
	if tokens := domain.AutoCompactTokenCount(window, modelWindow); contextwindow.AutoCompactEnvKey(cfg.Harness) != "" && tokens > 0 {
		setTallpineCompactWindow(&cfg, tokens)
	}
	*cfgp = cfg
}
