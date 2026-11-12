package chat

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Orchestrator research mode (B413). An orchestrator that must examine a large
// body of code itself switches it on; its compaction window then rises to the
// research window until it switches it off, an idle compaction or handoff
// ends it, or it has no turn for domain.ResearchQuietExpiry. The state lives
// in memory on the controller. Claude Code applies a window only at launch, so
// switching on restarts the idle agent and the flag is carried across that
// restart (compaction_research_restart.go); a daemon restart drops back to
// coordination.

var (
	// ErrResearchUnsupported means the session is not an orchestrator whose
	// agent can change its compaction window while it runs.
	ErrResearchUnsupported = errors.New("orchestrator research mode is not supported for this agent")
	// ErrResearchDisabled means the global Settings toggle or the project's
	// orchestrator.researchCompactWindow turned research mode off.
	ErrResearchDisabled = errors.New("orchestrator research mode is turned off")
	// ErrResearchRequestFailed means the request to the agent failed (timeout,
	// closed session, bad reply), so what it would do is unknown; a retry may work.
	ErrResearchRequestFailed = errors.New("could not ask the agent to change its compaction window")
)

// researchState is a controller's research mode. on and since (when it was
// switched on) are in memory only and guarded by Controller.mu; mu serializes
// switches so a flag change and its live window change stay together.
// restartPending is set while the flag waits for the agent's restart to apply
// the research window.
type researchState struct {
	on             bool
	since          time.Time
	restartPending bool
	mu             sync.Mutex
}

// ResearchStatus is one orchestrator's research mode.
type ResearchStatus struct {
	// Active reports research mode is on and in effect.
	Active bool
	// Available reports the session could switch it on (feature enabled, agent
	// supported).
	Available bool
	// Window is the compaction window now in effect, in tokens; 0 is the
	// agent's own default.
	Window int
	// ResearchWindow is the window research mode would use or uses; "" when
	// research mode is off for the project or globally.
	ResearchWindow string
	// Since is when research mode was switched on; zero when it is off.
	Since time.Time
	// RestartPending reports research mode is switched on but waits for the
	// agent to be idle and restart before the window applies.
	RestartPending bool
}

func (s *Service) dynamicContextEnabled(ctx context.Context) bool {
	return s.dynamicContext == nil || s.dynamicContext(ctx)
}

// researchChoice is the compaction choice research mode would apply to c, ok
// false when research mode is unavailable for it.
func (s *Service) researchChoice(ctx context.Context, c *Controller) (string, bool) {
	if c.kind != domain.KindOrchestrator {
		return "", false
	}
	return domain.ResearchCompactChoice(s.compactProjectConfig(ctx, c.conversation.ProjectID), s.dynamicContextEnabled(ctx))
}

// researchInEffect reports whether c compacts at the research window now:
// switched on, still allowed, and the owner chose no window for this chat
// (their explicit choice wins).
func (s *Service) researchInEffect(ctx context.Context, c *Controller) (string, bool) {
	_, choice, ok := s.researchTarget(ctx, c)
	return choice, ok
}

// researchTarget is the window research mode puts c at, in tokens (0 is the
// agent's own default), and the choice it came from. ok is false unless research
// is switched on, allowed, and raises the window: research never lowers it, so
// a coordination window that is larger (or the agent's default, the largest)
// stays.
func (s *Service) researchTarget(ctx context.Context, c *Controller) (tokens int, choice string, ok bool) {
	c.mu.Lock()
	on := c.research.on
	c.mu.Unlock()
	if !on || c.Settings().AutoCompactWindow != "" {
		return 0, "", false
	}
	choice, allowed := s.researchChoice(ctx, c)
	if !allowed {
		return 0, "", false
	}
	tokens = s.researchTokens(ctx, c, choice)
	if tokens == 0 {
		tokens = c.projectCompactBaseline()
	}
	return tokens, choice, windowLarger(tokens, s.desiredCompactTokensIn(ctx, c, false))
}

