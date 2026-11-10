package chat

import (
	"context"
	"errors"
	"strconv"
	"time"

	"tallpine.eu/tallpine/backend/internal/contextwindow"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// compactLiveUnknown marks a reattached provider whose launch window Tallpine never saw.
const compactLiveUnknown = -1

// ErrRestartBusy means the agent has work in flight, so a restart would lose it.
var ErrRestartBusy = errors.New("agent is busy; restart when it is idle")

// launchedCompactTokens is the compaction window a Claude Code agent starts
// with, from the environment the chat service set (or the project supplied).
func launchedCompactTokens(cfg StartConfig, reattached bool) int {
	if reattached {
		return compactLiveUnknown
	}
	key := contextwindow.AutoCompactEnvKey(cfg.Harness)
	if key == "" {
		return 0
	}
	tokens, err := strconv.Atoi(cfg.Env[key])
	if err != nil || tokens < 0 {
		return 0
	}
	return tokens
}

// dropStaleCompactWindow removes the window the chat service put in the
// environment for an earlier start, so a restart recomputes it from the current
// choice: Auto after a custom value must not keep the old number. A window the
// project's own environment sets is never Tallpine's to remove. The map is copied
// because the caller's may be the one saved for later restarts.
func dropStaleCompactWindow(cfg *StartConfig) {
	if !cfg.CompactWindowFromTallpine {
		return
	}
	key := contextwindow.AutoCompactEnvKey(cfg.Harness)
	cfg.Env = cloneStartConfig(*cfg).Env
	delete(cfg.Env, key)
	if cfg.ProjectCompactWindow != "" && key != "" {
		cfg.Env[key] = cfg.ProjectCompactWindow
	}
	cfg.ProjectCompactWindow = ""
	cfg.CompactWindowFromTallpine = false
}

// retargetCompactWindow moves a source chat's config to another harness. The
// window state is the source's, so it is dropped under the key it was written
// with before the harness changes.
func retargetCompactWindow(cfg *StartConfig, target domain.AgentHarness) {
	dropStaleCompactWindow(cfg)
	cfg.Harness = target
}

// setTallpineCompactWindow puts the chat service's window in the environment. It
// replaces any window the project set, which is remembered so Auto and a restart
// can return to it. The map is copied because the caller's may be the one saved
// for later restarts.
func setTallpineCompactWindow(cfg *StartConfig, tokens int) {
	key := contextwindow.AutoCompactEnvKey(cfg.Harness)
	if key == "" {
		return
	}
	cfg.Env = cloneStartConfig(*cfg).Env
	if !cfg.CompactWindowFromTallpine {
		cfg.ProjectCompactWindow = cfg.Env[key]
	}
	cfg.Env[key] = strconv.Itoa(tokens)
	cfg.CompactWindowFromTallpine = true
}

// projectCompactTokens is the window the project's own environment sets for
// Claude Code, which an Auto choice leaves alone; 0 when it sets none.
func projectCompactTokens(cfg StartConfig) int {
	key := contextwindow.AutoCompactEnvKey(cfg.Harness)
	if key == "" {
		return 0
	}
	raw := cfg.Env[key]
	if cfg.CompactWindowFromTallpine {
		raw = cfg.ProjectCompactWindow
	}
	tokens, err := strconv.Atoi(raw)
	if err != nil || tokens < 0 {
		return 0
	}
	return tokens
}

// desiredCompactTokens is the window the session should compact at now: its
// own choice, else the Settings default for its agent, resolved against the
// model's window. Auto, or a percent whose window is unknown (a named model the catalog lacks),
// means the window the project's environment sets, else 0: the agent's default.
func (s *Service) desiredCompactTokens(ctx context.Context, c *Controller) int {
	return s.desiredCompactTokensIn(ctx, c, true)
}

// desiredCompactTokensIn is desiredCompactTokens with research mode (B413)
// counted or not. The idle threshold passes false: it is sized from the
// coordination window, never the research window.
func (s *Service) desiredCompactTokensIn(ctx context.Context, c *Controller, research bool) int {
	if research {
		if tokens, _, ok := s.researchTarget(ctx, c); ok {
			return tokens
		}
	}
	settings := c.Settings()
	window := s.effectiveCompactChoice(ctx, c.harness, c.kind, c.conversation.ProjectID, settings.AutoCompactWindow)
	var modelWindow int64
	if _, percent := domain.ParseAutoCompactPercent(window); percent {
		modelWindow, _ = s.modelCapacity(ctx, c.harness, c.conversation.ProjectID, settings.Model)
	}
	if tokens := domain.AutoCompactTokenCount(window, modelWindow); tokens > 0 {
		return tokens
	}
	// Auto (or a percent whose window is unknown): the project's own window if it
	// sets one, else the agent's default.
	return c.projectCompactBaseline()
}

// modelCapacity is the model's context window and display name from the saved
// catalog; see contextwindow.Lookup.
func (s *Service) modelCapacity(ctx context.Context, harness domain.AgentHarness, project domain.ProjectID, model string) (int64, string) {
	return contextwindow.Lookup(ctx, s.modelCatalog, string(harness), string(project), model)
}

// compactionPending reports whether the running Claude Code agent compacts at a
// window other than the one chosen, so the choice waits for a restart.
func (s *Service) compactionPending(ctx context.Context, c *Controller) bool {
	if contextwindow.AutoCompactEnvKey(c.harness) == "" {
		return false
	}
	c.mu.Lock()
	live := c.compactLive
	c.mu.Unlock()
	return live != s.desiredCompactTokens(ctx, c)
}

// overlayCompactionView adds what a client shows about compaction to settings
// read for a snapshot: whether the chosen window waits for a restart, and the
// window the project's environment sets (so Auto can say "set by project").
func (s *Service) overlayCompactionView(ctx context.Context, settings *domain.ConversationSettings, c *Controller, pending bool) {
	settings.AutoCompactPending = pending
	settings.AutoCompactProjectTokens = 0
	if contextwindow.AutoCompactEnvKey(c.harness) != "" {
		settings.AutoCompactProjectTokens = c.projectCompactBaseline()
	}
	s.overlayResolvedView(ctx, settings, c.harness, c.kind, c.conversation.ProjectID)
	if choice, ok := s.researchInEffect(ctx, c); ok {
		settings.CompactThreshold = s.researchTokens(ctx, c, choice)
	}
}

// overlayResolvedView sets the model's capacity, the threshold the chosen
// setting makes of it, and the model's name, so a client renders them instead
// of deriving them from the window the agent reports (its compaction limit once
// Tallpine sets one). It needs no controller: a stopped or hibernated chat has the
// same view.
func (s *Service) overlayResolvedView(ctx context.Context, settings *domain.ConversationSettings, harness domain.AgentHarness, kind domain.SessionKind, project domain.ProjectID) {
	capacity, label := s.modelCapacity(ctx, harness, project, settings.Model)
	settings.ModelLabel = label
	settings.ContextCapacity = capacity
	settings.CompactThreshold = domain.AutoCompactTokenCount(s.effectiveCompactChoice(ctx, harness, kind, project, settings.AutoCompactWindow), capacity)
}

// effectiveCompactChoice is the session's own choice, else its default: the
// Settings default for its agent, and for an orchestrator the project's
// orchestrator window or Tallpine's built-in one (domain.DefaultAutoCompactChoice).
func (s *Service) effectiveCompactChoice(ctx context.Context, harness domain.AgentHarness, kind domain.SessionKind, project domain.ProjectID, own string) string {
	if own != "" {
		return own
	}
	var harnessDefault string
	if s.autoCompactDefault != nil {
		harnessDefault = s.autoCompactDefault(ctx, harness)
	}
	if kind != domain.KindOrchestrator {
		return harnessDefault
	}
	cfg := s.compactProjectConfig(ctx, project)
	key := contextwindow.AutoCompactEnvKey(harness)
	envWindow := key != "" && cfg.Env[key] != ""
	return domain.DefaultAutoCompactChoice(kind, cfg, harnessDefault, envWindow)
}

// compactProjectConfig is the project's config for the compaction default. An
// unreadable project counts as one that sets nothing, so the built-in
// orchestrator window still applies: compacting early never loses tracking,
// which lives in the daemon, while not compacting costs on every call.
func (s *Service) compactProjectConfig(ctx context.Context, project domain.ProjectID) domain.ProjectConfig {
	if s.projects == nil || project == "" {
		return domain.ProjectConfig{}
	}
	rec, ok, err := s.projects.GetProject(ctx, string(project))
	if err != nil || !ok {
		return domain.ProjectConfig{}
	}
	return rec.Config
}

func (s *Service) reconcileCompactionAsync(c *Controller) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		s.reconcileCompaction(ctx, c)
	}()
}

