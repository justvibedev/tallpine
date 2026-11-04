package chat

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// IdleCompactionState is what the chat service knows about one session for
// idle compaction (B204).
type IdleCompactionState struct {
	// Busy covers a turn or compaction in flight and a controller that is not
	// ready (starting, waiting on an approval, handing off).
	Busy bool
	// ContextUsed is the conversation's current context size in tokens.
	ContextUsed int64
	// Threshold is domain.IdleCompactThreshold for the session's project
	// setting and effective compaction window; 0 disables idle compaction.
	Threshold int64
	// Handoff reports the project's idleAction is domain.IdleActionHandoff.
	Handoff bool
	// Research reports the orchestrator is in research mode (B413).
	Research bool
}

// IdleCompaction reports a live orchestrator controller's idle-compaction
// state. ok is false for a session without a live controller, one that is not
// an orchestrator, or one whose provider cannot compact.
func (s *Service) IdleCompaction(ctx context.Context, id domain.SessionID) (IdleCompactionState, bool) {
	c, err := s.Controller(id)
	if err != nil {
		return IdleCompactionState{}, false
	}
	return s.idleCompactionState(ctx, c)
}

func (s *Service) idleCompactionState(ctx context.Context, c *Controller) (IdleCompactionState, bool) {
	if c.kind != domain.KindOrchestrator {
		return IdleCompactionState{}, false
	}
	if _, ok := c.conv.(ports.ChatCompactor); !ok || !c.Capabilities().Has(ports.ChatCapabilityCompaction) {
		return IdleCompactionState{}, false
	}
	c.mu.Lock()
	usage := c.usage
	c.mu.Unlock()
	// The effective window is the one the provider compacts at on its own
	// (B193), else the model's.
	window := int64(s.desiredCompactTokensIn(ctx, c, false))
	if window <= 0 {
		window = usage.ContextWindow
	}
	cfg := s.compactProjectConfig(ctx, c.conversation.ProjectID)
	return IdleCompactionState{
		Busy:        c.busy() || c.State() != ports.ChatControllerReady,
		ContextUsed: usage.ContextUsed,
		Threshold:   domain.IdleCompactThreshold(cfg.Orchestrator.IdleCompactAt, window),
		Handoff:     cfg.Orchestrator.IdleAction == domain.IdleActionHandoff,
		Research:    c.researchActive(),
	}, true
}

// CompactIdle compacts an idle orchestrator between tasks, asking a provider
// that accepts instructions to keep what the next task needs
// (domain.IdleCompactInstructions). It refuses a busy controller like Compact.
func (s *Service) CompactIdle(ctx context.Context, id domain.SessionID) (ports.ChatCompactionResult, error) {
	controller, release, err := s.workingController(ctx, id)
	if err != nil {
		return ports.ChatCompactionResult{}, err
	}
	defer release()
	return controller.compactWith(ctx, domain.IdleCompactInstructions)
}
