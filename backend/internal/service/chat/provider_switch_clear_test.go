package chat_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

// clearBriefRule sits in the middle of a long brief, where the provider-switch
// note's 2,000-character task cap would clip it.
const clearBriefRule = "BRIEF-RULE: publishing is authorized, open a PR"

// newClearHarness is a Chat session on the given agent with a long spawn brief
// and one answered turn. The edit harness starts on Codex; for Claude it is
// switched first, as a user does.
func newClearHarness(t *testing.T, agent domain.AgentHarness) (*harness, *editDriverState) {
	t.Helper()
	h, _, driver := newEditHarness(t, false)
	ctx := context.Background()
	record, found, err := h.st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("GetSession: found=%v err=%v", found, err)
	}
	record.Metadata.Prompt = strings.Repeat("brief head. ", 200) + clearBriefRule + strings.Repeat(" brief tail.", 200)
	if err := h.st.UpdateSession(ctx, record); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	turnID := "provider-turn-1"
	if agent == domain.HarnessClaudeCode {
		if err := h.svc.SwitchProvider(ctx, testSession, domain.ProviderSwitchRequest{
			TargetHarness: domain.HarnessClaudeCode, IdempotencyKey: "to-claude",
			Model: "claude-opus-5-5", AutoCompactWindow: "1m",
		}); err != nil {
			t.Fatalf("switch to Claude: %v", err)
		}
		driver.mu.Lock()
		h.conv = driver.fresh
		driver.mu.Unlock()
		turnID = "provider-turn-101"
	}
	completeTurn(t, h, "remember the number 42", turnID)
	awaitChatIdle(t, h)
	return h, driver
}

// awaitChatIdle waits for the session's live controller to be ready with
// every turn finished, so a /clear is not refused as busy.
func awaitChatIdle(t *testing.T, h *harness) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var state ports.ChatControllerState
	var turns []domain.TurnState
	for time.Now().Before(deadline) {
		controller, err := h.svc.Controller(testSession)
		if err != nil {
			t.Fatalf("Controller: %v", err)
		}
		snapshot, err := h.st.LoadConversationSnapshot(context.Background(), controller.ConversationID())
		if err != nil {
			t.Fatalf("LoadConversationSnapshot: %v", err)
		}
		state, turns = controller.State(), nil
		idle := state == ports.ChatControllerReady
		for _, turn := range snapshot.Turns {
			turns = append(turns, turn.State)
			idle = idle && turn.State.Terminal()
		}
		if idle {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the agent never went idle: controller %q, turns %v", state, turns)
}

func startCount(driver *editDriverState) int {
	driver.mu.Lock()
	defer driver.mu.Unlock()
	return len(driver.startConfigs)
}

// A /clear never reaches the agent. Claude Code's own clear keeps the system
// prompt but drops the brief and everything else Tallpine sent as messages, and
// the adapter keeps the pre-clear transcript id; Codex reads it as prompt text,
// and a bare new thread keeps only the developer instructions. Tallpine hands
// over instead: a fresh provider conversation of the same agent starts with the
// standing instructions and a note carrying the whole brief and the
// conversation, on the same model, bound durably, with a clear marker.
func TestClearHandsOverToAFreshContext(t *testing.T) {
	t.Parallel()
	for _, agent := range []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessCodex} {
		t.Run(string(agent), func(t *testing.T) {
			t.Parallel()
			assertClearHandsOver(t, agent)
		})
	}
}

