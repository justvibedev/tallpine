package chat

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/contextwindow"
	"tallpine.eu/tallpine/backend/internal/domain"
)

type compactProjects map[string]domain.ProjectConfig

func (p compactProjects) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	cfg, ok := p[id]
	return domain.ProjectRecord{ID: id, Config: cfg}, ok, nil
}

// An orchestrator chat with no window of its own starts with the built-in
// 120k window; a worker is untouched; an explicit project choice or a window
// the project's env sets wins over the built-in one, the Settings default does not.
func TestApplyLaunchCompactionGivesOrchestratorsTheirDefault(t *testing.T) {
	for _, tc := range []struct {
		name    string
		kind    domain.SessionKind
		project domain.ProjectConfig
		def     string
		want    string
	}{
		{"orchestrator", domain.KindOrchestrator, domain.ProjectConfig{}, "", "120000"},
		{"worker", domain.KindWorker, domain.ProjectConfig{}, "", ""},
		// B398: a worker-sized Settings default (40% of a 1M model = 400k) no
		// longer shadows the orchestrator's 120k.
		{"settings default does not shadow it", domain.KindOrchestrator, domain.ProjectConfig{}, "40%", "120000"},
		{"project window wins", domain.KindOrchestrator, domain.ProjectConfig{Orchestrator: domain.RoleOverride{AutoCompactWindow: "50%"}}, "1m", "500000"},
		{"project off", domain.KindOrchestrator, domain.ProjectConfig{Orchestrator: domain.RoleOverride{AutoCompactWindow: domain.AutoCompactOff}}, "", ""},
		{"project env kept", domain.KindOrchestrator, domain.ProjectConfig{Env: map[string]string{contextwindow.ClaudeAutoCompactWindowEnv: "300000"}}, "", "300000"},
	} {
		def := tc.def
		s := compactionService(&def)
		s.projects = compactProjects{"p": tc.project}
		cfg := StartConfig{Harness: domain.HarnessClaudeCode, Kind: tc.kind, ProjectID: "p", Model: "opus", Env: map[string]string{}}
		for k, v := range tc.project.Env {
			cfg.Env[k] = v
		}
		conversation := domain.ConversationRecord{Settings: domain.ConversationSettings{Model: "opus"}}
		s.applyLaunchCompaction(context.Background(), &cfg, &conversation)
		if got := cfg.Env[contextwindow.ClaudeAutoCompactWindowEnv]; got != tc.want {
			t.Errorf("%s: window env = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A session's own choice still wins for an orchestrator.
func TestOrchestratorOwnCompactChoiceWins(t *testing.T) {
	def := ""
	s := compactionService(&def)
	s.projects = compactProjects{"p": {}}
	if got := s.effectiveCompactChoice(context.Background(), domain.HarnessClaudeCode, domain.KindOrchestrator, "p", "1m"); got != "1m" {
		t.Fatalf("own choice = %q, want 1m", got)
	}
	c := claudeController(&liveWindowConv{applied: true}, 0, "")
	c.kind = domain.KindOrchestrator
	if got := s.desiredCompactTokens(context.Background(), c); got != 120_000 {
		t.Fatalf("running orchestrator desired window = %d, want 120000", got)
	}
}

type settleOnlyStore struct{ Store }

func (settleOnlyStore) FailReservedQueuedBatch(context.Context, string, time.Time) error { return nil }

// The running controller must know it serves an orchestrator: reconcile and
// the chat view resolve its default window from that, and a controller that
// forgot would push a running orchestrator back to the agent's own default.
func TestBuildControllerKeepsTheOrchestratorKind(t *testing.T) {
	def := ""
	s := compactionService(&def)
	s.projects = compactProjects{"p": {}}
	s.store = settleOnlyStore{}
	s.now = time.Now
	l := &launch{
		cfg:                         &StartConfig{SessionID: "s1", Harness: domain.HarnessClaudeCode, Kind: domain.KindOrchestrator, ProjectID: "p"},
		conversation:                domain.ConversationRecord{ProjectID: "p", Settings: domain.ConversationSettings{Model: "opus"}},
		queuedBeforeFirstController: true,
	}
	if err := s.buildController(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	if got := s.desiredCompactTokens(context.Background(), l.controller); got != 120_000 {
		t.Fatalf("running orchestrator desired window = %d, want 120000", got)
	}
}

// An edit, rollback or branch replaces the controller; the replacement must
// still be an orchestrator with the project's env window, or its compaction
// default silently drops to the worker one.
func TestReplacementControllerKeepsOrchestratorCompaction(t *testing.T) {
	def := ""
	s := compactionService(&def)
	s.projects = compactProjects{"p": {}}
	source := claudeController(&liveWindowConv{applied: true}, 0, "")
	source.kind = domain.KindOrchestrator
	source.compactBaseline = 300_000
	source.conversation.ProjectID = "p"
	replacement := s.newReplacementController("s1", source, source.conversation, "g2", &liveWindowConv{applied: true})
	if replacement.kind != domain.KindOrchestrator || replacement.compactBaseline != 300_000 {
		t.Fatalf("replacement kind=%q baseline=%d, want orchestrator and 300000", replacement.kind, replacement.compactBaseline)
	}
	replacement.settings = source.settings
	if got := s.desiredCompactTokens(context.Background(), replacement); got != 120_000 {
		t.Fatalf("replacement desired window = %d, want 120000", got)
	}
}
