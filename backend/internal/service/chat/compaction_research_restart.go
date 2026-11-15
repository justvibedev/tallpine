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
// the flag across a restart: the flag is carried in service memory and on the
// session (B736), so a daemon restart keeps it; the agent is restarted once it is
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

// researchCarry holds, per orchestrator, the research mode that waits for the
// next launch. The zero value is ready to use.
type researchCarry struct {
	mu      sync.Mutex
	carried map[domain.SessionID]carriedResearch
}

// carriedResearch is a research mode carried to the next launch; auto marks a
// thrash step-up.
type carriedResearch struct {
	domain.SessionResearch
	auto bool
}

func (r *researchCarry) put(id domain.SessionID, research carriedResearch) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.carried == nil {
		r.carried = map[domain.SessionID]carriedResearch{}
	}
	r.carried[id] = research
}

func (r *researchCarry) get(id domain.SessionID) (carriedResearch, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	research, ok := r.carried[id]
	return research, ok
}

func (r *researchCarry) has(id domain.SessionID) bool {
	_, ok := r.get(id)
	return ok
}

func (r *researchCarry) clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.carried)
}

func (r *researchCarry) take(id domain.SessionID) (carriedResearch, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	research, ok := r.carried[id]
	delete(r.carried, id)
	return research, ok
}

// researchLaunchTokens is the window a launch of cfg's orchestrator uses when
// research mode is carried for it: ok is false when none is carried, the chat
// has a window of its own, research mode is off, or it would not raise the
// window. tokens 0 is the agent's own default. coordination is the choice the
// launch would otherwise resolve; model is the launch model.
func (s *Service) researchLaunchTokens(ctx context.Context, cfg StartConfig, own, coordination, model string) (tokens int, ok bool) {
	if cfg.Kind != domain.KindOrchestrator {
		return 0, false
	}
	s.restoreResearch(ctx, cfg.SessionID, cfg.Harness)
	carried, ok := s.researchCarried.get(cfg.SessionID)
	if own != "" || !ok {
		return 0, false
	}
	choice, allowed := domain.ResearchCompactChoice(s.compactProjectConfig(ctx, cfg.ProjectID), s.dynamicContextEnabled(ctx))
	if !allowed {
		return 0, false
	}
	if carried.Window != "" {
		choice = carried.Window
	}
	modelWindow, _ := s.modelCapacity(ctx, cfg.Harness, cfg.ProjectID, model)
	tokens = domain.ResolveAutoCompactWindow(choice, modelWindow, cfg.AutoCompactBaseline)
	baseline := projectCompactTokens(cfg)
	if tokens == 0 {
		tokens = baseline
	}
	coordTokens := domain.ResolveAutoCompactWindow(coordination, modelWindow, cfg.AutoCompactBaseline)
	if coordTokens <= 0 {
		coordTokens = baseline
	}
	return tokens, windowLarger(tokens, coordTokens)
}

// carryResearchToController gives a controller built for a launch the research
// flag the previous controller carried, when research mode is still allowed.
func (s *Service) carryResearchToController(ctx context.Context, c *Controller) {
	if c.kind != domain.KindOrchestrator {
		return
	}
	s.restoreResearch(ctx, c.sessionID, c.harness)
	carried, ok := s.researchCarried.take(c.sessionID)
	if !ok {
		return
	}
	c.mu.Lock()
	c.research.on, c.research.since, c.research.lastTurn = true, carried.Since, carried.LastTurnAt
	c.research.window, c.research.auto = carried.Window, carried.auto
	c.mu.Unlock()
	if _, allowed := s.researchChoice(ctx, c); !allowed {
		s.setResearchFlag(ctx, c, nil, false)
	}
}

// beginResearchRestart records that c's research flag waits for a restart and
// starts the wait for the agent to be idle. running says a wait already runs
// (the window changed while it waited): it takes the new carried state, so no
// second wait starts.
func (s *Service) beginResearchRestart(c *Controller, running bool) {
	c.mu.Lock()
	c.research.restartPending = true
	carried := carriedResearch{SessionResearch: domain.SessionResearch{Window: c.research.window, Since: c.research.since, LastTurnAt: c.research.lastTurn}, auto: c.research.auto}
	c.mu.Unlock()
	s.researchCarried.put(c.sessionID, carried)
	if !running {
		go s.restartForResearch(c)
	}
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
				s.persistResearch(ctx, c.sessionID, nil)
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
	c.mu.Lock()
	auto := c.research.auto
	c.mu.Unlock()
	if auto {
		return fmt.Sprintf("Your last turn stopped because auto-compaction was thrashing, so your compaction window was raised to %s and your agent was restarted to apply it; the conversation is kept. "+
			"Continue the task you were working on. The larger window ends like research mode: when you are idle-compacted, after an hour without a turn, or with `research off`.", s.researchWindowLabel(ctx, c))
	}
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
	s.setResearchFlag(ctx, c, nil, false)
	c.research.mu.Unlock()
	s.log.Warn("research mode not applied: the agent restart failed", "session", c.sessionID, "error", cause)
	s.announceSummary(ctx, c, "Research mode not enabled: the agent could not be restarted for the larger compaction window")
	s.sendResearchMessage(ctx, c, "Research mode could not be enabled: the restart for the larger compaction window failed. "+
		"You stay in coordination mode; delegate the reading to workers instead.")
}
