package chat

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Research mode through a restart (B638). Claude Code reads its compaction
// window only at launch, so an orchestrator that asks for research mode keeps
// the flag across a restart: the flag is carried in service memory (a daemon
// restart drops back to the normal window), the agent is restarted once it is
// idle, the new launch uses the research window, and a wake message lets the
// agent continue the reading it asked for.

const (
	researchRestartPoll    = 3 * time.Second
	researchRestartTimeout = 30 * time.Minute
)

// researchWaitLimit is the longest the restart waits for an idle agent.
func (s *Service) researchWaitLimit() time.Duration {
	if s.researchWait > 0 {
		return s.researchWait
	}
	return researchRestartTimeout
}

// researchCarry holds, per orchestrator, the research flag that waits for the
// next launch. The zero value is ready to use.
type researchCarry struct {
	mu    sync.Mutex
	since map[domain.SessionID]time.Time
}

func (r *researchCarry) put(id domain.SessionID, since time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.since == nil {
		r.since = map[domain.SessionID]time.Time{}
	}
	r.since[id] = since
}

func (r *researchCarry) has(id domain.SessionID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.since[id]
	return ok
}

func (r *researchCarry) take(id domain.SessionID) (time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	since, ok := r.since[id]
	delete(r.since, id)
	return since, ok
}

// researchLaunchTokens is the window a launch of cfg's orchestrator uses when
// research mode is carried for it: ok is false when none is carried, the chat
// has a window of its own, research mode is off, or it would not raise the
// window. tokens 0 is the agent's own default. coordination is the choice the
// launch would otherwise resolve; model is the launch model.
func (s *Service) researchLaunchTokens(ctx context.Context, cfg StartConfig, own, coordination, model string) (tokens int, ok bool) {
	if cfg.Kind != domain.KindOrchestrator || own != "" || !s.researchCarried.has(cfg.SessionID) {
		return 0, false
	}
	choice, allowed := domain.ResearchCompactChoice(s.compactProjectConfig(ctx, cfg.ProjectID), s.dynamicContextEnabled(ctx))
	if !allowed {
		return 0, false
	}
	modelWindow, _ := s.modelCapacity(ctx, cfg.Harness, cfg.ProjectID, model)
	tokens = domain.AutoCompactTokenCount(choice, modelWindow)
	if tokens <= 0 || (modelWindow > 0 && int64(tokens) >= modelWindow) {
		tokens = 0
	}
	baseline := projectCompactTokens(cfg)
	if tokens == 0 {
		tokens = baseline
	}
	coordTokens := domain.AutoCompactTokenCount(coordination, modelWindow)
	if coordTokens <= 0 {
		coordTokens = baseline
	}
	return tokens, windowLarger(tokens, coordTokens)
}

// carryResearchToController gives a controller built for a launch the research
// flag the previous controller carried, when research mode is still allowed.
func (s *Service) carryResearchToController(ctx context.Context, c *Controller) {
	since, ok := s.researchCarried.take(c.sessionID)
	if !ok {
		return
	}
	if _, allowed := s.researchChoice(ctx, c); !allowed {
		return
	}
	c.mu.Lock()
	c.research.on, c.research.since = true, since
	c.mu.Unlock()
}

// beginResearchRestart records that c's research flag waits for a restart and
// starts the wait for the agent to be idle.
func (s *Service) beginResearchRestart(c *Controller) {
	c.mu.Lock()
	c.research.restartPending = true
	since := c.research.since
	c.mu.Unlock()
	s.researchCarried.put(c.sessionID, since)
	go s.restartForResearch(c)
}

