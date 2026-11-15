package chat

import (
	"context"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Research windows and their persistence (B736). `research on --window` picks
// a rung of domain.OrchestratorWindowLadder; an orchestrator whose turn stopped
// on compaction thrash steps one rung up and restarts to resume. Research mode
// is kept on the session so a daemon restart restores it, and every end of
// research mode (off, idle compaction, handoff, the quiet hour) clears it.

var (
	// ErrResearchWindowInvalid means --window is not a rung of the ladder.
	ErrResearchWindowInvalid = errors.New("research window must be 250k, 400k or 1m")
	// ErrResearchWindowTooLarge means the window is larger than the
	// orchestrator's model can hold.
	ErrResearchWindowTooLarge = errors.New("research window does not fit the orchestrator's model")
)

func (s *Service) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now().UTC()
}

// researchController is the live controller of an orchestrator that can use
// research mode. A worker, a terminal-mode orchestrator and one on another
// agent than Claude Code get ErrResearchUnsupported.
func (s *Service) researchController(ctx context.Context, id domain.SessionID) (*Controller, error) {
	c, err := s.Controller(id)
	if err == nil {
		if c.kind != domain.KindOrchestrator || c.harness != domain.HarnessClaudeCode {
			return nil, ErrResearchUnsupported
		}
		return c, nil
	}
	if s.sessions != nil {
		rec, found, readErr := s.sessions.GetSession(ctx, id)
		if readErr == nil && found && !researchCapable(rec) {
			return nil, ErrResearchUnsupported
		}
	}
	return nil, err
}

// researchCapable reports whether a session could use research mode: a
// chat-mode Claude Code orchestrator.
func researchCapable(rec domain.SessionRecord) bool {
	return rec.Kind == domain.KindOrchestrator && rec.Harness == domain.HarnessClaudeCode &&
		domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat
}

// SetResearchWindow switches an orchestrator's research mode on at window, a
// rung of domain.OrchestratorWindowLadder, or moves a running research mode to
// it. A window larger than the model's context window is refused.
func (s *Service) SetResearchWindow(ctx context.Context, id domain.SessionID, window string) (ResearchStatus, error) {
	if !domain.ValidResearchWindow(window) {
		return ResearchStatus{}, ErrResearchWindowInvalid
	}
	c, err := s.researchController(ctx, id)
	if err != nil {
		return ResearchStatus{}, err
	}
	if modelWindow, _ := s.modelCapacity(ctx, c.harness, c.conversation.ProjectID, c.Settings().Model); !domain.ResearchWindowFits(window, modelWindow) {
		return ResearchStatus{}, fmt.Errorf("%w: %s is larger than the model's %dk-token context window", ErrResearchWindowTooLarge, window, modelWindow/1000)
	}
	c.research.mu.Lock()
	defer c.research.mu.Unlock()
	return s.switchResearchOn(ctx, c, window, false)
}

// persistResearch writes research mode to the session; nil clears it. A failed
// write is logged: the live state still applies, only a daemon restart loses it.
func (s *Service) persistResearch(ctx context.Context, id domain.SessionID, research *domain.SessionResearch) {
	if s.researchStore == nil {
		return
	}
	if err := s.researchStore.SetSessionResearch(ctx, id, research); err != nil {
		s.log.Warn("could not persist orchestrator research mode", "session", id, "on", research != nil, "error", err)
	}
}

// restoreResearch carries the persisted research mode of id to its next
// launch, unless one is carried already. One whose quiet hour ran out while no
// controller watched it is cleared instead.
func (s *Service) restoreResearch(ctx context.Context, id domain.SessionID, harness domain.AgentHarness) {
	if s.researchStore == nil || harness != domain.HarnessClaudeCode || s.researchCarried.has(id) {
		return
	}
	research, found, err := s.researchStore.SessionResearch(ctx, id)
	if err != nil {
		s.log.Warn("could not read the persisted orchestrator research mode", "session", id, "error", err)
		return
	}
	if !found {
		return
	}
	last := research.LastTurnAt
	if last.IsZero() {
		last = research.Since
	}
	if s.clock().Sub(last) >= domain.ResearchQuietExpiry {
		s.persistResearch(ctx, id, nil)
		s.log.Info("orchestrator research mode expired after a quiet hour", "session", id)
		return
	}
	s.researchCarried.put(id, carriedResearch{SessionResearch: research})
}