// ApplyCompactionDefault is called after a harness's compaction default
// changes: running chats of that harness without a choice of their own follow
// it now where the agent allows, and the rest on their next restart.
func (s *Service) ApplyCompactionDefault(ctx context.Context, harness domain.AgentHarness) {
	s.mu.RLock()
	var targets []*Controller
	for _, c := range s.controllers {
		if c.harness == harness {
			targets = append(targets, c)
		}
	}
	s.mu.RUnlock()
	for _, c := range targets {
		if c.Settings().AutoCompactWindow == "" {
			s.reconcileCompaction(ctx, c)
		}
	}
}

// RestartChatAgent restarts a quiet chat's agent so a saved setting that only
// applies at launch takes effect. The conversation is kept: the provider
// resumes its own history. A turn, queued message, approval or background work
// in flight refuses the restart rather than losing it.
func (s *Service) RestartChatAgent(ctx context.Context, id domain.SessionID) error {
	stopped, err := s.HibernateChatForUpdate(ctx, id)
	if err != nil {
		return err
	}
	if !stopped {
		return ErrRestartBusy
	}
	_, release, err := s.workingController(ctx, id)
	if err != nil {
		return err
	}
	release()
	return nil
}

// ContextBreakdown asks a running chat's agent what fills its context window.
// It is an explicit request, never part of a snapshot: the agent may count each
// category with a model API. available=false means no breakdown exists to show
// (an agent that cannot report one, an unpatched bridge, a chat that is not
// running), and the caller shows the totals it already has.
func (s *Service) ContextBreakdown(ctx context.Context, id domain.SessionID) (domain.ContextBreakdown, bool, error) {
	if _, err := s.requireChatSession(ctx, id); err != nil {
		return domain.ContextBreakdown{}, false, err
	}
	return s.runningContextBreakdown(ctx, id)
}

