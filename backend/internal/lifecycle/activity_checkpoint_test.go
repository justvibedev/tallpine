package lifecycle

import (
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestNormalizeActivitySignal(t *testing.T) {
	t.Run("subagent stop without id is dropped", func(t *testing.T) {
		if _, ok := normalizeActivitySignal(ports.ActivitySignal{Event: "subagent-stop", Valid: true}); ok {
			t.Fatal("expected drop")
		}
	})
	t.Run("headless entrypoint without controller is dropped", func(t *testing.T) {
		if _, ok := normalizeActivitySignal(ports.ActivitySignal{Event: "stop", Valid: true, NativeEntrypoint: "sdk-cli"}); ok {
			t.Fatal("expected drop")
		}
		if _, ok := normalizeActivitySignal(ports.ActivitySignal{Event: "stop", Valid: true, NativeEntrypoint: "sdk-cli", ControllerGeneration: "g"}); !ok {
			t.Fatal("controller-owned signal must be kept")
		}
	})
	t.Run("subagent signal loses root identity", func(t *testing.T) {
		got, ok := normalizeActivitySignal(ports.ActivitySignal{
			Event: "stop", Valid: true, SubagentID: " a ", AgentSessionID: "n", TranscriptPath: "/t",
			LatestUserPrompt: "p", LatestAssistantUpdate: "u", ProviderTurnID: "t",
		})
		if !ok || got.SubagentID != "a" || got.AgentSessionID != "" || got.TranscriptPath != "" ||
			got.LatestAssistantUpdate != "" || got.ProviderTurnID != "" {
			t.Fatalf("got %+v ok=%v", got, ok)
		}
	})
	t.Run("provider text is kept only for its event", func(t *testing.T) {
		got, _ := normalizeActivitySignal(ports.ActivitySignal{Event: "user-prompt-submit", Valid: true, LatestUserPrompt: "p", LatestAssistantUpdate: "a", ProviderTurnID: "t"})
		if got.LatestUserPrompt != "p" || got.LatestAssistantUpdate != "" || got.ProviderTurnID != "t" {
			t.Fatalf("prompt: %+v", got)
		}
		got, _ = normalizeActivitySignal(ports.ActivitySignal{Event: "stop", Valid: true, LatestUserPrompt: "p", LatestAssistantUpdate: "a"})
		if got.LatestUserPrompt != "" || got.LatestAssistantUpdate != "a" {
			t.Fatalf("stop: %+v", got)
		}
		got, _ = normalizeActivitySignal(ports.ActivitySignal{Event: "pre-tool-use", Valid: true, LatestUserPrompt: "p", LatestAssistantUpdate: "a", ProviderTurnID: "t"})
		if got.LatestUserPrompt != "" || got.LatestAssistantUpdate != "" || got.ProviderTurnID != "" {
			t.Fatalf("other: %+v", got)
		}
	})
	t.Run("empty invalid signal is dropped", func(t *testing.T) {
		if _, ok := normalizeActivitySignal(ports.ActivitySignal{Event: "stop"}); ok {
			t.Fatal("expected drop")
		}
	})
	t.Run("unknown checkpoint origin is normalised", func(t *testing.T) {
		got, _ := normalizeActivitySignal(ports.ActivitySignal{Event: "stop", Valid: true})
		if got.ConversationCheckpointOrigin != domain.ConversationCheckpointOriginUnknown {
			t.Fatalf("origin %v", got.ConversationCheckpointOrigin)
		}
	})
}

var checkpointNow = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func ownedCheckpoint() checkpointOwner {
	return checkpointOwner{harness: domain.HarnessClaudeCode, generation: "launch-1", nativeID: "native-1"}
}

func TestApplyPromptSubmitCheckpoint(t *testing.T) {
	t.Run("owned prompt opens a prompt checkpoint", func(t *testing.T) {
		cp := domain.SessionMetadata{LatestAssistantUpdate: "old", ConversationCheckpointUnsettled: true}
		s := ports.ActivitySignal{Event: "user-prompt-submit", LatestUserPrompt: "do it"}
		if !applyPromptSubmitCheckpoint(&cp, s, ownedCheckpoint(), checkpointNow) {
			t.Fatal("prompt not accepted")
		}
		if cp.LatestUserPrompt != "do it" || cp.LatestAssistantUpdate != "" || cp.ConversationCheckpointUnsettled ||
			cp.ConversationCheckpointState != domain.ConversationCheckpointPrompt ||
			cp.ConversationCheckpointGeneration != "launch-1" || cp.ConversationCheckpointNativeID != "native-1" ||
			!cp.LatestUserPromptAt.Equal(checkpointNow) {
			t.Fatalf("checkpoint %+v", cp)
		}
	})
	t.Run("codex records the provider turn id", func(t *testing.T) {
		o := ownedCheckpoint()
		o.harness = domain.HarnessCodex
		cp := domain.SessionMetadata{}
		applyPromptSubmitCheckpoint(&cp, ports.ActivitySignal{Event: "user-prompt-submit", LatestUserPrompt: "p", ProviderTurnID: "turn-9"}, o, checkpointNow)
		if cp.ConversationCheckpointTurnID != "turn-9" {
			t.Fatalf("turn id %q", cp.ConversationCheckpointTurnID)
		}
	})
	t.Run("unowned prompt is legacy", func(t *testing.T) {
		cp := domain.SessionMetadata{}
		applyPromptSubmitCheckpoint(&cp, ports.ActivitySignal{Event: "user-prompt-submit", LatestUserPrompt: "p"}, checkpointOwner{harness: domain.HarnessClaudeCode}, checkpointNow)
		if cp.ConversationCheckpointState != domain.ConversationCheckpointLegacy {
			t.Fatalf("state %v", cp.ConversationCheckpointState)
		}
	})
	t.Run("coordination prompt keeps human facts", func(t *testing.T) {
		cp := domain.SessionMetadata{LatestUserPrompt: "human"}
		s := ports.ActivitySignal{Event: "user-prompt-submit", LatestUserPrompt: "coord", ConversationCheckpointOrigin: domain.ConversationCheckpointOriginCoordination, CoordinationID: "c1"}
		if !applyPromptSubmitCheckpoint(&cp, s, ownedCheckpoint(), checkpointNow) {
			t.Fatal("not accepted")
		}
		if cp.LatestUserPrompt != "human" || cp.ConversationCheckpointState != domain.ConversationCheckpointCoordination || cp.ConversationCheckpointTurnID != "c1" {
			t.Fatalf("checkpoint %+v", cp)
		}
	})
	t.Run("delayed duplicate prompt is not accepted", func(t *testing.T) {
		cp := domain.SessionMetadata{
			LatestUserPrompt: "new", LatestUserPromptAt: checkpointNow, LatestAssistantUpdate: "answer",
			ConversationCheckpointState: domain.ConversationCheckpointComplete, ConversationCheckpointGeneration: "launch-1", ConversationCheckpointNativeID: "native-1",
		}
		s := ports.ActivitySignal{Event: "user-prompt-submit", LatestUserPrompt: "older", Timestamp: checkpointNow.Add(-time.Minute)}
		if applyPromptSubmitCheckpoint(&cp, s, ownedCheckpoint(), checkpointNow) {
			t.Fatal("stale prompt accepted")
		}
		if cp.LatestUserPrompt != "new" || cp.LatestAssistantUpdate != "answer" || !cp.ConversationCheckpointUnsettled {
			t.Fatalf("checkpoint %+v", cp)
		}
	})
}

func TestApplyPromptSubmitCheckpointOrdering(t *testing.T) {
	owned := func() domain.SessionMetadata {
		return domain.SessionMetadata{
			LatestUserPrompt: "first", LatestUserPromptAt: checkpointNow, LatestAssistantUpdate: "answer",
			ConversationCheckpointState: domain.ConversationCheckpointComplete, ConversationCheckpointGeneration: "launch-1", ConversationCheckpointNativeID: "native-1",
		}
	}
	t.Run("equal time with different text is a new boundary", func(t *testing.T) {
		cp := owned()
		s := ports.ActivitySignal{Event: "user-prompt-submit", LatestUserPrompt: "second", Timestamp: checkpointNow}
		if !applyPromptSubmitCheckpoint(&cp, s, ownedCheckpoint(), checkpointNow.Add(time.Hour)) {
			t.Fatal("not accepted")
		}
		if cp.LatestUserPrompt != "second" || cp.LatestAssistantUpdate != "" || cp.ConversationCheckpointState != domain.ConversationCheckpointPrompt {
			t.Fatalf("checkpoint %+v", cp)
		}
	})
	t.Run("reset accepts an older prompt", func(t *testing.T) {
		cp := owned()
		o := ownedCheckpoint()
		o.reset = true
		s := ports.ActivitySignal{Event: "user-prompt-submit", LatestUserPrompt: "older", Timestamp: checkpointNow.Add(-time.Minute)}
		if !applyPromptSubmitCheckpoint(&cp, s, o, checkpointNow) {
			t.Fatal("not accepted")
		}
		if cp.LatestUserPrompt != "older" || cp.LatestAssistantUpdate != "" {
			t.Fatalf("checkpoint %+v", cp)
		}
		// high-water time is preserved
		if !cp.LatestUserPromptAt.Equal(checkpointNow) {
			t.Fatalf("prompt time %v", cp.LatestUserPromptAt)
		}
	})
}

func TestApplyStopCheckpoint(t *testing.T) {
	prompted := func() domain.SessionMetadata {
		return domain.SessionMetadata{
			LatestUserPrompt: "p", LatestUserPromptAt: checkpointNow.Add(-time.Minute),
			ConversationCheckpointState: domain.ConversationCheckpointPrompt, ConversationCheckpointGeneration: "launch-1", ConversationCheckpointNativeID: "native-1",
		}
	}
	t.Run("matching prompt completes", func(t *testing.T) {
		cp := prompted()
		s := ports.ActivitySignal{Event: "stop", LatestAssistantUpdate: "answer"}
		applyStopCheckpoint(&cp, &s, ownedCheckpoint(), checkpointNow)
		if cp.ConversationCheckpointState != domain.ConversationCheckpointComplete || cp.LatestAssistantUpdate != "answer" || !cp.LatestAssistantUpdateAt.Equal(checkpointNow) {
			t.Fatalf("checkpoint %+v", cp)
		}
	})
	t.Run("claude native boundary is unsettled", func(t *testing.T) {
		cp := prompted()
		o := ownedCheckpoint()
		o.claudeBoundary = true
		s := ports.ActivitySignal{Event: "stop", LatestAssistantUpdate: "answer"}
		applyStopCheckpoint(&cp, &s, o, checkpointNow)
		if !cp.ConversationCheckpointUnsettled || s.LatestAssistantUpdate != "" || cp.ConversationCheckpointState != domain.ConversationCheckpointPrompt {
			t.Fatalf("checkpoint %+v signal %+v", cp, s)
		}
	})
	t.Run("coordination stop never promotes the answer", func(t *testing.T) {
		cp := prompted()
		s := ports.ActivitySignal{Event: "stop", LatestAssistantUpdate: "answer", ConversationCheckpointOrigin: domain.ConversationCheckpointOriginCoordination, CoordinationID: "c1"}
		applyStopCheckpoint(&cp, &s, ownedCheckpoint(), checkpointNow)
		if cp.ConversationCheckpointState != domain.ConversationCheckpointCoordination || s.LatestAssistantUpdate != "" || cp.ConversationCheckpointTurnID != "c1" {
			t.Fatalf("checkpoint %+v signal %+v", cp, s)
		}
	})
	t.Run("owned stop without a prompt boundary is unsettled", func(t *testing.T) {
		cp := domain.SessionMetadata{LatestUserPrompt: "p"}
		s := ports.ActivitySignal{Event: "stop", LatestAssistantUpdate: "answer"}
		applyStopCheckpoint(&cp, &s, ownedCheckpoint(), checkpointNow)
		if !cp.ConversationCheckpointUnsettled || s.LatestAssistantUpdate != "" || cp.LatestAssistantUpdate != "" {
			t.Fatalf("checkpoint %+v signal %+v", cp, s)
		}
	})
	t.Run("unowned standalone stop is legacy text", func(t *testing.T) {
		cp := domain.SessionMetadata{}
		s := ports.ActivitySignal{Event: "stop", LatestAssistantUpdate: "answer"}
		applyStopCheckpoint(&cp, &s, checkpointOwner{harness: domain.HarnessClaudeCode}, checkpointNow)
		if cp.ConversationCheckpointState != domain.ConversationCheckpointLegacy || cp.LatestAssistantUpdate != "answer" {
			t.Fatalf("checkpoint %+v", cp)
		}
	})
	t.Run("unowned stop after a prompt is dropped", func(t *testing.T) {
		cp := domain.SessionMetadata{LatestUserPrompt: "p"}
		s := ports.ActivitySignal{Event: "stop", LatestAssistantUpdate: "answer"}
		applyStopCheckpoint(&cp, &s, checkpointOwner{harness: domain.HarnessClaudeCode}, checkpointNow)
		if s.LatestAssistantUpdate != "" || cp.LatestAssistantUpdate != "" {
			t.Fatalf("checkpoint %+v signal %+v", cp, s)
		}
	})
}