// clearStoredResearch ends research mode of an orchestrator with no live
// controller (hibernated or stopped), and reports whether it was on.
func (s *Service) clearStoredResearch(ctx context.Context, id domain.SessionID) bool {
	_, carried := s.researchCarried.take(id)
	if s.researchStore == nil {
		return carried
	}
	_, found, err := s.researchStore.SessionResearch(ctx, id)
	if err != nil || !found {
		return carried
	}
	s.persistResearch(ctx, id, nil)
	return true
}

// researchTurnEnded runs when an orchestrator's turn ends: a research turn
// moves the quiet-hour clock, and a turn stopped on compaction thrash steps
// the window up (in the background: it may wait for the agent to restart).
func (s *Service) researchTurnEnded(ctx context.Context, c *Controller, providerTurnID, thrash string) {
	if c.kind != domain.KindOrchestrator {
		return
	}
	now := s.clock()
	c.mu.Lock()
	on := c.research.on
	if on {
		c.research.lastTurn = now
	}
	c.mu.Unlock()
	if on && s.researchStore != nil {
		if err := s.researchStore.TouchSessionResearch(ctx, c.sessionID, now); err != nil {
			s.log.Warn("could not record the research turn", "session", c.sessionID, "error", err)
		}
	}
	if thrash != "" {
		go s.stepUpOnThrash(context.WithoutCancel(ctx), c, providerTurnID)
	}
}

// stepUpOnThrash raises a chat-mode Claude Code orchestrator's compaction
// window one rung after its turn stopped on compaction thrash, and restarts it
// to resume. A notice steps at most once, and not while a step waits for its
// restart. There is no step at the agent's own default (1m), when the next
// rung does not fit the model, when the chat has a window of its own, or when
// research mode is turned off. It reports whether it stepped.
func (s *Service) stepUpOnThrash(ctx context.Context, c *Controller, providerTurnID string) bool {
	if c.kind != domain.KindOrchestrator || c.harness != domain.HarnessClaudeCode {
		return false
	}
	c.research.mu.Lock()
	defer c.research.mu.Unlock()
	c.mu.Lock()
	repeat := c.research.restartPending || (providerTurnID != "" && providerTurnID == c.research.stepTurn)
	if !repeat {
		c.research.stepTurn = providerTurnID
	}
	current := c.compactLive
	c.mu.Unlock()
	if repeat || c.Settings().AutoCompactWindow != "" {
		return false
	}
	if _, allowed := s.researchChoice(ctx, c); !allowed {
		return false
	}
	if current < 0 {
		current = s.desiredCompactTokens(ctx, c)
	}
	modelWindow, _ := s.modelCapacity(ctx, c.harness, c.conversation.ProjectID, c.Settings().Model)
	next, ok := domain.NextOrchestratorWindow(current, modelWindow)
	if !ok {
		s.log.Info("compaction thrash: no larger window to step up to", "session", c.sessionID, "current", current, "modelWindow", modelWindow)
		return false
	}
	if _, err := s.switchResearchOn(ctx, c, next, true); err != nil {
		s.log.Warn("compaction thrash: could not raise the window", "session", c.sessionID, "window", next, "error", err)
		return false
	}
	s.log.Info("compaction thrash: window stepped up", "session", c.sessionID, "from", current, "window", next)
	return true
}

// announceThrashStep writes the one chat line a thrash step-up gets.
// restart says the window waits for the agent's restart.
func (s *Service) announceThrashStep(ctx context.Context, c *Controller, from int, restart bool) {
	previous := "the coordination window"
	if from > 0 {
		previous = fmt.Sprintf("%dk tokens", from/1000)
	}
	line := fmt.Sprintf("Compaction thrash stopped the last turn: compaction window raised from %s to %s", previous, s.researchWindowLabel(ctx, c))
	if restart {
		line += "; the agent restarts when idle and resumes"
	}
	s.announceSummary(ctx, c, line)
}