// windowLarger reports whether window a is larger than b, where 0 (the agent's
// own default) is the largest.
func windowLarger(a, b int) bool {
	switch {
	case b == 0:
		return false
	case a == 0:
		return true
	}
	return a > b
}

// researchTokens resolves the research choice against the model window. A
// window that is not below the model's own falls back to the agent's default
// (0), where it compacts on its own anyway.
func (s *Service) researchTokens(ctx context.Context, c *Controller, choice string) int {
	modelWindow, _ := s.modelCapacity(ctx, c.harness, c.conversation.ProjectID, c.Settings().Model)
	tokens := domain.AutoCompactTokenCount(choice, modelWindow)
	if tokens <= 0 || (modelWindow > 0 && int64(tokens) >= modelWindow) {
		return 0
	}
	return tokens
}

// ResearchStatus reports an orchestrator's research mode; ok is false for a
// session without a live orchestrator controller.
func (s *Service) ResearchStatus(ctx context.Context, id domain.SessionID) (ResearchStatus, bool) {
	c, err := s.Controller(id)
	if err != nil || c.kind != domain.KindOrchestrator {
		return ResearchStatus{}, false
	}
	return s.researchStatus(ctx, c), true
}

func (s *Service) researchStatus(ctx context.Context, c *Controller) ResearchStatus {
	choice, allowed := s.researchChoice(ctx, c)
	st := ResearchStatus{
		ResearchWindow: choice,
	}
	c.mu.Lock()
	st.Available = allowed && c.harness == domain.HarnessClaudeCode
	st.RestartPending = c.research.restartPending
	st.Window = c.compactLive
	if c.research.on {
		st.Since = c.research.since
	}
	c.mu.Unlock()
	_, st.Active = s.researchInEffect(ctx, c)
	st.Active = st.Active && !st.RestartPending
	return st
}

// SetResearchMode switches an orchestrator's research mode. Switching on while
// on changes nothing and adds no second chat line.
func (s *Service) SetResearchMode(ctx context.Context, id domain.SessionID, on bool) (ResearchStatus, error) {
	c, err := s.Controller(id)
	if err != nil {
		return ResearchStatus{}, err
	}
	if c.kind != domain.KindOrchestrator || c.harness != domain.HarnessClaudeCode {
		return ResearchStatus{}, ErrResearchUnsupported
	}
	c.research.mu.Lock()
	defer c.research.mu.Unlock()
	c.mu.Lock()
	was := c.research.on
	c.mu.Unlock()
	if on {
		if _, allowed := s.researchChoice(ctx, c); !allowed {
			return ResearchStatus{}, ErrResearchDisabled
		}
	}
	if on == was {
		return s.researchStatus(ctx, c), nil
	}
	_, effectiveBefore := s.researchInEffect(ctx, c)
	s.setResearchFlag(c, on)
	if !on {
		s.reconcileCompaction(ctx, c)
		if effectiveBefore {
			s.announceResearch(ctx, c, false, s.compactionPending(ctx, c))
		}
		return s.researchStatus(ctx, c), nil
	}
	out, live, desired, err := s.tryLiveCompaction(ctx, c)
	switch out {
	case compactAskFailed:
		// Nothing was asked for in the end: the flag goes back, so the log says
		// only what happened.
		s.setResearchFlag(c, false)
		s.log.Warn("research mode not enabled: the request to change the live compaction window failed", "session", c.sessionID, "live", live, "desired", desired, "error", err)
		return ResearchStatus{}, ErrResearchRequestFailed
	case compactKept, compactNoSetter:
		// The agent reads its window only at launch: restart it when idle.
		s.log.Info("research mode waits for an agent restart", "session", c.sessionID, "live", live, "desired", desired)
		s.beginResearchRestart(c)
		s.announceResearchPending(ctx, c)
		return s.researchStatus(ctx, c), nil
	case compactApplied:
		s.log.Info("compaction window applied live", "session", c.sessionID, "previous", live, "tokens", desired)
	}
	// A chat with a window of its own is unchanged by the switch: no line.
	if _, effectiveAfter := s.researchInEffect(ctx, c); effectiveBefore != effectiveAfter {
		s.announceResearch(ctx, c, true, false)
	}
	return s.researchStatus(ctx, c), nil
}

