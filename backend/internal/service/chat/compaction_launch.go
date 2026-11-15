package chat

import (
	"context"
	"fmt"

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
// a compaction window must fit below. 0 when unknown: the percent is then
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
	// The window an earlier start set was floored at that start's baseline.
	previousBaseline := cfg.AutoCompactBaseline
	cfg.AutoCompactBaseline = s.launchBaseline(ctx, cfg)
	dropStaleCompactWindow(&cfg)
	// A switch to Claude Code sets the window, Auto (empty) included.
	if cfg.ProviderSwitch != nil && contextwindow.AutoCompactEnvKey(cfg.Harness) != "" {
		// The switch's config is the source's, which may still carry the window
		// Tallpine set for an earlier Claude Code start. Drop that value so the new
		// choice, Auto included, is what starts; a project's own value stays.
		if previous := domain.ResolveAutoCompactWindow(conversation.Settings.AutoCompactWindow, s.previousCompactWindow(ctx, cfg, *conversation), previousBaseline); previous > 0 &&
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
	if tokens, ok := s.researchLaunchTokens(ctx, cfg, conversation.Settings.AutoCompactWindow, window, launchModel); ok {
		// Research mode (B638) carried across a restart: its window applies at launch.
		if tokens > 0 && contextwindow.AutoCompactEnvKey(cfg.Harness) != "" {
			setTallpineCompactWindow(&cfg, tokens)
		}
		*cfgp = cfg
		return
	}
	var modelWindow int64
	if window != "" {
		modelWindow = s.compactModelWindow(ctx, cfg, *conversation, launchModel)
	}
	if tokens := domain.ResolveAutoCompactWindow(window, modelWindow, cfg.AutoCompactBaseline); contextwindow.AutoCompactEnvKey(cfg.Harness) != "" && tokens > 0 {
		setTallpineCompactWindow(&cfg, tokens)
	}
	*cfgp = cfg
}

// launchBaseline is the context a chat of cfg's project, kind and harness is
// measured to start with, read once per launch (B735) for a harness Tallpine
// sets a window on. 0 without a reader; the default on a failed read.
func (s *Service) launchBaseline(ctx context.Context, cfg StartConfig) int64 {
	if s.autoCompactBaselines == nil || contextwindow.AutoCompactEnvKey(cfg.Harness) == "" {
		return 0
	}
	baseline, err := s.autoCompactBaselines.Baseline(ctx, cfg.ProjectID, cfg.Kind, cfg.Harness)
	if err != nil {
		s.log.Warn("read auto-compact baseline", "session", cfg.SessionID, "error", err)
		return domain.AutoCompactBaselineDefault
	}
	return baseline
}