func assertClearHandsOver(t *testing.T, agent domain.AgentHarness) {
	h, driver := newClearHarness(t, agent)
	ctx := context.Background()
	beforeStarts := startCount(driver)
	before := h.conv
	controller, err := h.svc.Controller(testSession)
	if err != nil {
		t.Fatalf("Controller: %v", err)
	}
	model := controller.Settings().Model

	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "/clear", Origin: domain.MessageOriginHuman})
	if err != nil {
		t.Fatalf("Send(/clear): %v", err)
	}
	before.mu.Lock()
	for _, sent := range before.sent {
		if strings.TrimSpace(sent.Text) == "/clear" {
			before.mu.Unlock()
			t.Fatalf("/clear was forwarded to the agent: %v", before.sent)
		}
	}
	before.mu.Unlock()
	if got := startCount(driver); got != beforeStarts+1 {
		t.Fatalf("provider starts = %d, want one fresh start after %d", got, beforeStarts)
	}
	cfg := lastStart(driver)
	for _, want := range []string{"preserved prompt", "cleared your context with /clear", clearBriefRule, "remember the number 42"} {
		if !strings.Contains(cfg.SystemPrompt, want) {
			t.Fatalf("fresh context's system prompt is missing %q:\n%s", want, cfg.SystemPrompt)
		}
	}
	if strings.Contains(cfg.SystemPrompt, "taking over this Tallpine session from") {
		t.Fatalf("a clear's note must not read as a switch from another agent:\n%s", cfg.SystemPrompt)
	}
	if cfg.Model != model {
		t.Fatalf("fresh context model = %q, want the session's %q", cfg.Model, model)
	}
	if agent == domain.HarnessClaudeCode {
		if got := cfg.Env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]; got != "1000000" {
			t.Fatalf("fresh context compaction window = %q, want the session's 1m", got)
		}
	}

	record, found, err := h.st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("GetSession: found=%v err=%v", found, err)
	}
	if record.Harness != agent || record.Mode != domain.SessionModeChat {
		t.Fatalf("session after clear = %s/%s, want %s Chat", record.Harness, record.Mode, agent)
	}
	session, branch := boundIDs(t, h)
	if session == "" || session == before.ProviderConversationID() || branch != session {
		t.Fatalf("bound ids after clear = %q/%q, want both on the fresh provider conversation", session, branch)
	}
	if _, providerID, ok := chatsvc.StartConfigOwnerIDs(h.svc, testSession); !ok || providerID != session {
		t.Fatalf("retained start-config owner = %q (found=%v), want %q: a stale fence breaks the next branch edit or reconnect", providerID, ok, session)
	}
	conversation, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil {
		t.Fatalf("ConversationForSession: %v", err)
	}
	snapshot, err := h.st.LoadConversationSnapshot(ctx, conversation.ID)
	if err != nil {
		t.Fatalf("LoadConversationSnapshot: %v", err)
	}
	if !strings.Contains(snapshot.ActiveBranch.HandoffNote, clearBriefRule) {
		t.Fatalf("the clear's note was not kept with the new branch, so a restart would lose it: %q", snapshot.ActiveBranch.HandoffNote)
	}
	if turn.ContextClearedActivityID != snapshot.ActiveBranch.ID+":context-cleared" {
		t.Fatalf("cleared marker id = %q, want the new branch's %q", turn.ContextClearedActivityID, snapshot.ActiveBranch.ID+":context-cleared")
	}
	var marker *domain.ConversationActivity
	for index, activity := range snapshot.Activities {
		if activity.ID == turn.ContextClearedActivityID {
			marker = &snapshot.Activities[index]
		}
		if strings.Contains(activity.Summary, "from "+string(agent)+" to "+string(agent)) {
			t.Fatalf("a clear left a provider-switch marker: %+v", activity)
		}
	}
	if marker == nil || marker.Summary != "Context cleared" || marker.Kind != domain.ActivityKindSystem {
		t.Fatalf("clear marker = %+v, want a Context cleared system row", marker)
	}
}

// Steer and SteerOrSend clear the same way, and neither forwards the text.
func TestSteerPathsClearByHandoff(t *testing.T) {
	t.Parallel()
	for _, agent := range []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessCodex} {
		t.Run(string(agent), func(t *testing.T) {
			t.Parallel()
			h, driver := newClearHarness(t, agent)
			ctx := context.Background()
			before := h.conv
			beforeStarts := startCount(driver)
			if res, err := h.svc.Steer(ctx, testSession, ports.ChatUserMessage{Text: "/clear"}); err != nil || res.ActivityID == "" {
				t.Fatalf("Steer(/clear) = %+v, %v", res, err)
			}
			awaitChatIdle(t, h)
			res, err := h.svc.SteerOrSend(ctx, testSession, ports.ChatUserMessage{Text: "/clear", ClientMessageID: "c1"}, false)
			if err != nil || res.Steered || !res.Cleared || res.Steer.ActivityID == "" {
				t.Fatalf("SteerOrSend(/clear) = %+v, %v", res, err)
			}
			if got := startCount(driver); got != beforeStarts+2 {
				t.Fatalf("provider starts = %d, want two fresh starts after %d", got, beforeStarts)
			}
			before.mu.Lock()
			defer before.mu.Unlock()
			if len(before.sent) != 1 {
				t.Fatalf("a /clear reached the agent: %v", before.sent)
			}
		})
	}
}

// A clear replaces the provider conversation a turn runs on, so it waits: it is
// refused, nothing starts, and the ids stay.
func TestClearRefusedWhileTurnRunning(t *testing.T) {
	t.Parallel()
	for _, agent := range []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessCodex} {
		t.Run(string(agent), func(t *testing.T) {
			t.Parallel()
			h, driver := newClearHarness(t, agent)
			ctx := context.Background()
			if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "work", Origin: domain.MessageOriginHuman}); err != nil {
				t.Fatalf("Send: %v", err)
			}
			beforeStarts := startCount(driver)
			session, branch := boundIDs(t, h)
			if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "/clear"}); !errors.Is(err, chatsvc.ErrClearWhileBusy) {
				t.Fatalf("Send(/clear) = %v, want ErrClearWhileBusy", err)
			}
			if _, err := h.svc.SteerOrSend(ctx, testSession, ports.ChatUserMessage{Text: "/clear", ClientMessageID: "c9"}, false); !errors.Is(err, chatsvc.ErrClearWhileBusy) {
				t.Fatalf("SteerOrSend(/clear) = %v, want ErrClearWhileBusy", err)
			}
			if got := startCount(driver); got != beforeStarts {
				t.Fatalf("a refused clear started a provider (%d starts, want %d)", got, beforeStarts)
			}
			if afterSession, afterBranch := boundIDs(t, h); afterSession != session || afterBranch != branch {
				t.Fatalf("ids moved to %q/%q despite the refusal, want %q/%q", afterSession, afterBranch, session, branch)
			}
		})
	}
}
