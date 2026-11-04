package chat

import (
	"context"
	"strconv"

	"tallpine.eu/tallpine/backend/internal/contextwindow"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// ApplyProjectCompaction is called after a project's config changes: its
// running orchestrators without a choice of their own move to the window the
// project now resolves to (orchestrator.autoCompactWindow or its environment)
// where the agent allows, and the rest on their next restart.
func (s *Service) ApplyProjectCompaction(ctx context.Context, project domain.ProjectID) {
	s.mu.RLock()
	var targets []*Controller
	for _, c := range s.controllers {
		if c.kind == domain.KindOrchestrator && c.conversation.ProjectID == project {
			targets = append(targets, c)
		}
	}
	s.mu.RUnlock()
	if len(targets) == 0 {
		return
	}
	env := s.compactProjectConfig(ctx, project).Env
	for _, c := range targets {
		// The baseline was read from the launch environment; follow the
		// project's current one, as projectCompactTokens would at a restart.
		baseline := 0
		if key := contextwindow.AutoCompactEnvKey(c.harness); key != "" {
			if tokens, err := strconv.Atoi(env[key]); err == nil && tokens > 0 {
				baseline = tokens
			}
		}
		c.mu.Lock()
		c.compactBaseline = baseline
		c.mu.Unlock()
		if c.Settings().AutoCompactWindow == "" {
			s.reconcileCompaction(ctx, c)
		}
	}
}

// projectCompactBaseline reads compactBaseline under the controller lock:
// ApplyProjectCompaction rewrites it from a project config write.
func (c *Controller) projectCompactBaseline() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.compactBaseline
}