// restartForResearch restarts c's agent once it is idle, then wakes it. Any
// other end of research mode (switched off, idle compaction) cancels it by
// clearing the carried flag.
func (s *Service) restartForResearch(c *Controller) {
	ctx, cancel := context.WithTimeout(context.Background(), s.researchWaitLimit())
	defer cancel()
	poll := s.researchPoll
	if poll <= 0 {
		poll = researchRestartPoll
	}
	tick := time.NewTicker(poll)
	defer tick.Stop()
	for {
		cur, err := s.Controller(c.sessionID)
		if !s.researchCarried.has(c.sessionID) {
			// Switched off, or an idle compaction ended it; unless another path
			// relaunched the agent, which took the flag: it still gets the message.
			if err == nil && cur != c && cur.researchActive() {
				s.sendResearchMessage(ctx, c, s.researchWakeText(ctx, c))
			}
			return
		}
		switch {
		case err != nil || cur != c:
			// The agent is gone or replaced. Only a hibernated chat is woken by
			// the message (its launch takes the carried flag); a chat the owner
			// stopped stays stopped and the flag is dropped.
			if s.researchHibernated(ctx, c.sessionID) {
				s.sendResearchMessage(ctx, c, s.researchWakeText(ctx, c))
			} else {
				s.researchCarried.take(c.sessionID)
			}
			return
		case c.busy():
		default:
			if err := s.restartResearchAgent(ctx, c.sessionID); err == nil {
				s.sendResearchMessage(ctx, c, s.researchWakeText(ctx, c))
				return
			} else if !errors.Is(err, ErrRestartBusy) {
				s.failResearchRestart(ctx, c, err)
				return
			}
		}
		select {
		case <-ctx.Done():
			s.failResearchRestart(ctx, c, errors.New("the agent stayed busy"))
			return
		case <-tick.C:
		}
	}
}

// researchHibernated reports the chat is hibernated (not stopped or ended), so
// a message wakes it.
func (s *Service) researchHibernated(ctx context.Context, id domain.SessionID) bool {
	if s.researchHibernatedFn != nil {
		return s.researchHibernatedFn(ctx, id)
	}
	rec, err := s.requireChatSession(ctx, id)
	return err == nil && rec.HibernatedAt != nil && !rec.IsTerminated
}

func (s *Service) restartResearchAgent(ctx context.Context, id domain.SessionID) error {
	if s.researchRestartFn != nil {
		return s.researchRestartFn(ctx, id)
	}
	return s.RestartChatAgent(ctx, id)
}

func (s *Service) sendResearchMessage(ctx context.Context, c *Controller, text string) {
	var err error
	if s.researchSendFn != nil {
		err = s.researchSendFn(ctx, c.sessionID, text)
	} else {
		_, err = s.Send(ctx, c.sessionID, ports.ChatUserMessage{Text: text})
	}
	if err != nil {
		s.log.Warn("could not send the research mode wake message", "session", c.sessionID, "error", err)
	}
}

func (s *Service) researchWakeText(ctx context.Context, c *Controller) string {
	return fmt.Sprintf("Research mode is on: your compaction window is now %s and your agent was restarted to apply it; the conversation is kept. "+
		"Continue the reading task you started. Switch research mode off when the analysis is done.", s.researchWindowLabel(ctx, c))
}

// failResearchRestart ends the pending research mode after a restart that did
// not happen, so the session is left as it was and says so.
func (s *Service) failResearchRestart(_ context.Context, c *Controller, cause error) {
	// A fresh context: the wait's own may have expired, and the owner and the
	// agent must still be told.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if !s.researchCarried.has(c.sessionID) {
		return // already switched off
	}
	c.research.mu.Lock()
	s.setResearchFlag(c, false)
	c.research.mu.Unlock()
	s.log.Warn("research mode not applied: the agent restart failed", "session", c.sessionID, "error", cause)
	s.announceSummary(ctx, c, "Research mode not enabled: the agent could not be restarted for the larger compaction window")
	s.sendResearchMessage(ctx, c, "Research mode could not be enabled: the restart for the larger compaction window failed. "+
		"You stay in coordination mode; delegate the reading to workers instead.")
}
