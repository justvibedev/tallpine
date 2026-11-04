package chat

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"tallpine.eu/tallpine/backend/internal/contextwindow"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// liveWindowConv is a running agent whose bridge may or may not change the
// compaction window live.
type liveWindowConv struct {
	ports.ChatConversation
	applied bool
	err     error
	calls   []int
}

func (c *liveWindowConv) SetAutoCompactWindow(_ context.Context, tokens int) (bool, error) {
	c.calls = append(c.calls, tokens)
	return c.applied, c.err
}

// plainConv has no live-window ability at all (an unpatched or other bridge).
type plainConv struct{ ports.ChatConversation }

func compactionService(def *string) *Service {
	return &Service{
		log:                slog.New(slog.NewTextHandler(io.Discard, nil)),
		modelCatalog:       windowCatalog{"opus": 1_000_000},
		autoCompactDefault: func(context.Context, domain.AgentHarness) string { return *def },
	}
}

func claudeController(conv ports.ChatConversation, live int, own string) *Controller {
	return &Controller{
		sessionID:   "s1",
		harness:     domain.HarnessClaudeCode,
		conv:        conv,
		compactLive: live,
		settings:    domain.ConversationSettings{Model: "opus", AutoCompactWindow: own},
	}
}

func TestReconcileCompactionAppliesTheNewDefaultToARunningChat(t *testing.T) {
	def := "40%"
	s := compactionService(&def)
	conv := &liveWindowConv{applied: true}
	c := claudeController(conv, 400_000, "")

	if s.reconcileCompaction(context.Background(), c) || len(conv.calls) != 0 {
		t.Fatalf("already at 40%% of 1M: calls = %v, want none", conv.calls)
	}
	def = "70%"
	if s.compactionPending(context.Background(), c) != true {
		t.Fatal("a changed default is pending until applied")
	}
	if pending := s.reconcileCompaction(context.Background(), c); pending {
		t.Fatal("applied live, but still reported pending")
	}
	if len(conv.calls) != 1 || conv.calls[0] != 700_000 {
		t.Fatalf("calls = %v, want one call with 700000", conv.calls)
	}
	if s.compactionPending(context.Background(), c) {
		t.Fatal("pending after a live apply")
	}
}

func TestReconcileCompactionWaitsForARestartWhenTheBridgeCannotChangeItLive(t *testing.T) {
	def := "40%"
	s := compactionService(&def)
	for name, conv := range map[string]ports.ChatConversation{
		"bridge declines": &liveWindowConv{applied: false},
		"bridge errors":   &liveWindowConv{err: errors.New("boom")},
		"no live ability": &plainConv{},
	} {
		c := claudeController(conv, 200_000, "")
		if !s.reconcileCompaction(context.Background(), c) {
			t.Errorf("%s: want pending (applies after restart)", name)
		}
		if c.compactLive != 200_000 {
			t.Errorf("%s: live window moved to %d without being applied", name, c.compactLive)
		}
		if !s.compactionPending(context.Background(), c) {
			t.Errorf("%s: snapshot should report pending", name)
		}
	}
}

func TestReconcileCompactionBackToAutoClearsTheLiveWindow(t *testing.T) {
	def := ""
	s := compactionService(&def)
	conv := &liveWindowConv{applied: true}
	c := claudeController(conv, 400_000, "")
	if s.reconcileCompaction(context.Background(), c) {
		t.Fatal("pending after clearing")
	}
	if len(conv.calls) != 1 || conv.calls[0] != 0 || c.compactLive != 0 {
		t.Fatalf("calls = %v live = %d, want one clear call and live 0", conv.calls, c.compactLive)
	}
}

func TestReconcileCompactionSkipsHarnessesWithoutTheCapability(t *testing.T) {
	def := "40%"
	s := compactionService(&def)
	conv := &liveWindowConv{applied: true}
	c := claudeController(conv, 0, "")
	c.harness = domain.HarnessKiro
	if s.reconcileCompaction(context.Background(), c) || s.compactionPending(context.Background(), c) || len(conv.calls) != 0 {
		t.Fatalf("kiro chat must not report or apply a compaction window; calls = %v", conv.calls)
	}
}

