package acp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/persistenthost"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestACPConversationImplementsCompactor(t *testing.T) {
	agent := &fakeAgent{}
	driver := newFakeDriver(agent)

	conv := startConversation(t, driver)

	compactor, ok := conv.(ports.ChatCompactor)
	if !ok {
		t.Fatal("conversation does not implement ChatCompactor")
	}

	// Refuses when capability is not advertised
	if _, err := compactor.Compact(context.Background()); err == nil || !strings.Contains(err.Error(), "cannot compact history") {
		t.Fatalf("Compact without capability = %v, want cannot compact history", err)
	}

	// Refuses when another turn is already active
	c := conv.(*conversation)
	c.mu.Lock()
	c.capabilities[ports.ChatCapabilityCompaction] = true
	c.activeTurn = "active-turn"
	c.mu.Unlock()

	if _, err := compactor.Compact(context.Background()); err == nil {
		t.Fatal("Compact should fail when another turn is in flight")
	}
}

func TestACPCompactionExecutesPromptAndEmitsCompactedEvent(t *testing.T) {
	agent := &fakeAgent{}
	driver := newFakeDriver(agent, ports.ChatCapabilityCompaction)

	opened := startConversation(t, driver)

	_ = nextEvent(t, opened.Events()) // controller.ready

	// Report initial usage
	if err := agent.conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
		SessionId: acpsdk.SessionId(opened.ProviderConversationID()),
		Update: acpsdk.SessionUpdate{
			UsageUpdate: &acpsdk.SessionUsageUpdate{Used: 25000, Size: 200000},
		},
	}); err != nil {
		t.Fatalf("SessionUpdate initial usage: %v", err)
	}
	_ = nextEvent(t, opened.Events()) // usage event

	compactionPromptReceived := make(chan string, 1)
	agent.mu.Lock()
	agent.customPrompt = func(ctx context.Context, params acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
		promptText := ""
		for _, block := range params.Prompt {
			if block.Text != nil {
				promptText += block.Text.Text
			}
		}
		compactionPromptReceived <- promptText

		// Emit intermediate message chunk (should be suppressed by compaction turn)
		_ = agent.conn.SessionUpdate(ctx, acpsdk.SessionNotification{
			SessionId: params.SessionId,
			Update: acpsdk.SessionUpdate{
				AgentMessageChunk: &acpsdk.SessionUpdateAgentMessageChunk{
					Content: acpsdk.TextBlock("Compacted conversation history."),
				},
			},
		})

		// Emit reduced token usage
		_ = agent.conn.SessionUpdate(ctx, acpsdk.SessionNotification{
			SessionId: params.SessionId,
			Update: acpsdk.SessionUpdate{
				UsageUpdate: &acpsdk.SessionUsageUpdate{Used: 10000, Size: 200000},
			},
		})

		return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
	}
	agent.mu.Unlock()

	compactor := opened.(ports.ChatCompactor)
	result, err := compactor.Compact(context.Background())
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if result.TokensBefore != 25000 {
		t.Errorf("TokensBefore = %d, want 25000", result.TokensBefore)
	}

	select {
	case prompt := <-compactionPromptReceived:
		if prompt != "/compact" {
			t.Errorf("prompt sent = %q, want /compact", prompt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for /compact prompt")
	}

	// Drain events and verify compaction event
	var compactedEvent *ports.ChatEvent
	for {
		ev := nextEvent(t, opened.Events())
		if ev.Kind == ports.ChatEventMessageDelta {
			t.Fatalf("unexpected message delta emitted during compaction turn: %q", ev.Delta)
		}
		if ev.Kind == ports.ChatEventCompacted {
			compactedEvent = &ev
		}
		if ev.Kind == ports.ChatEventTurnCompleted {
			break
		}
	}

	if compactedEvent == nil {
		t.Fatal("ChatEventCompacted was not emitted")
	}
	if !strings.Contains(compactedEvent.Summary, "15.0k tokens") {
		t.Errorf("summary = %q, want 15.0k tokens named", compactedEvent.Summary)
	}

	var detail struct {
		TokensBefore    int64 `json:"tokensBefore"`
		TokensAfter     int64 `json:"tokensAfter"`
		TokensReclaimed int64 `json:"tokensReclaimed"`
		ContextWindow   int64 `json:"contextWindow"`
	}
	if err := json.Unmarshal(compactedEvent.Detail, &detail); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if detail.TokensBefore != 25000 || detail.TokensAfter != 10000 {
		t.Errorf("detail tokens = %d -> %d, want 25000 -> 10000", detail.TokensBefore, detail.TokensAfter)
	}
	if detail.TokensReclaimed != 15000 {
		t.Errorf("tokensReclaimed = %d, want 15000", detail.TokensReclaimed)
	}
	if detail.ContextWindow != 200000 {
		t.Errorf("contextWindow = %d, want 200000", detail.ContextWindow)
	}
}

func TestACPDriverClientCapabilitiesOmitsPrematureSessionCompaction(t *testing.T) {
	agent := &fakeAgent{}
	driver := newFakeDriver(agent)

	startConversation(t, driver)

	meta := agent.initParams.ClientCapabilities.Meta
	if meta != nil && meta["session"] != nil {
		t.Fatalf("ClientCapabilities.Meta should not advertise session.compaction before structured notifications are handled: %#v", meta["session"])
	}
}

func TestACPDriverExposesCompactionWhenCommandAdvertised(t *testing.T) {
	agent := &fakeAgent{}
	driver := newFakeDriver(agent)

	opened := startConversation(t, driver)

	if opened.Capabilities().Has(ports.ChatCapabilityCompaction) {
		t.Fatal("compaction capability should not be present before command update")
	}

	// Push available commands update containing compact
	if err := agent.conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
		SessionId: acpsdk.SessionId(opened.ProviderConversationID()),
		Update: acpsdk.SessionUpdate{
			AvailableCommandsUpdate: &acpsdk.SessionAvailableCommandsUpdate{
				AvailableCommands: []acpsdk.AvailableCommand{
					{Name: "compact", Description: "Compact history"},
				},
			},
		},
	}); err != nil {
		t.Fatalf("SessionUpdate available commands: %v", err)
	}

	// Await skills known
	lister := opened.(ports.ChatSkillLister)
	awaitSkillCount(t, lister, 1)

	if !opened.Capabilities().Has(ports.ChatCapabilityCompaction) {
		t.Fatal("compaction capability was not enabled after compact command was advertised")
	}

	// Push later commands update omitting compact: capability must be cleared
	if err := agent.conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
		SessionId: acpsdk.SessionId(opened.ProviderConversationID()),
		Update: acpsdk.SessionUpdate{
			AvailableCommandsUpdate: &acpsdk.SessionAvailableCommandsUpdate{
				AvailableCommands: []acpsdk.AvailableCommand{
					{Name: "help", Description: "Help"},
				},
			},
		},
	}); err != nil {
		t.Fatalf("SessionUpdate available commands without compact: %v", err)
	}

	for start := time.Now(); opened.Capabilities().Has(ports.ChatCapabilityCompaction); {
		if time.Since(start) > 2*time.Second {
			t.Fatal("compaction capability was not cleared after compact command was removed")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestACPEarlyCommandDuringSessionNewPreservedInStart(t *testing.T) {
	agent := &fakeAgent{
		newSessionUpdates: []acpsdk.SessionUpdate{
			{
				AvailableCommandsUpdate: &acpsdk.SessionAvailableCommandsUpdate{
					AvailableCommands: []acpsdk.AvailableCommand{
						{Name: "compact", Description: "Compact history"},
					},
				},
			},
		},
	}
	driver := newFakeDriver(agent)

	opened := startConversation(t, driver)

	if !opened.Capabilities().Has(ports.ChatCapabilityCompaction) {
		t.Fatal("compaction capability received during session/new was lost in start()")
	}
}

func TestACPCompactionEmitsBusyBeforeCompactReturns(t *testing.T) {
	agent := &fakeAgent{}
	driver := newFakeDriver(agent, ports.ChatCapabilityCompaction)

	opened := startConversation(t, driver)

	_ = nextEvent(t, opened.Events()) // controller.ready

	agent.mu.Lock()
	blockPrompt := make(chan struct{})
	agent.customPrompt = func(ctx context.Context, params acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
		<-blockPrompt
		return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
	}
	agent.mu.Unlock()

	compactor := opened.(ports.ChatCompactor)
	_, err := compactor.Compact(context.Background())
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// Immediately after Compact returns, turn-started and controller-busy must already be queued
	ev1 := nextEvent(t, opened.Events())
	if ev1.Kind != ports.ChatEventTurnStarted {
		t.Fatalf("first event = %v, want ChatEventTurnStarted", ev1.Kind)
	}
	ev2 := nextEvent(t, opened.Events())
	if ev2.Kind != ports.ChatEventControllerState || ev2.ControllerState != ports.ChatControllerBusy {
		t.Fatalf("second event = %v (%v), want ChatEventControllerState (busy)", ev2.Kind, ev2.ControllerState)
	}

	close(blockPrompt)
}

func TestACPCompactionCancelledDoesNotSettle(t *testing.T) {
	agent := &fakeAgent{}
	driver := newFakeDriver(agent, ports.ChatCapabilityCompaction)

	opened := startConversation(t, driver)

	_ = nextEvent(t, opened.Events()) // controller.ready

	agent.mu.Lock()
	agent.customPrompt = func(ctx context.Context, params acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
		return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonCancelled}, nil
	}
	agent.mu.Unlock()

	compactor := opened.(ports.ChatCompactor)
	if _, err := compactor.Compact(context.Background()); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	for {
		ev := nextEvent(t, opened.Events())
		if ev.Kind == ports.ChatEventCompacted {
			t.Fatal("ChatEventCompacted emitted on cancelled prompt")
		}
		if ev.Kind == ports.ChatEventTurnCompleted {
			if ev.TurnState != domain.TurnStateInterrupted {
				t.Fatalf("turn state = %v, want TurnStateInterrupted", ev.TurnState)
			}
			break
		}
	}

	c := opened.(*conversation)
	c.mu.Lock()
	compactingID := c.compactingTurnID
	c.mu.Unlock()
	if compactingID != "" {
		t.Fatalf("compactingTurnID = %q, want cleared after cancel", compactingID)
	}
}

func TestACPCompactionRestoredOnLiveReconnect(t *testing.T) {
	agent := &fakeAgent{}
	driver := newFakeDriver(agent, ports.ChatCapabilityCompaction)

	opened := startConversation(t, driver)

	c := opened.(*conversation)
	c.proc.reconnected = true
	gate := newGatedReader(strings.NewReader(""), false)
	c.proc.gate = gate
	c.liveState = &persistenthost.ACPState{
		ActivePrompt:     true,
		ActiveCompaction: true,
	}

	if err := c.ActivateLiveReconnect(context.Background(), "durable-compaction-turn"); err != nil {
		t.Fatalf("ActivateLiveReconnect: %v", err)
	}

	c.mu.Lock()
	active := c.activeTurn
	compacting := c.compactingTurnID
	c.mu.Unlock()

	if active != "durable-compaction-turn" {
		t.Errorf("activeTurn = %q, want durable-compaction-turn", active)
	}
	if compacting != "durable-compaction-turn" {
		t.Errorf("compactingTurnID = %q, want durable-compaction-turn", compacting)
	}
}
