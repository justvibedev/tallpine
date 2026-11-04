package sessionmanager

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type usageAgent struct {
	fakeAgent
	gotID string
}

func (a *usageAgent) TerminalModelUsage(_ context.Context, id string) ([]domain.ModelUsage, bool, error) {
	a.gotID = id
	return []domain.ModelUsage{{Model: "m", Credits: 2}}, true, nil
}

type usageAgents struct{ agent *usageAgent }

func (u usageAgents) Agent(domain.AgentHarness) (ports.Agent, bool) { return u.agent, true }

func TestTerminalModelUsageUsesNativeSessionID(t *testing.T) {
	m, st, _, _ := newManager()
	agent := &usageAgent{}
	m.agents = usageAgents{agent}
	rec, err := st.CreateSession(context.Background(), domain.SessionRecord{
		ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessKiro, Mode: domain.SessionModeTUI,
		Metadata: domain.SessionMetadata{AgentSessionID: "sess_abc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := m.TerminalModelUsage(context.Background(), rec.ID)
	if err != nil || !ok || len(got) != 1 || agent.gotID != "sess_abc" {
		t.Fatalf("got=%v ok=%v err=%v id=%q", got, ok, err, agent.gotID)
	}
	chat := rec
	chat.Mode = domain.SessionModeChat
	chat.ID = ""
	chatRec, _ := st.CreateSession(context.Background(), chat)
	if _, ok, _ := m.TerminalModelUsage(context.Background(), chatRec.ID); ok {
		t.Fatal("chat-mode session must not read a terminal transcript")
	}
}