func TestApplyCompactionDefaultSkipsChatsWithTheirOwnChoice(t *testing.T) {
	def := "50%"
	s := compactionService(&def)
	follower := &liveWindowConv{applied: true}
	own := &liveWindowConv{applied: true}
	s.controllers = map[domain.SessionID]*Controller{
		"follower": claudeController(follower, 0, ""),
		"own":      claudeController(own, 0, "200k"),
	}
	s.ApplyCompactionDefault(context.Background(), domain.HarnessClaudeCode)
	if len(follower.calls) != 1 || follower.calls[0] != 500_000 {
		t.Fatalf("follower calls = %v, want [500000]", follower.calls)
	}
	if len(own.calls) != 0 {
		t.Fatalf("a chat with its own window was changed: %v", own.calls)
	}
}

func TestLaunchedCompactTokensReadsWhatTheAgentStartedWith(t *testing.T) {
	claude := StartConfig{Harness: domain.HarnessClaudeCode, Env: map[string]string{contextwindow.ClaudeAutoCompactWindowEnv: "400000"}}
	for _, tt := range []struct {
		name       string
		cfg        StartConfig
		reattached bool
		want       int
	}{
		{"claude with a window", claude, false, 400_000},
		{"claude on auto", StartConfig{Harness: domain.HarnessClaudeCode}, false, 0},
		{"garbage value", StartConfig{Harness: domain.HarnessClaudeCode, Env: map[string]string{contextwindow.ClaudeAutoCompactWindowEnv: "x"}}, false, 0},
		{"reattached process is unknown", claude, true, compactLiveUnknown},
		{"codex has none", StartConfig{Harness: domain.HarnessCodex, Env: claude.Env}, false, 0},
	} {
		if got := launchedCompactTokens(tt.cfg, tt.reattached); got != tt.want {
			t.Errorf("%s: = %d, want %d", tt.name, got, tt.want)
		}
	}
}

// An unknown launch window (a reattached provider) is pending until a live
// apply proves otherwise.
func TestReattachedAgentIsReconciledToTheChosenWindow(t *testing.T) {
	def := "40%"
	s := compactionService(&def)
	conv := &liveWindowConv{applied: true}
	c := claudeController(conv, compactLiveUnknown, "")
	if !s.compactionPending(context.Background(), c) {
		t.Fatal("unknown live window should read as pending")
	}
	s.reconcileCompaction(context.Background(), c)
	if len(conv.calls) != 1 || conv.calls[0] != 400_000 || c.compactLive != 400_000 {
		t.Fatalf("calls = %v live = %d", conv.calls, c.compactLive)
	}
}

// A restart reuses the saved start config. The window Tallpine put there for the
// first start must not outlive a change back to Auto; one the project set stays.
func TestRestartDropsOnlyTheWindowTallpineSet(t *testing.T) {
	saved := map[string]string{contextwindow.ClaudeAutoCompactWindowEnv: "400000", "KEEP": "1"}
	cfg := StartConfig{Harness: domain.HarnessClaudeCode, Env: saved, CompactWindowFromTallpine: true}
	dropStaleCompactWindow(&cfg)
	if _, present := cfg.Env[contextwindow.ClaudeAutoCompactWindowEnv]; present || cfg.Env["KEEP"] != "1" || cfg.CompactWindowFromTallpine {
		t.Fatalf("env after restart = %v (fromTallpine %v), want only KEEP", cfg.Env, cfg.CompactWindowFromTallpine)
	}
	if saved[contextwindow.ClaudeAutoCompactWindowEnv] != "400000" {
		t.Fatal("the saved start config's map was modified in place")
	}

	project := StartConfig{Harness: domain.HarnessClaudeCode, Env: map[string]string{contextwindow.ClaudeAutoCompactWindowEnv: "300000"}}
	dropStaleCompactWindow(&project)
	if project.Env[contextwindow.ClaudeAutoCompactWindowEnv] != "300000" {
		t.Fatal("a window from the project's own environment was removed")
	}
}