// ModelUsage returns the session's cumulative token spend by model.
//
// Harnesses that report per-turn spend by model (Claude) fill the per-model
// ledger directly. Harnesses that only report cumulative conversation totals
// (Codex, DeepSeek, Kiro) never fill its token counters, so when no ledger row
// has tokens the totals are attributed to the session's resolved model instead
// of reading as no usage. A ledger row that carries only credits (Kiro) gets
// the totals merged into it, so one row shows credits and tokens together.
func (s *Service) ModelUsage(ctx context.Context, id domain.SessionID) ([]domain.ModelUsage, error) {
	if _, err := s.requireChatSession(ctx, id); err != nil {
		return nil, err
	}
	reader, ok := s.store.(interface {
		ModelUsageForSession(context.Context, domain.SessionID) ([]domain.ModelUsage, error)
	})
	if !ok {
		return []domain.ModelUsage{}, nil
	}
	rows, err := reader.ModelUsageForSession(ctx, id)
	if err != nil {
		return rows, err
	}
	for _, row := range rows {
		if row.InputTokens+row.CacheCreationTokens+row.CacheReadTokens+row.OutputTokens > 0 {
			return rows, nil
		}
	}
	conversation, err := s.store.ConversationForSession(ctx, id)
	if err != nil || conversation.Usage == nil {
		return rows, nil //nolint:nilerr // no conversation yet means no usage
	}
	u := conversation.Usage
	if u.InputTokens == 0 && u.OutputTokens == 0 && u.CachedTokens == 0 {
		return rows, nil
	}
	model := s.EffectiveModel(ctx, id)
	if model == "" {
		model = "unknown"
	}
	turns := s.finishedTurns(ctx, id)
	// Codex counts cached input inside InputTokens (total = in + out); DeepSeek
	// reports hit and miss as disjoint counts (total = in + cached + out). Only
	// subtract when the total shows the cache is already part of input.
	fresh := u.InputTokens
	disjoint := u.CachedTokens > 0 && u.TotalTokens >= u.InputTokens+u.CachedTokens+u.OutputTokens
	if !disjoint {
		fresh -= u.CachedTokens
		if fresh < 0 {
			fresh = 0
		}
	}
	totals := domain.ModelUsage{
		Model: model, InputTokens: fresh, CacheReadTokens: u.CachedTokens,
		OutputTokens: u.OutputTokens, Turns: turns,
	}
	// Merge into the credits-only row of this model, or the only row there is.
	target := -1
	for i := range rows {
		if rows[i].Model == model || len(rows) == 1 {
			target = i
			break
		}
	}
	if target < 0 {
		// Credits-only rows already carry the turns they were charged for; the
		// appended totals row counts only the rest, so the sum is not doubled.
		for _, r := range rows {
			totals.Turns -= r.Turns
		}
		if totals.Turns < 0 {
			totals.Turns = 0
		}
		return append(rows, totals), nil
	}
	rows[target].InputTokens, rows[target].CacheReadTokens, rows[target].OutputTokens =
		totals.InputTokens, totals.CacheReadTokens, totals.OutputTokens
	// The ledger rows already carry the turns they were charged for; give the
	// merged row only the finished turns no row accounts for, so the column sums
	// to the conversation's turn count rather than exceeding it.
	var counted int64
	for _, r := range rows {
		counted += r.Turns
	}
	if turns > counted {
		rows[target].Turns += turns - counted
	}
	return rows, nil
}

