package acp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// A binding's SessionUsage totals reach the usage event once a prompt completes
// without usage of its own, and are skipped when the response carries usage.
func TestSessionUsageHookEmitsUsageAfterCompletedPrompt(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response *acpsdk.Usage
		want     bool
	}{
		{"no response usage", nil, true},
		{"empty response usage", &acpsdk.Usage{}, true},
		{"response usage wins", &acpsdk.Usage{InputTokens: 1, TotalTokens: 1}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cost := 0.25
			var gotSession string
			agent := &fakeAgent{promptResponse: &acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn, Usage: tc.response}}
			driver := newSessionUsageDriver(agent, func(id string) *ports.ChatUsage {
				gotSession = id
				return &ports.ChatUsage{InputTokens: 7, CachedTokens: 5, OutputTokens: 3, TotalTokens: 15,
					TotalsKnown: true, Cost: &cost, Currency: "USD"}
			})
			opened := startHelloTurn(t, driver)
			var usage *ports.ChatUsage
			for {
				event := nextEvent(t, opened.Events())
				if event.Kind == ports.ChatEventUsage {
					usage = event.Usage
				}
				if event.Kind == ports.ChatEventTurnCompleted {
					break
				}
			}
			if !tc.want {
				if gotSession != "" || usage == nil || usage.InputTokens != 1 {
					t.Fatalf("hook ran or response usage lost: session=%q usage=%+v", gotSession, usage)
				}
				return
			}
			if gotSession == "" || usage == nil || usage.InputTokens != 7 || usage.CachedTokens != 5 ||
				usage.OutputTokens != 3 || usage.Cost == nil || *usage.Cost != 0.25 {
				t.Fatalf("session=%q usage=%+v", gotSession, usage)
			}
		})
	}
}

// A turn that fails or is cancelled was still billed, so the provider's
// accounting is read for it too.
func TestSessionUsageHookRunsForFailedTurn(t *testing.T) {
	calls := 0
	agent := &fakeAgent{promptErr: errors.New("upstream exploded")}
	driver := newSessionUsageDriver(agent, func(string) *ports.ChatUsage {
		calls++
		return &ports.ChatUsage{InputTokens: 9, TotalTokens: 9, TotalsKnown: true}
	})
	opened := startHelloTurn(t, driver)
	var usage *ports.ChatUsage
	for {
		event := nextEvent(t, opened.Events())
		if event.Kind == ports.ChatEventUsage {
			usage = event.Usage
		}
		if event.Kind == ports.ChatEventTurnCompleted || event.Kind == ports.ChatEventError {
			break
		}
	}
	if calls != 1 || usage == nil || usage.InputTokens != 9 {
		t.Fatalf("calls=%d usage=%+v", calls, usage)
	}
}

// A provider that flushes its log just after the prompt response returns is
// read again until the totals appear.
func TestSessionUsageHookRetriesUntilLogAppears(t *testing.T) {
	var calls atomic.Int32
	agent := &fakeAgent{promptResponse: &acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}}
	driver := newSessionUsageDriver(agent, func(string) *ports.ChatUsage {
		if calls.Add(1) < 3 {
			return nil
		}
		return &ports.ChatUsage{InputTokens: 7, OutputTokens: 3, TotalTokens: 10, TotalsKnown: true}
	})
	opened := startHelloTurn(t, driver)
	var usage *ports.ChatUsage
	for {
		event := nextEvent(t, opened.Events())
		if event.Kind == ports.ChatEventUsage {
			usage = event.Usage
		}
		if usage != nil {
			break
		}
	}
	if usage.InputTokens != 7 {
		t.Fatalf("usage after retries = %+v (calls=%d)", usage, calls.Load())
	}
}

// The provider total is cumulative: a later turn that re-reads the previous
// total emits nothing until the total advances, and then emits exactly once.
func TestSessionUsageHookEmitsOnlyWhenTotalAdvances(t *testing.T) {
	var calls atomic.Int32
	agent := &fakeAgent{promptResponse: &acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}}
	driver := newSessionUsageDriver(agent, func(string) *ports.ChatUsage {
		if calls.Add(1) >= 3 {
			return &ports.ChatUsage{TotalTokens: 25, TotalsKnown: true}
		}
		return &ports.ChatUsage{TotalTokens: 10, TotalsKnown: true}
	})
	opened, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	_ = nextEvent(t, opened.Events())
	totals := map[int64]int{}
	for turn := 0; turn < 2; turn++ {
		ref, err := opened.SendTurn(context.Background(), ports.ChatUserMessage{Text: "hello"})
		if err != nil {
			t.Fatal(err)
		}
		if err := opened.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
			t.Fatal(err)
		}
		for done := false; !done; {
			event := nextEvent(t, opened.Events())
			if event.Kind == ports.ChatEventUsage {
				totals[event.Usage.TotalTokens]++
			}
			done = event.Kind == ports.ChatEventTurnCompleted
		}
	}
	for totals[25] == 0 {
		if event := nextEvent(t, opened.Events()); event.Kind == ports.ChatEventUsage {
			totals[event.Usage.TotalTokens]++
		}
	}
	if totals[10] != 1 || totals[25] != 1 {
		t.Fatalf("usage events by total = %v, want one each of 10 and 25", totals)
	}
}

// newSessionUsageDriver builds a usage-capable driver over agent whose
// SessionUsage hook is hook.
func newSessionUsageDriver(agent *fakeAgent, hook func(string) *ports.ChatUsage) *Driver {
	driver := New(Config{
		Harness:      domain.HarnessDeepSeek,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityUsage: true},
		Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		SessionUsage: hook,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))
	return driver
}