// A window the project's environment sets is what Auto means: a session that
// launched with it is not pending, and nothing is sent that would drop it.
func TestAutoKeepsTheProjectsOwnCompactionWindow(t *testing.T) {
	def := ""
	s := compactionService(&def)
	conv := &liveWindowConv{applied: true}
	c := claudeController(conv, 250_000, "")
	c.compactBaseline = 250_000
	if s.compactionPending(context.Background(), c) {
		t.Fatal("Auto with a project-set window reads as pending forever")
	}
	if s.reconcileCompaction(context.Background(), c) || len(conv.calls) != 0 {
		t.Fatalf("a reconcile touched the project's window: calls = %v", conv.calls)
	}
	// Going from a chosen window back to Auto returns to the project's, not to none.
	c.compactLive = 400_000
	if s.reconcileCompaction(context.Background(), c) {
		t.Fatal("pending after moving back to the project's window")
	}
	if len(conv.calls) != 1 || conv.calls[0] != 250_000 || c.compactLive != 250_000 {
		t.Fatalf("calls = %v live = %d, want one call with 250000", conv.calls, c.compactLive)
	}
}

func TestProjectCompactionWindowSurvivesAOsValueAndARestart(t *testing.T) {
	project := StartConfig{Harness: domain.HarnessClaudeCode, Env: map[string]string{contextwindow.ClaudeAutoCompactWindowEnv: "250000"}}
	if got := projectCompactTokens(project); got != 250_000 {
		t.Fatalf("project window = %d", got)
	}
	// Tallpine overrides it; the project's value is remembered, then restored on restart.
	overridden := StartConfig{
		Harness: domain.HarnessClaudeCode, CompactWindowFromTallpine: true, ProjectCompactWindow: "250000",
		Env: map[string]string{contextwindow.ClaudeAutoCompactWindowEnv: "400000"},
	}
	if got := projectCompactTokens(overridden); got != 250_000 {
		t.Fatalf("project window behind Tallpine's value = %d, want 250000", got)
	}
	// The same path the start takes: Tallpine's value over the project's, twice (a restart
	// recomputes), must still remember the project's original.
	fresh := StartConfig{Harness: domain.HarnessClaudeCode, Env: map[string]string{contextwindow.ClaudeAutoCompactWindowEnv: "250000"}}
	setTallpineCompactWindow(&fresh, 400_000)
	if fresh.Env[contextwindow.ClaudeAutoCompactWindowEnv] != "400000" || fresh.ProjectCompactWindow != "250000" || !fresh.CompactWindowFromTallpine {
		t.Fatalf("after Tallpine's value: env %v project %q", fresh.Env, fresh.ProjectCompactWindow)
	}
	if projectCompactTokens(fresh) != 250_000 {
		t.Fatal("the project window was lost behind Tallpine's value")
	}
	dropStaleCompactWindow(&overridden)
	if overridden.Env[contextwindow.ClaudeAutoCompactWindowEnv] != "250000" || overridden.ProjectCompactWindow != "" || overridden.CompactWindowFromTallpine {
		t.Fatalf("after restart: env %v fromTallpine %v remembered %q", overridden.Env, overridden.CompactWindowFromTallpine, overridden.ProjectCompactWindow)
	}
	if got := projectCompactTokens(StartConfig{Harness: domain.HarnessCodex, Env: project.Env}); got != 0 {
		t.Fatalf("codex baseline = %d", got)
	}
}

func TestOverlayCompactionViewReportsTheProjectsWindowForClaudeOnly(t *testing.T) {
	s := compactionService(new(string))
	c := claudeController(&plainConv{}, 250_000, "")
	c.compactBaseline = 250_000
	var settings domain.ConversationSettings
	s.overlayCompactionView(context.Background(), &settings, c, true)
	if settings.AutoCompactProjectTokens != 250_000 || !settings.AutoCompactPending {
		t.Fatalf("settings = %+v, want project window 250000 and pending", settings)
	}
	c.harness = domain.HarnessKiro
	s.overlayCompactionView(context.Background(), &settings, c, false)
	if settings.AutoCompactProjectTokens != 0 || settings.AutoCompactPending {
		t.Fatalf("kiro settings = %+v, want neither", settings)
	}
}

