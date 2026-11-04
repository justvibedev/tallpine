package acp

import (
	"context"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// newFakeDriver builds a Claude-harness Driver whose provider process is the
// given fake agent. Streaming is always advertised; caps adds more.
func newFakeDriver(agent *fakeAgent, caps ...ports.ChatCapability) *Driver {
	return newDriverWith(fakeConfig(caps...), agent)
}

// fakeConfig is the minimal Claude-harness Config the fake-driver tests share:
// streaming plus caps, a passing probe and a "fake" launch. Tests set the
// hooks they exercise on the returned value.
func fakeConfig(caps ...ports.ChatCapability) Config {
	capabilities := ports.ChatCapabilities{ports.ChatCapabilityStreaming: true}
	for _, c := range caps {
		capabilities[c] = true
	}
	return Config{
		Harness:      domain.HarnessClaudeCode,
		Capabilities: capabilities,
		Probe:        func(context.Context) error { return nil },
		Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
	}
}

// newDriverWith builds a Driver from cfg whose provider process is the given
// fake agent.
func newDriverWith(cfg Config, agent *fakeAgent) *Driver {
	driver := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))
	return driver
}

// modelSessionOption maps a turn's model to the "model" session option.
func modelSessionOption(settings ports.ChatTurnSettings) []SessionOption {
	if settings.Model == "" {
		return nil
	}
	return []SessionOption{{ID: "model", Value: settings.Model}}
}

// startConversation opens a conversation in a fresh workspace and closes it
// when the test ends.
func startConversation(t *testing.T, driver *Driver) ports.ChatConversation {
	t.Helper()
	conv, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = conv.Close() })
	return conv
}

// startHelloTurn opens a conversation, drains its first event, sends "hello" and
// starts the deferred turn, so the test can read the turn's events.
func startHelloTurn(t *testing.T, driver *Driver) ports.ChatConversation {
	t.Helper()
	opened := startConversation(t, driver)
	_ = nextEvent(t, opened.Events())
	ref, err := opened.SendTurn(context.Background(), ports.ChatUserMessage{Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if err := opened.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatal(err)
	}
	return opened
}

// pipeProcess wraps the two in-memory pipes of a fake agent as a process whose
// stop closes all four ends once.
func pipeProcess(clientToAgentR *io.PipeReader, clientToAgentW *io.PipeWriter, agentToClientR *io.PipeReader, agentToClientW *io.PipeWriter) *process {
	var once sync.Once
	return &process{
		stdin: clientToAgentW, stdout: agentToClientR,
		stop: func() error {
			once.Do(func() {
				_ = clientToAgentW.Close()
				_ = clientToAgentR.Close()
				_ = agentToClientW.Close()
				_ = agentToClientR.Close()
			})
			return nil
		},
	}
}

// activateTurn marks turn-1 as the conversation's active turn, so tests can feed
// session updates and steering without running a prompt.
func activateTurn(opened ports.ChatConversation) *conversation {
	conv := opened.(*conversation)
	conv.mu.Lock()
	conv.activeTurn = "turn-1"
	conv.mu.Unlock()
	return conv
}

// startActiveTurn opens a conversation, drains its first event and activates
// turn-1.
func startActiveTurn(t *testing.T, driver *Driver) ports.ChatConversation {
	t.Helper()
	opened := startConversation(t, driver)
	_ = nextEvent(t, opened.Events())
	activateTurn(opened)
	return opened
}

// sendSessionFailure delivers a JetBrains "sessionFailure" session_info_update.
func sendSessionFailure(t *testing.T, agent *fakeAgent, sessionID string, failure map[string]any) {
	t.Helper()
	if err := agent.conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
		SessionId: acpsdk.SessionId(sessionID),
		Update: acpsdk.SessionUpdate{SessionInfoUpdate: &acpsdk.SessionSessionInfoUpdate{
			SessionUpdate: "session_info_update",
			Meta: map[string]any{
				"jetbrains": map[string]any{"air": map[string]any{"version": float64(1), "sessionFailure": failure}},
			},
		}},
	}); err != nil {
		t.Fatalf("session failure update: %v", err)
	}
}

// newPipeConversation builds a conversation over an in-memory pipe whose far
// end is closed when the test ends.
func newPipeConversation(t *testing.T) (*conversation, net.Conn) {
	t.Helper()
	daemon, host := net.Pipe()
	t.Cleanup(func() { _ = host.Close() })
	conv := newConversation(&process{
		stdin: daemon, stdout: daemon, stop: daemon.Close,
		terminate: func() error { return nil },
	}, slog.New(slog.DiscardHandler), "", nil, nil)
	return conv, host
}

// startPreambleConversation opens a conversation with the standing-instructions
// preamble config (cfg) and drains controller.ready.
func startPreambleConversation(t *testing.T, cfg Config, agent *fakeAgent) ports.ChatConversation {
	t.Helper()
	conv, err := newDriverWith(cfg, agent).Start(context.Background(), ports.ChatStartConfig{
		WorkspacePath: t.TempDir(), SystemPrompt: standingInstructions,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = conv.Close() })
	_ = nextEvent(t, conv.Events()) // controller.ready
	return conv
}

// forkInBackground runs ForkIntoHost from owned:parent in a goroutine in a fresh
// workspace and delivers its error on the returned channel.
func forkInBackground(ctx context.Context, t *testing.T, driver *Driver, start ports.ChatStartConfig) chan error {
	start.WorkspacePath = t.TempDir()
	done := make(chan error, 1)
	go func() {
		_, err := driver.ForkIntoHost(ctx, "owned:parent", "", start)
		done <- err
	}()
	return done
}
