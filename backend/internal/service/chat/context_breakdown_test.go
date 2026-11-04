package chat

import (
	"context"
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type breakdownConv struct {
	ports.ChatConversation
	breakdown domain.ContextBreakdown
	supported bool
	err       error
	calls     int
}

func (c *breakdownConv) ContextBreakdown(context.Context) (domain.ContextBreakdown, bool, error) {
	c.calls++
	return c.breakdown, c.supported, c.err
}

func breakdownService(sessionID domain.SessionID, controller *Controller) *Service {
	s := compactionService(new(string))
	s.controllers = map[domain.SessionID]*Controller{}
	if controller != nil {
		s.controllers[sessionID] = controller
	}
	return s
}

func TestContextBreakdownAsksTheRunningAgentOnRequest(t *testing.T) {
	want := domain.ContextBreakdown{TotalTokens: 5, MaxTokens: 100, Categories: []domain.ContextCategory{{Name: "Messages", Tokens: 5}}}
	conv := &breakdownConv{breakdown: want, supported: true}
	s := breakdownService("s1", &Controller{harness: domain.HarnessClaudeCode, conv: conv, state: ports.ChatControllerReady})
	got, available, err := s.runningContextBreakdown(context.Background(), "s1")
	if err != nil || !available || got.TotalTokens != 5 || conv.calls != 1 {
		t.Fatalf("got %+v available %v err %v calls %d", got, available, err, conv.calls)
	}
}

func TestContextBreakdownIsUnavailableNotAnErrorWhenTheAgentCannotGiveOne(t *testing.T) {
	for name, tc := range map[string]struct {
		controller *Controller
	}{
		"no running controller":   {nil},
		"agent without breakdown": {&Controller{harness: domain.HarnessCodex, conv: &plainConv{}, state: ports.ChatControllerReady}},
		"unpatched bridge":        {&Controller{harness: domain.HarnessClaudeCode, conv: &breakdownConv{supported: false}, state: ports.ChatControllerReady}},
		"request fails":           {&Controller{harness: domain.HarnessClaudeCode, conv: &breakdownConv{err: errors.New("boom")}, state: ports.ChatControllerReady}},
		"controller stopped":      {&Controller{harness: domain.HarnessClaudeCode, conv: &breakdownConv{supported: true}, state: ports.ChatControllerStopped}},
	} {
		s := breakdownService("s1", tc.controller)
		_, available, err := s.runningContextBreakdown(context.Background(), "s1")
		if err != nil || available {
			t.Errorf("%s: available = %v, err = %v; want unavailable and no error", name, available, err)
		}
	}
}