// A chat on the provider default has no saved model, so the catalog is asked
// for "" and may name no default; the percent must then use the window the
// agent last reported, or Settings' 40% would silently never apply.
func TestDesiredCompactTokensResolvesAPercentForADefaultModelChat(t *testing.T) {
	ctx := context.Background()
	def := "40%"
	s := compactionService(&def)
	c := claudeController(&plainConv{}, 0, "")
	c.settings.Model = ""
	c.usage.ContextWindow = 1_000_000
	if got := s.desiredCompactTokens(ctx, c); got != 400_000 {
		t.Fatalf("live: desiredCompactTokens = %d, want 400000 (40%% of the reported window)", got)
	}
	// After a restart the launch path reads the stored usage the same way.
	record := domain.ConversationRecord{Usage: &domain.ConversationUsage{ContextWindow: 1_000_000}}
	window := s.compactModelWindow(ctx, StartConfig{Harness: domain.HarnessClaudeCode}, record, "")
	if tokens := domain.AutoCompactTokenCount("40%", window); tokens != 400_000 {
		t.Fatalf("launch: tokens = %d, want 400000", tokens)
	}
}

// A brand-new default-model chat has no usage yet; the percent still resolves
// from the catalog's default model, else its largest window, because the env var
// is fixed at launch.
func TestCompactModelWindowForAFreshDefaultModelChat(t *testing.T) {
	ctx := context.Background()
	s := &Service{modelCatalog: windowCatalog{"sonnet": 200_000, "opus": 1_000_000}}
	window := s.compactModelWindow(ctx, StartConfig{Harness: domain.HarnessClaudeCode}, domain.ConversationRecord{}, "")
	if tokens := domain.AutoCompactTokenCount("40%", window); tokens != 400_000 {
		t.Fatalf("no default marked: tokens = %d, want 400000 from the largest window", tokens)
	}
}

// Codex has the capability: its window travels in its own env key, a live
// change cannot be applied (no setter), so it stays pending until the next
// thread/resume.
func TestCodexCompactionWindowUsesItsOwnKeyAndStaysPending(t *testing.T) {
	cfg := StartConfig{Harness: domain.HarnessCodex}
	setTallpineCompactWindow(&cfg, 300_000)
	if cfg.Env[contextwindow.CodexAutoCompactLimitEnv] != "300000" || cfg.Env[contextwindow.ClaudeAutoCompactWindowEnv] != "" {
		t.Fatalf("env = %v", cfg.Env)
	}
	if got := launchedCompactTokens(cfg, false); got != 300_000 {
		t.Fatalf("launched = %d", got)
	}
	def := "40%"
	s := compactionService(&def)
	c := claudeController(&plainConv{}, 0, "")
	c.harness = domain.HarnessCodex
	if !s.reconcileCompaction(context.Background(), c) || !s.compactionPending(context.Background(), c) {
		t.Fatal("a codex window change must stay pending until the next resume")
	}
}

// The window state belongs to the source harness's key: a switch must neither
// copy a project's Claude window into Codex nor delete it going back.
func TestDropStaleCompactWindowUsesTheSourceHarnessKey(t *testing.T) {
	claude := StartConfig{Harness: domain.HarnessClaudeCode, CompactWindowFromTallpine: true, ProjectCompactWindow: "250000",
		Env: map[string]string{contextwindow.ClaudeAutoCompactWindowEnv: "400000"}}
	retargetCompactWindow(&claude, domain.HarnessCodex)
	if v, ok := claude.Env[contextwindow.CodexAutoCompactLimitEnv]; ok || claude.Env[contextwindow.ClaudeAutoCompactWindowEnv] != "250000" {
		t.Fatalf("claude->codex env = %v (codex limit %q)", claude.Env, v)
	}
	codex := StartConfig{Harness: domain.HarnessCodex, CompactWindowFromTallpine: true,
		Env: map[string]string{contextwindow.CodexAutoCompactLimitEnv: "300000", contextwindow.ClaudeAutoCompactWindowEnv: "250000"}}
	retargetCompactWindow(&codex, domain.HarnessClaudeCode)
	if _, ok := codex.Env[contextwindow.CodexAutoCompactLimitEnv]; ok || codex.Env[contextwindow.ClaudeAutoCompactWindowEnv] != "250000" {
		t.Fatalf("codex->claude env = %v", codex.Env)
	}
}
