package chat

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// plainCompactConv compacts without instructions (Codex).
type plainCompactConv struct {
	ports.ChatConversation
	plain int
}

func (*plainCompactConv) Capabilities() ports.ChatCapabilities {
	return ports.ChatCapabilities{ports.ChatCapabilityCompaction: true}
}

func (c *plainCompactConv) Compact(context.Context) (ports.ChatCompactionResult, error) {
	c.plain++
	return ports.ChatCompactionResult{}, nil
}

// instructedCompactConv also takes instructions (Claude Code).
type instructedCompactConv struct {
	plainCompactConv
	instructions []string
}

func (c *instructedCompactConv) CompactWithInstructions(_ context.Context, instructions string) (ports.ChatCompactionResult, error) {
	c.instructions = append(c.instructions, instructions)
	return ports.ChatCompactionResult{}, nil
}

func idleController(conv ports.ChatConversation, kind domain.SessionKind) *Controller {
	c := claudeController(conv, 0, "")
	c.kind = kind
	c.state = ports.ChatControllerReady
	c.conversation.ProjectID = "p"
	c.usage = domain.ConversationUsage{ContextUsed: 150_000, ContextWindow: 1_000_000}
	return c
}

// An orchestrator's threshold is 30% of its effective window (the 120k default,
// not the model's 1M); the project setting changes it; a worker has none.
func TestIdleCompactionStateResolvesTheThreshold(t *testing.T) {
	def := ""
	s := compactionService(&def)
	s.projects = compactProjects{"p": {}}
	ctx := context.Background()
	st, ok := s.idleCompactionState(ctx, idleController(&plainCompactConv{}, domain.KindOrchestrator))
	if !ok || st.Threshold != 36_000 || st.ContextUsed != 150_000 || st.Busy {
		t.Fatalf("orchestrator state = %+v ok=%v, want threshold 36000 (30%% of the 120k window)", st, ok)
	}
	s.projects = compactProjects{"p": {Orchestrator: domain.RoleOverride{IdleCompactAt: domain.AutoCompactOff}}}
	if st, _ := s.idleCompactionState(ctx, idleController(&plainCompactConv{}, domain.KindOrchestrator)); st.Threshold != 0 {
		t.Fatalf("off: threshold = %d, want 0", st.Threshold)
	}
	if _, ok := s.idleCompactionState(ctx, idleController(&plainCompactConv{}, domain.KindWorker)); ok {
		t.Fatal("a worker reported idle-compaction state")
	}
	busy := idleController(&plainCompactConv{}, domain.KindOrchestrator)
	busy.pendingTurnID = "t1"
	if st, _ := s.idleCompactionState(ctx, busy); !st.Busy {
		t.Fatal("a controller with a turn in flight is not busy")
	}
}

// Idle compaction passes its instructions where the provider takes them and
// compacts plainly elsewhere.
func TestCompactWithPassesInstructionsWhereSupported(t *testing.T) {
	instructed := &instructedCompactConv{}
	if _, err := idleController(instructed, domain.KindOrchestrator).compactWith(context.Background(), domain.IdleCompactInstructions); err != nil {
		t.Fatal(err)
	}
	if len(instructed.instructions) != 1 || instructed.instructions[0] != domain.IdleCompactInstructions || instructed.plain != 0 {
		t.Fatalf("instructed calls = %v plain = %d", instructed.instructions, instructed.plain)
	}
	plain := &plainCompactConv{}
	if _, err := idleController(plain, domain.KindOrchestrator).compactWith(context.Background(), domain.IdleCompactInstructions); err != nil {
		t.Fatal(err)
	}
	if plain.plain != 1 {
		t.Fatalf("plain compactions = %d, want 1", plain.plain)
	}
}

// The project's idleAction reaches the sweep as the Handoff flag.
func TestIdleCompactionStateCarriesTheHandoffChoice(t *testing.T) {
	def := ""
	s := compactionService(&def)
	s.projects = compactProjects{"p": {Orchestrator: domain.RoleOverride{IdleAction: domain.IdleActionHandoff}}}
	st, ok := s.idleCompactionState(context.Background(), idleController(&plainCompactConv{}, domain.KindOrchestrator))
	if !ok || !st.Handoff {
		t.Fatalf("state = %+v ok=%v, want Handoff", st, ok)
	}
	s.projects = compactProjects{"p": {}}
	if st, _ := s.idleCompactionState(context.Background(), idleController(&plainCompactConv{}, domain.KindOrchestrator)); st.Handoff {
		t.Fatal("default project reported Handoff")
	}
}
