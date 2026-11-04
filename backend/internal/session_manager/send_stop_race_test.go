package sessionmanager

import (
	"context"
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// stopDuringRelayLauncher marks the session exited while the relay is running,
// then fails it as a stopped conversation does.
type stopDuringRelayLauncher struct {
	*deadHostSendLauncher
	stop func()
}

func (l *stopDuringRelayLauncher) RelaySessionChatTurn(context.Context, domain.SessionID, string, string, ports.MessageDeliveryOptions) (string, error) {
	l.stop()
	return "", errors.New("ACP conversation already has a turn in flight")
}

// B488: a send that raced the stop (the record was live when the send began)
// still answers AGENT_STOPPED, not an internal error.
func TestSendRacingAgentStopIsRefusedAsStopped(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newLifecycleManager()
	rec := deadHostChat("mer-race")
	st.sessions[rec.ID] = rec
	_ = st.SetSessionAgentExitReason(context.Background(), rec.ID, domain.AgentExitUser)
	m.chat = &stopDuringRelayLauncher{
		deadHostSendLauncher: &deadHostSendLauncher{deadHostLauncher: newDeadHostLauncher()},
		stop: func() {
			r := st.sessions[rec.ID]
			r.Activity.State = domain.ActivityExited
			st.sessions[rec.ID] = r
		},
	}

	_, err := m.SendWithReceipt(context.Background(), rec.ID, "hello", nil, ports.MessageDeliveryOptions{})
	if !errors.Is(err, ErrAgentStoppedByUser) {
		t.Fatalf("send racing a stop = %v, want ErrAgentStoppedByUser", err)
	}
}

// ExitAgent closes the controller (failing the relay) before it records the
// exit, so the record is still live when the relay fails; the exit operation in
// progress is what marks the agent stopped.
func TestSendDuringExitInProgressIsRefusedAsStopped(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newLifecycleManager()
	rec := deadHostChat("mer-exiting")
	st.sessions[rec.ID] = rec
	m.chat = &stopDuringRelayLauncher{
		deadHostSendLauncher: &deadHostSendLauncher{deadHostLauncher: newDeadHostLauncher()},
		stop: func() {
			if err := m.beginAgentOperation(context.Background(), rec.ID, agentOperationExit); err != nil {
				t.Errorf("begin exit: %v", err)
			}
		},
	}

	_, err := m.SendWithReceipt(context.Background(), rec.ID, "hello", nil, ports.MessageDeliveryOptions{})
	if !errors.Is(err, ErrAgentStoppedByUser) {
		t.Fatalf("send during exit = %v, want ErrAgentStoppedByUser", err)
	}
}
