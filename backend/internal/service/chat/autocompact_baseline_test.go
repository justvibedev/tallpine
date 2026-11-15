package chat

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/contextwindow"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// fixedBaselines measures every session at one baseline and records the kinds
// it was asked for.
type fixedBaselines struct {
	baseline int64
	asked    []domain.SessionKind
}

func (b *fixedBaselines) Baseline(_ context.Context, _ domain.ProjectID, kind domain.SessionKind, _ domain.AgentHarness) (int64, error) {
	b.asked = append(b.asked, kind)
	return b.baseline, nil
}

// An orchestrator's 120k window with a measured 51k baseline launches at 151k
// (B735), and the launch config carries the baseline to the controller.
func TestApplyLaunchCompactionFloorsTheOrchestratorWindowAtTheBaseline(t *testing.T) {
	def := ""
	s := compactionService(&def)
	s.projects = compactProjects{"p": {}}
	reader := &fixedBaselines{baseline: 51_000}
	s.autoCompactBaselines = reader
	cfg := StartConfig{Harness: domain.HarnessClaudeCode, Kind: domain.KindOrchestrator, ProjectID: "p", Model: "opus", Env: map[string]string{}}
	conversation := domain.ConversationRecord{Settings: domain.ConversationSettings{Model: "opus"}}
	s.applyLaunchCompaction(context.Background(), &cfg, &conversation)
	if got := cfg.Env[contextwindow.ClaudeAutoCompactWindowEnv]; got != "151000" {
		t.Fatalf("window env = %q, want 151000", got)
	}
	if cfg.AutoCompactBaseline != 51_000 || len(reader.asked) != 1 || reader.asked[0] != domain.KindOrchestrator {
		t.Fatalf("baseline %d asked %v, want 51000 read once for an orchestrator", cfg.AutoCompactBaseline, reader.asked)
	}
}

// The running controller keeps its launch baseline: the desired window, the
// idle threshold and the readout all use it, and a replacement controller
// keeps it.
func TestControllerUsesItsLaunchBaseline(t *testing.T) {
	def := ""
	s := compactionService(&def)
	s.projects = compactProjects{"p": {}}
	s.store = settleOnlyStore{}
	s.now = time.Now
	l := &launch{
		cfg:                         &StartConfig{SessionID: "s1", Harness: domain.HarnessClaudeCode, Kind: domain.KindOrchestrator, ProjectID: "p", AutoCompactBaseline: 51_000},
		conversation:                domain.ConversationRecord{ProjectID: "p", Settings: domain.ConversationSettings{Model: "opus"}},
		queuedBeforeFirstController: true,
	}
	if err := s.buildController(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if got := s.desiredCompactTokens(ctx, l.controller); got != 151_000 {
		t.Fatalf("desired window = %d, want 151000", got)
	}
	replacement := s.newReplacementController("s1", l.controller, l.controller.conversation, "g2", &liveWindowConv{applied: true})
	if replacement.startBaseline != 51_000 {
		t.Fatalf("replacement baseline = %d, want 51000", replacement.startBaseline)
	}
	var view domain.ConversationSettings
	view.Model = "opus"
	s.overlayCompactionView(ctx, &view, l.controller, false)
	if view.AutoCompactBaseline != 51_000 || view.CompactThreshold != 151_000 || view.AutoCompactTrigger != 118_000 {
		t.Fatalf("view baseline %d window %d trigger %d, want 51000 151000 118000", view.AutoCompactBaseline, view.CompactThreshold, view.AutoCompactTrigger)
	}
}

// The idle threshold is at least baseline + 30k: 30% of 151k (45.3k) is
// raised to 81k by a 51k baseline.
func TestIdleThresholdKeepsRoomAboveTheBaseline(t *testing.T) {
	def := ""
	s := compactionService(&def)
	s.projects = compactProjects{"p": {}}
	c := idleController(&plainCompactConv{}, domain.KindOrchestrator)
	c.startBaseline = 51_000
	st, ok := s.idleCompactionState(context.Background(), c)
	if !ok || st.Threshold != 81_000 {
		t.Fatalf("idle threshold = %d ok=%v, want 81000", st.Threshold, ok)
	}
}