func (s *Service) runningContextBreakdown(ctx context.Context, id domain.SessionID) (domain.ContextBreakdown, bool, error) {
	controller, err := s.Controller(id)
	if errors.Is(err, ErrNoController) {
		return domain.ContextBreakdown{}, false, nil // not running: no breakdown to give
	}
	if err != nil {
		return domain.ContextBreakdown{}, false, err
	}
	if controller.State() == ports.ChatControllerStopped {
		return domain.ContextBreakdown{}, false, nil
	}
	reader, ok := controller.conv.(ports.ChatContextBreakdownReader)
	if !ok {
		return domain.ContextBreakdown{}, false, nil
	}
	breakdown, supported, err := reader.ContextBreakdown(ctx)
	if err != nil {
		// The totals already on screen stay; a failed request is not a failed chat.
		s.log.Warn("context breakdown unavailable", "session", id, "error", err)
		return domain.ContextBreakdown{}, false, nil
	}
	return breakdown, supported, nil
}

// finishedTurns counts the conversation's finished turns for every harness; the
// per-model ledger counts only those that report spend by model.
func (s *Service) finishedTurns(ctx context.Context, id domain.SessionID) int64 {
	counter, ok := s.store.(interface {
		FinishedTurnsForSession(context.Context, domain.SessionID) (int64, error)
	})
	if !ok {
		return 0
	}
	n, err := counter.FinishedTurnsForSession(ctx, id)
	if err != nil {
		s.log.Warn("count finished turns", "session", id, "error", err)
		return 0
	}
	return n
}

// effectiveModelLiveTimeout bounds the provider read behind EffectiveModel.
const effectiveModelLiveTimeout = 2 * time.Second

// EffectiveModel is the model the session actually runs: the one pinned at
// spawn, else the one chosen for the conversation, else the default the live
// provider reports as current. It is empty when none is known yet.
func (s *Service) EffectiveModel(ctx context.Context, id domain.SessionID) string {
	record, err := s.requireChatSession(ctx, id)
	if err != nil {
		return ""
	}
	if record.Metadata.Model != "" {
		return record.Metadata.Model
	}
	if conversation, cerr := s.store.ConversationForSession(ctx, id); cerr == nil && conversation.Settings.Model != "" {
		return conversation.Settings.Model
	}
	// Never wait for the controller gate or a slow provider: this serves plain
	// reads (session get), which must not hang behind a start or switch.
	controller, _, release, gerr := s.tryReadingController(ctx, id)
	if gerr != nil {
		return ""
	}
	defer release()
	configurer, ok := controller.conv.(ports.ChatConfigOptionController)
	if !ok {
		return ""
	}
	liveCtx, cancel := context.WithTimeout(ctx, effectiveModelLiveTimeout)
	defer cancel()
	options, oerr := configurer.ListConfigOptions(liveCtx)
	if oerr != nil {
		return ""
	}
	for _, o := range options {
		if o.ID == "model" || o.Category == "model" {
			return o.Current.Select
		}
	}
	return ""
}