func (s *Service) setResearchFlag(c *Controller, on bool) {
	c.mu.Lock()
	c.research.on = on
	c.research.since = time.Time{}
	c.research.restartPending = false
	if !on {
		s.researchCarried.take(c.sessionID)
	}
	if on {
		c.research.since = time.Now()
	}
	c.mu.Unlock()
}

// ClearResearch ends an orchestrator's research mode, as an idle compaction or
// handoff does, and reports whether it was on.
func (s *Service) ClearResearch(ctx context.Context, id domain.SessionID) bool {
	c, err := s.Controller(id)
	if err != nil {
		return false
	}
	return s.clearResearch(ctx, c)
}

func (s *Service) clearResearch(ctx context.Context, c *Controller) bool {
	c.research.mu.Lock()
	defer c.research.mu.Unlock()
	c.mu.Lock()
	was := c.research.on
	c.mu.Unlock()
	if !was {
		return false
	}
	_, effective := s.researchInEffect(ctx, c)
	s.setResearchFlag(c, false)
	s.reconcileCompaction(ctx, c)
	if effective {
		s.announceResearch(ctx, c, false, s.compactionPending(ctx, c))
	}
	return true
}

// ClearAllResearch ends every running research mode, for the global toggle
// turning off.
func (s *Service) ClearAllResearch(ctx context.Context) {
	s.mu.RLock()
	var targets []*Controller
	for _, c := range s.controllers {
		if c.kind == domain.KindOrchestrator {
			targets = append(targets, c)
		}
	}
	s.mu.RUnlock()
	for _, c := range targets {
		s.clearResearch(ctx, c)
	}
}

// announceResearch writes the one chat line a switch gets. offPending says the
// agent still compacts at the research window until its next restart.
func (s *Service) announceResearch(ctx context.Context, c *Controller, on, offPending bool) {
	summary := "Research mode off: compaction window back to the coordination window"
	switch {
	case on:
		summary = fmt.Sprintf("Research mode on: compaction window raised to %s", s.researchWindowLabel(ctx, c))
	case offPending:
		summary = "Research mode off: the coordination window applies at the agent's next restart"
	}
	s.announceSummary(ctx, c, summary)
}

func (s *Service) announceResearchPending(ctx context.Context, c *Controller) {
	s.announceSummary(ctx, c, fmt.Sprintf("Research mode on: the agent restarts when idle to raise its compaction window to %s", s.researchWindowLabel(ctx, c)))
}

func (s *Service) announceSummary(ctx context.Context, c *Controller, summary string) {
	if s.announce != nil {
		s.announce(c, summary)
		return
	}
	now := c.now()
	err := c.store.UpsertActivity(ctx, c.conversation.ID, "", domain.ConversationActivity{
		ID:      c.newID(),
		Kind:    domain.ActivityKindSystem,
		Status:  domain.ActivityStatusCompleted,
		Summary: summary,
	}, now)
	if err != nil {
		s.log.Warn("could not write the research mode chat line", "session", c.sessionID, "error", err)
	}
}

func (s *Service) researchWindowLabel(ctx context.Context, c *Controller) string {
	choice, _ := s.researchChoice(ctx, c)
	if tokens := s.researchTokens(ctx, c, choice); tokens > 0 {
		return fmt.Sprintf("%dk tokens", tokens/1000)
	}
	return "the agent's own default"
}

func (c *Controller) researchActive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.research.on
}
