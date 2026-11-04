package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

func TestStaleControllerEventsDoNotReachTheTimeline(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()
	if err := h.st.ClaimChatControllerGeneration(ctx, testSession, "replacement-generation"); err != nil {
		t.Fatalf("replace controller generation: %v", err)
	}

	h.conv.emit(ports.ChatEvent{
		Kind:           ports.ChatEventMessageDelta,
		ProviderTurnID: "stale-turn",
		ProviderItemID: "stale-message",
		Delta:          "must not survive",
	})
	if err := h.svc.Stop(ctx, testSession); err != nil {
		t.Fatalf("stop stale controller: %v", err)
	}

	snapshot, err := h.st.LoadConversationSnapshot(ctx, h.ctrl.ConversationID())
	if err != nil {
		t.Fatalf("load conversation: %v", err)
	}
	for _, message := range snapshot.Messages {
		if message.Text == "must not survive" {
			t.Fatalf("stale controller message was projected: %+v", message)
		}
	}
}

/* ---- tests ------------------------------------------------------------- */

func TestSendVerifiesExcerptAndFallsBackToTextForProvider(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct{ name, source, selected string }{
		{"desktop", "Keep this exact sentence.", "exact sentence"},
		{"native", "# Title\n\n- First\n- Second\n\n```ts\ncode();\n```", "Title\n\n• First\n• Second\n\nts\ncode();"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			seed, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
				Text: "seed", ClientMessageID: "excerpt-seed", Origin: domain.MessageOriginHuman,
			})
			if err != nil {
				t.Fatalf("send seed: %v", err)
			}
			h.conv.emit(
				ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: seed.ProviderTurnID},
				ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderTurnID: seed.ProviderTurnID,
					ProviderItemID: "excerpt-source", Delta: tt.source},
				ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: seed.ProviderTurnID,
					ProviderItemID: "excerpt-source", Text: tt.source},
				ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: seed.ProviderTurnID,
					TurnState: domain.TurnStateCompleted},
			)
			snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
				return len(s.Messages) == 2 && len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateCompleted
			})
			source := snapshot.Messages[1]

			_, err = h.svc.Send(ctx, testSession, ports.ChatUserMessage{
				Text: "Use it", ClientMessageID: "excerpt-followup", Origin: domain.MessageOriginHuman,
				Excerpts: []ports.ChatExcerptReference{{
					ConversationID: h.ctrl.ConversationID(), MessageID: source.ID,
					Revision: source.Revision, Text: tt.selected,
				}},
			})
			if err != nil {
				t.Fatalf("send with excerpt: %v", err)
			}
			sent := h.conv.sentMessages()
			if len(sent) != 2 || !strings.Contains(sent[1].Text, "Referenced chat excerpt") ||
				!strings.Contains(sent[1].Text, tt.selected) || !strings.Contains(sent[1].Text, "User:\nseed\nAssistant:\n"+tt.source) || len(sent[1].Content) != 0 {
				t.Fatalf("provider delivery = %#v", sent)
			}
			duplicate, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
				Text: "Use it", ClientMessageID: "excerpt-followup", Origin: domain.MessageOriginHuman,
				Excerpts: []ports.ChatExcerptReference{{
					ConversationID: h.ctrl.ConversationID(), MessageID: source.ID,
					Revision: source.Revision + 1, Text: "no longer relevant to the accepted retry",
				}},
			})
			if err != nil || duplicate.ID != "" {
				t.Fatalf("idempotent excerpt retry = (%+v, %v), want duplicate success", duplicate, err)
			}

			_, err = h.svc.Send(ctx, testSession, ports.ChatUserMessage{
				Text: "stale", Origin: domain.MessageOriginHuman,
				Excerpts: []ports.ChatExcerptReference{{
					ConversationID: h.ctrl.ConversationID(), MessageID: source.ID,
					Revision: source.Revision + 1, Text: tt.selected,
				}},
			})
			if !errors.Is(err, chatsvc.ErrExcerptStale) {
				t.Fatalf("stale excerpt error = %v, want ErrExcerptStale", err)
			}
		})
	}
}

func TestProviderPromptFailureSettlesTurnAndRecordsRecoveryOnce(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	turn, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{
		Text: "hello", ClientMessageID: "failure-prompt", Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatal(err)
	}
	message := "Provider rejected this request\n\nOriginal details with https://example.com/help"
	completion := ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID,
		ProviderEventID: "host:1", TurnState: domain.TurnStateCompleted,
		Err: ports.NewChatProviderFailure(
			"Provider rejected this request",
			"Original details with https://example.com/help",
			ports.ErrChatAuthRequired,
		),
	}
	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: turn.ProviderTurnID},
		completion,
		// A daemon restart can replay the terminal event with the same identity.
		completion,
	)
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateFailed &&
			s.Conversation.Account != nil && s.Conversation.Account.ReauthRequiredAt != nil
	})
	if snapshot.Turns[0].ErrorMessage != message {
		t.Fatalf("turn error = %q", snapshot.Turns[0].ErrorMessage)
	}
	if snapshot.Conversation.Account.ReauthReason != message {
		t.Fatalf("reauth reason = %q", snapshot.Conversation.Account.ReauthReason)
	}
	for _, activity := range snapshot.Activities {
		if activity.Kind == domain.ActivityKindError || strings.Contains(activity.ProviderItemID, "ao-reauth-") {
			t.Fatalf("terminal failure was duplicated as an activity: %#v", activity)
		}
	}
}

func TestStandaloneProviderFailurePreservesOpaqueText(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventError, ProviderEventID: "provider-error-1",
		Err: ports.NewChatProviderFailure(
			"Connection interrupted",
			"Inspect https://example.com/status",
			nil,
		),
	})

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Activities) == 1 && s.Activities[0].Kind == domain.ActivityKindError
	})
	activity := snapshot.Activities[0]
	if activity.Summary != "Connection interrupted\n\nInspect https://example.com/status" {
		t.Fatalf("summary = %q", activity.Summary)
	}
	var detail map[string]string
	if err := json.Unmarshal(activity.Detail, &detail); err != nil {
		t.Fatal(err)
	}
	if detail["error"] != activity.Summary {
		t.Fatalf("detail = %#v", detail)
	}
}

func TestTerminalFailureSettlesOnlyActiveRetry(t *testing.T) {
	t.Parallel()

	for _, source := range []string{"completion", "notification", "both"} {
		t.Run(source, func(t *testing.T) {
			h := newHarness(t)
			turn, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{
				Text: "hello", ClientMessageID: "retry-failure-prompt", Origin: domain.MessageOriginHuman,
			})
			if err != nil {
				t.Fatal(err)
			}
			retry := func(id string, kind ports.ChatEventKind, status domain.ActivityStatus) ports.ChatEvent {
				return ports.ChatEvent{
					Kind: kind, ProviderTurnID: turn.ProviderTurnID, ProviderItemID: id,
					ActivityKind: domain.ActivityKindSystem, ActivityStatus: status,
					Summary: "Retrying", Detail: json.RawMessage(`{"event":"provider.failure"}`),
				}
			}
			failure := ports.NewChatProviderFailure("Request failed", "Try later", nil)
			var completionError error
			if source != "notification" {
				completionError = failure
			}
			h.conv.emit(
				ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: turn.ProviderTurnID},
				retry("recovered-retry", ports.ChatEventActivityStarted, domain.ActivityStatusRunning),
				retry("recovered-retry", ports.ChatEventActivityCompleted, domain.ActivityStatusCompleted),
				retry("active-retry", ports.ChatEventActivityStarted, domain.ActivityStatusRunning),
			)
			if source != "completion" {
				h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventError, ProviderTurnID: turn.ProviderTurnID, Err: failure})
			}
			h.conv.emit(
				ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID,
					TurnState: domain.TurnStateFailed, Err: completionError},
			)
			snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
				return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateFailed
			})
			if got := findActivity(t, snapshot, "recovered-retry").Status; got != domain.ActivityStatusCompleted {
				t.Fatalf("recovered retry = %s", got)
			}
			if got := findActivity(t, snapshot, "active-retry").Status; got != domain.ActivityStatusFailed {
				t.Fatalf("active retry = %s", got)
			}
			wantError := "Request failed\n\nTry later"
			if source == "notification" {
				wantError = ""
			}
			if snapshot.Turns[0].ErrorMessage != wantError {
				t.Fatalf("failure = %q", snapshot.Turns[0].ErrorMessage)
			}
		})
	}
}

// The whole point: a message goes out, provider events come back, and the durable
// timeline reflects them in sequence order.
func TestProjectsAFullTurnIntoDurableRows(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()

	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text:            "what changed?",
		ClientMessageID: "client-1",
		Origin:          domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if turn.ProviderTurnID != "provider-turn-1" {
		t.Fatalf("provider turn = %q", turn.ProviderTurnID)
	}

	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"},
		ports.ChatEvent{
			Kind: ports.ChatEventActivityCompleted, ProviderTurnID: "provider-turn-1",
			ProviderItemID: "exec-1", ActivityKind: domain.ActivityKindCommand,
			ActivityStatus: domain.ActivityStatusCompleted, Summary: "git status --short",
		},
		// Streaming arrives in pieces and must fold into one message.
		ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderTurnID: "provider-turn-1", ProviderItemID: "msg-1", Delta: "Two "},
		ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderTurnID: "provider-turn-1", ProviderItemID: "msg-1", Delta: "files "},
		ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderTurnID: "provider-turn-1", ProviderItemID: "msg-1", Delta: "changed."},
		ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: "provider-turn-1", ProviderItemID: "msg-1", Text: "Two files changed."},
		ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted},
	)

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State.Terminal() && len(s.Messages) == 2
	})

	if got := snapshot.Turns[0].State; got != domain.TurnStateCompleted {
		t.Errorf("turn state = %q, want completed", got)
	}

	user, assistant := snapshot.Messages[0], snapshot.Messages[1]
	if user.Role != domain.MessageRoleUser || user.Text != "what changed?" {
		t.Errorf("user message = %+v", user)
	}
	if user.Origin != domain.MessageOriginHuman {
		t.Errorf("user origin = %q", user.Origin)
	}
	// Three deltas folded into one message, not three timeline entries.
	if assistant.Role != domain.MessageRoleAssistant || assistant.Text != "Two files changed." {
		t.Errorf("assistant message = %+v", assistant)
	}
	if assistant.Streaming {
		t.Error("assistant message still marked streaming after completion")
	}
	if assistant.Revision == 0 {
		t.Error("assistant revision never advanced despite streaming rewrites")
	}

	// Sequence is conversation-scoped and strictly increasing across both tables.
	var sequences []int64
	for _, m := range snapshot.Messages {
		sequences = append(sequences, m.Sequence)
	}
	for _, a := range snapshot.Activities {
		sequences = append(sequences, a.Sequence)
	}
	seen := map[int64]bool{}
	for _, seq := range sequences {
		if seen[seq] {
			t.Fatalf("sequence %d was handed out twice", seq)
		}
		seen[seq] = true
	}

	if len(snapshot.Activities) != 1 || snapshot.Activities[0].Summary != "git status --short" {
		t.Fatalf("activities = %+v", snapshot.Activities)
	}
}

func TestEarlyTurnStartedBindsDispatchingTurn(t *testing.T) {
	t.Parallel()

	conv := newFakeConversation()
	conv.onSend = func(providerTurnID string) {
		conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: providerTurnID})
	}
	h := newHarnessWithConversation(t, conv)
	ctx := context.Background()

	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text:            "race turn/start",
		ClientMessageID: "early-start-1",
		Origin:          domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if turn.ProviderTurnID != "provider-turn-1" {
		t.Fatalf("provider turn = %q, want provider-turn-1", turn.ProviderTurnID)
	}
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		if len(s.Turns) != 1 {
			return false
		}
		turn := s.Turns[0]
		return turn.ProviderTurnID == "provider-turn-1" && turn.State == domain.TurnStateRunning
	})

	conv.emit(ports.ChatEvent{
		Kind:           ports.ChatEventTurnCompleted,
		ProviderTurnID: "provider-turn-1",
		TurnState:      domain.TurnStateCompleted,
	})
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateCompleted
	})
	if len(snapshot.Turns) != 1 {
		t.Fatalf("turns = %d, want 1: %+v", len(snapshot.Turns), snapshot.Turns)
	}
}

func TestControllerCloseHonorsContextWhenProviderStreamStaysOpen(t *testing.T) {
	providerErr := errors.New("provider close failed")
	conv := &stuckConversation{fakeConversation: newFakeConversation(), closeErr: providerErr}
	h := newHarnessWithConversation(t, conv)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := h.ctrl.Close(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, providerErr) {
		t.Fatalf("Close error = %v, want provider error joined with deadline", err)
	}
	// Let the projection goroutine exit so the harness cleanup remains bounded.
	close(conv.events)
	h.ctrl.Wait()
}

// A retried send under the same client message id must not create a second turn.
func TestDuplicateSendDoesNotCreateASecondTurn(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()
	msg := ports.ChatUserMessage{Text: "hello", ClientMessageID: "client-dup", Origin: domain.MessageOriginHuman}

	if _, err := h.svc.Send(ctx, testSession, msg); err != nil {
		t.Fatalf("first Send: %v", err)
	}
	second, err := h.svc.Send(ctx, testSession, msg)
	if err != nil {
		t.Fatalf("retried Send returned an error instead of being ignored: %v", err)
	}
	if second.ProviderTurnID != "" {
		t.Errorf("retry reported a new provider turn %q", second.ProviderTurnID)
	}

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Turns) >= 1 })
	if len(snapshot.Turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(snapshot.Turns))
	}
	if len(snapshot.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(snapshot.Messages))
	}
}

func TestDeferredDriverStartsOnlyAfterProviderTurnIDIsDurable(t *testing.T) {
	t.Parallel()

	deferred := &deferredConversation{fakeConversation: newFakeConversation()}
	h := newHarnessWithConversation(t, deferred)
	deferred.start = func(providerTurnID string) error {
		snapshot, err := h.st.LoadConversationSnapshot(context.Background(), h.ctrl.ConversationID())
		if err != nil {
			return err
		}
		for _, turn := range snapshot.Turns {
			if turn.ProviderTurnID == providerTurnID {
				return nil
			}
		}
		return fmt.Errorf("provider turn %q was not bound before deferred start", providerTurnID)
	}

	turn, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{
		Text: "start through ACP", ClientMessageID: "deferred-1", Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if turn.ProviderTurnID == "" {
		t.Fatal("deferred turn has no provider id")
	}
}

// An approval must be stored pending, carry the provider's own decision list, and
// only resolve through a typed action.
func TestApprovalIsStoredPendingWithProviderDecisions(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "go", ClientMessageID: "c1"}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// The real captured shape: no decline on offer, plus a structured decision.
	h.conv.emit(ports.ChatEvent{
		Kind:           ports.ChatEventApprovalRequested,
		ProviderTurnID: "provider-turn-1",
		ProviderItemID: "0",
		RequestID:      "0",
		ActivityKind:   domain.ActivityKindCommand,
		ActivityStatus: domain.ActivityStatusPending,
		Summary:        "Run tallpine spawn",
		Decisions: []ports.ChatDecisionOption{
			{ID: "accept", Label: "Approve", Kind: ports.ChatDecisionAllowOnce},
			{ID: "acceptWithExecpolicyAmendment", Label: "Approve and remember this command", Kind: ports.ChatDecisionAllowAlways},
			{ID: "cancel", Label: "Cancel", Kind: ports.ChatDecisionRejectOnce},
		},
	})

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Activities) == 1
	})
	approval := snapshot.Activities[0]
	if approval.Kind != domain.ActivityKindApproval {
		t.Fatalf("kind = %q, want approval", approval.Kind)
	}
	if approval.Status != domain.ActivityStatusPending {
		t.Fatalf("status = %q, want pending", approval.Status)
	}
	if approval.RequestID != "0" {
		t.Fatalf("request id = %q; zero is a legitimate id and must survive", approval.RequestID)
	}

	var detail struct {
		Decisions []struct{ ID, Label, Kind string } `json:"decisions"`
	}
	if err := json.Unmarshal(approval.Detail, &detail); err != nil {
		t.Fatalf("detail not decodable: %v (%s)", err, approval.Detail)
	}
	if len(detail.Decisions) != 3 {
		t.Fatalf("stored %d decisions, want the provider's 3: %+v", len(detail.Decisions), detail.Decisions)
	}
	if detail.Decisions[0].Kind != string(ports.ChatDecisionAllowOnce) ||
		detail.Decisions[1].Kind != string(ports.ChatDecisionAllowAlways) ||
		detail.Decisions[2].Kind != string(ports.ChatDecisionRejectOnce) {
		t.Fatalf("stored decision kinds = %+v", detail.Decisions)
	}
	for _, d := range detail.Decisions {
		if d.ID == "decline" {
			t.Error("stored a decline option the provider never offered")
		}
	}

	// Resolving reaches the provider and then updates the row.
	if err := h.svc.Resolve(ctx, testSession, "0", ports.ChatDecision{ID: "accept"}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, ok := h.conv.decisionFor("0"); !ok {
		t.Error("decision never reached the provider")
	}
	resolved := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Activities) == 1 && s.Activities[0].Status == domain.ActivityStatusResolved
	})
	if resolved.Activities[0].Status != domain.ActivityStatusResolved {
		t.Fatalf("status = %q, want resolved", resolved.Activities[0].Status)
	}
	var resolvedDetail struct {
		Decision  string `json:"decision"`
		Decisions []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"decisions"`
	}
	if err := json.Unmarshal(resolved.Activities[0].Detail, &resolvedDetail); err != nil {
		t.Fatalf("resolved detail not decodable: %v (%s)", err, resolved.Activities[0].Detail)
	}
	if resolvedDetail.Decision != "accept" {
		t.Fatalf("resolved decision = %q, want accept", resolvedDetail.Decision)
	}
	if len(resolvedDetail.Decisions) != 3 {
		t.Fatalf("resolved decision options = %d, want preserved 3", len(resolvedDetail.Decisions))
	}
	if !hasActivitySignal(h.activity.snapshot(), domain.ActivityWaitingInput, "chat.approval.requested") {
		t.Fatalf("activity signals = %v, want waiting_input on approval request", h.activity.snapshot())
	}
	if !hasActivitySignal(h.activity.snapshot(), domain.ActivityActive, "chat.approval.resolved") {
		t.Fatalf("activity signals = %v, want active after approval resolve", h.activity.snapshot())
	}
}

func TestResolvingOneOfMultipleApprovalsKeepsWaitingInput(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "go", ClientMessageID: "c1"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	for _, requestID := range []string{"approval-1", "approval-2"} {
		h.conv.emit(ports.ChatEvent{
			Kind: ports.ChatEventApprovalRequested, ProviderTurnID: "provider-turn-1",
			ProviderItemID: requestID, RequestID: requestID, Summary: "Approve " + requestID,
			Decisions: []ports.ChatDecisionOption{{ID: "accept", Label: "Approve", Kind: ports.ChatDecisionAllowOnce}},
		})
	}
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Activities) == 2 &&
			s.Activities[0].Status == domain.ActivityStatusPending &&
			s.Activities[1].Status == domain.ActivityStatusPending
	})

	if err := h.svc.Resolve(ctx, testSession, "approval-1", ports.ChatDecision{ID: "accept"}); err != nil {
		t.Fatalf("Resolve first: %v", err)
	}
	if got := countActivitySignals(h.activity.snapshot(), domain.ActivityActive, "chat.approval.resolved"); got != 0 {
		t.Fatalf("active resolution signals after first approval = %d, want 0 while another approval is pending", got)
	}

	if err := h.svc.Resolve(ctx, testSession, "approval-2", ports.ChatDecision{ID: "accept"}); err != nil {
		t.Fatalf("Resolve second: %v", err)
	}
	if got := countActivitySignals(h.activity.snapshot(), domain.ActivityActive, "chat.approval.resolved"); got != 1 {
		t.Fatalf("active resolution signals after final approval = %d, want 1", got)
	}
}

func TestProviderResolutionKeepsWaitingInputUntilFinalApproval(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "go", ClientMessageID: "c1"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	for _, requestID := range []string{"approval-1", "approval-2"} {
		h.conv.emit(ports.ChatEvent{
			Kind: ports.ChatEventApprovalRequested, ProviderTurnID: "provider-turn-1",
			ProviderItemID: requestID, RequestID: requestID, Summary: "Approve " + requestID,
		})
	}
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Activities) == 2 })

	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventApprovalResolved, RequestID: "approval-1"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return s.Activities[0].Status == domain.ActivityStatusResolved
	})
	if got := countActivitySignals(h.activity.snapshot(), domain.ActivityActive, "chat.approval.resolved"); got != 0 {
		t.Fatalf("active provider-resolution signals after first approval = %d, want 0", got)
	}

	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventApprovalResolved, RequestID: "approval-2"})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := countActivitySignals(h.activity.snapshot(), domain.ActivityActive, "chat.approval.resolved"); got == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("active provider-resolution signals after final approval = %d, want 1",
		countActivitySignals(h.activity.snapshot(), domain.ActivityActive, "chat.approval.resolved"))
}

// A controller that dies mid-turn must not leave the turn looking like it is still
// working, and must not leave an approval the user can never answer.
func TestControllerDeathSettlesInFlightWork(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "go", ClientMessageID: "c1"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"},
		startedCommand("provider-turn-1", "exec-1", "sleep 60"),
	)
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Activities) == 1 })
	h.conv.emit(
		ports.ChatEvent{
			Kind: ports.ChatEventApprovalRequested, ProviderTurnID: "provider-turn-1",
			ProviderItemID: "0", RequestID: "0", ActivityKind: domain.ActivityKindCommand,
			ActivityStatus: domain.ActivityStatusPending, Summary: "Run something",
		},
	)
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Activities) == 2 })

	// The provider process dies: the stream closes with the turn still open.
	_ = h.conv.Close()
	h.ctrl.Wait()
	if err := h.svc.Stop(ctx, testSession); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State.Terminal()
	})
	if got := snapshot.Turns[0].State; got != domain.TurnStateFailed {
		t.Errorf("turn state = %q; an interrupted controller is not a completed turn", got)
	}
	if snapshot.Turns[0].ErrorMessage == "" {
		t.Error("orphaned turn carries no explanation")
	}
	if got := findActivity(t, snapshot, "0").Status; got == domain.ActivityStatusPending {
		t.Error("approval left pending after its controller died — the user can never answer it")
	}
	if got := findActivity(t, snapshot, "exec-1").Status; got != domain.ActivityStatusFailed {
		t.Errorf("running activity after controller death = %q, want failed", got)
	}
}

func TestControllerStreamClosureReportsSessionExited(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	// A provider can disappear without emitting a final controller-state event.
	// Closing the stream is still authoritative proof that this controller ended.
	if err := h.conv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	h.ctrl.Wait()

	for _, signal := range h.activity.snapshot() {
		if signal.State == domain.ActivityExited && signal.Event == "chat.controller.stopped" {
			return
		}
	}
	t.Fatalf("controller stream ended without an exited lifecycle signal: %+v", h.activity.snapshot())
}

func TestControllerReadyRunsBeforeStreamProjection(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	conv := newFakeConversation()
	if err := conv.Close(); err != nil {
		t.Fatalf("close provider stream: %v", err)
	}
	activity := &recordingActivity{}
	ready := false
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers:  fakeRegistry{driver: fakeDriver{conv: conv}},
		Activity: activity,
		Log:      slog.New(slog.DiscardHandler),
		NewID:    func() string { return "controller-ready-id" },
	})

	controller, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ControllerGeneration: "reserved-generation",
		ControllerReady: func(started chatsvc.StartResult) (chatsvc.ControllerCommit, error) {
			if signals := activity.snapshot(); len(signals) != 0 {
				t.Fatalf("provider events projected before controller-ready commit: %+v", signals)
			}
			if started.ProviderConversationID == "" || started.ControllerGeneration != "reserved-generation" {
				t.Fatalf("controller-ready result = %+v", started)
			}
			ready = true
			return chatsvc.ControllerCommit{Conversation: started.Conversation}, nil
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	controller.Wait()
	if !ready {
		t.Fatal("controller-ready callback was not called")
	}
	for _, signal := range activity.snapshot() {
		if signal.State == domain.ActivityExited && signal.Event == "chat.controller.stopped" {
			return
		}
	}
	t.Fatalf("stream closure was not projected after controller-ready: %+v", activity.snapshot())
}

func TestSwitchControllerReadyLeavesSourceGenerationForAtomicActivation(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	ctx := context.Background()
	record, found, err := st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("get source Chat session: found=%v err=%v", found, err)
	}
	record.Harness = domain.HarnessClaudeCode
	record.Metadata.ProviderConversationID = "source-provider"
	record.Metadata.ControllerGeneration = "source-generation"
	if err := st.UpdateSession(ctx, record); err != nil {
		t.Fatalf("seed source Chat owner: %v", err)
	}

	conv := newFakeConversation()
	readyErr := errors.New("stop after source-generation assertion")
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers:  fakeRegistry{driver: fakeDriver{conv: conv}},
		Activity: &recordingActivity{}, Log: slog.New(slog.DiscardHandler),
		NewID: func() string { return "switch-controller-ready" },
	})

	_, err = svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderScopeID: "switch-1:provider",
		ControllerGeneration: "target-generation",
		ControllerReady: func(chatsvc.StartResult) (chatsvc.ControllerCommit, error) {
			current, found, readErr := st.GetSession(ctx, testSession)
			if readErr != nil || !found {
				t.Fatalf("get owner in ControllerReady: found=%v err=%v", found, readErr)
			}
			if current.Metadata.ControllerGeneration != "source-generation" {
				t.Fatalf("ControllerReady preclaimed generation %q, want source-generation", current.Metadata.ControllerGeneration)
			}
			return chatsvc.ControllerCommit{}, readyErr
		},
	})
	if !errors.Is(err, readyErr) {
		t.Fatalf("Start error = %v, want ControllerReady assertion failure", err)
	}
}

func TestControllerReadyDurableSettingsRefreshBeforeFirstDispatch(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	conversation, err := st.CreateConversation(
		ctx, "switch-settings-conversation", domain.ConversationScopeProject,
		testProject, testSession, now,
	)
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if err := st.SetConversationSettings(ctx, conversation.ID, domain.ConversationSettings{
		Model: "source-provider-model", ReasoningEffort: "high",
		ApprovalMode: domain.PermissionModeAcceptEdits,
	}, now); err != nil {
		t.Fatalf("seed source settings: %v", err)
	}

	conv := newFakeConversation()
	nextID := 0
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID: func() string {
			nextID++
			return fmt.Sprintf("switch-settings-%d", nextID)
		},
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

	controller, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ControllerGeneration: "target-generation",
		ControllerReady: func(started chatsvc.StartResult) (chatsvc.ControllerCommit, error) {
			if err := st.SetConversationSettings(ctx, conversation.ID, domain.ConversationSettings{
				ApprovalMode: domain.PermissionModeAcceptEdits,
			}, now.Add(time.Second)); err != nil {
				return chatsvc.ControllerCommit{}, err
			}
			committed := started.Conversation
			committed.Settings = domain.ConversationSettings{ApprovalMode: domain.PermissionModeAcceptEdits}
			committed.UpdatedAt = now.Add(time.Second)
			return chatsvc.ControllerCommit{Conversation: committed}, nil
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := controller.Send(ctx, ports.ChatUserMessage{
		Text: "continue the handoff", ClientMessageID: "activation-message",
		Origin: domain.MessageOriginAutomation,
	}); err != nil {
		t.Fatalf("Send activation: %v", err)
	}

	sent := conv.sentMessages()
	if len(sent) != 1 {
		t.Fatalf("sent messages = %d, want 1", len(sent))
	}
	if sent[0].Settings.Model != "" || sent[0].Settings.Effort != "" ||
		sent[0].Settings.Approval != domain.PermissionModeAcceptEdits {
		t.Fatalf("activation settings = %+v, want target defaults with preserved approval", sent[0].Settings)
	}
}

// TestSetConfigOptionPersistsModelBeforeRouting covers the other model-change
// route: provider-owned pickers like Claude Code's model menu go through the
// config-options PATCH, which calls SetSettings directly. The pick must land on
// the session's durable metadata the same way a turn-settings model change does
// (#4893 follow-up), or a later TUI rebuild resumes with the stale model.
func TestSetConfigOptionPersistsModelBeforeRouting(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	ctx := context.Background()
	conv := &configOptionConversation{fakeConversation: newFakeConversation()}
	var (
		logMu sync.Mutex
		log   []string
	)
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return "config-option-model-id" },
		OnModelChanged: func(id domain.SessionID, model string) {
			logMu.Lock()
			defer logMu.Unlock()
			log = append(log, string(id)+":"+model)
		},
	})
	t.Cleanup(func() { _ = svc.Stop(ctx, testSession) })
	if _, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(),
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Picking a model through the config-options route persists it.
	options, err := svc.SetConfigOption(ctx, testSession, "model", ports.ChatConfigOptionValue{Select: "5.6-haiku"})
	if err != nil {
		t.Fatalf("SetConfigOption: %v", err)
	}
	if len(options) == 0 || options[0].Current.Select != "5.6-haiku" {
		t.Fatalf("post-change catalog = %+v, want the picked model", options)
	}
	if !reflect.DeepEqual(log, []string{string(testSession) + ":5.6-haiku"}) {
		t.Fatalf("model persistence log = %v, want [%s:5.6-haiku]", log, testSession)
	}

	// Re-applying the same selection is a no-op.
	if _, err := svc.SetConfigOption(ctx, testSession, "model", ports.ChatConfigOptionValue{Select: "5.6-haiku"}); err != nil {
		t.Fatalf("SetConfigOption (same): %v", err)
	}
	if !reflect.DeepEqual(log, []string{string(testSession) + ":5.6-haiku"}) {
		t.Fatalf("model persistence log after no-op = %v, want unchanged", log)
	}

	// A later pick through the same route supersedes the earlier one.
	if _, err := svc.SetConfigOption(ctx, testSession, "model", ports.ChatConfigOptionValue{Select: "5.6-sonnet"}); err != nil {
		t.Fatalf("SetConfigOption (sonnet): %v", err)
	}
	if !reflect.DeepEqual(log, []string{string(testSession) + ":5.6-haiku", string(testSession) + ":5.6-sonnet"}) {
		t.Fatalf("model persistence log = %v, want haiku then sonnet", log)
	}
}

// TestSetTurnSettingsPersistsModelBeforeRouting is the regression for the
// ChatUI ↔ TUI model-persistence bug (#4893). A model the user picks in ChatUI
// must be recorded on the session BEFORE the next prompt routes, so that when
// ChatUI later hands off to TUI, the rebuilt terminal resumes with the same
// model instead of reverting to the project default. The conversation row is
// the chat-side source of truth already; this pins the extra session metadata
// write that makes the choice visible to the TUI rebuild.
func TestSetTurnSettingsPersistsModelBeforeRouting(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	ctx := context.Background()
	conv := newFakeConversation()
	var (
		logMu sync.Mutex
		log   []string
	)
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return "persist-model-id" },
		// Production wires this to the session manager, which writes the model onto
		// the session's durable metadata before the next turn routes.
		OnModelChanged: func(id domain.SessionID, model string) {
			logMu.Lock()
			defer logMu.Unlock()
			log = append(log, string(id)+":"+model)
		},
	})
	t.Cleanup(func() { _ = svc.Stop(ctx, testSession) })
	if _, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(),
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// A model change is persisted immediately (before the next prompt routes).
	if _, err := svc.SetTurnSettings(ctx, testSession, domain.ConversationSettings{Model: "5.6-luna"}); err != nil {
		t.Fatalf("SetTurnSettings (luna): %v", err)
	}
	if !reflect.DeepEqual(log, []string{string(testSession) + ":5.6-luna"}) {
		t.Fatalf("model persistence log = %v, want [%s:5.6-luna]", log, testSession)
	}

	// Re-selecting the same model is a no-op: no redundant session write.
	if _, err := svc.SetTurnSettings(ctx, testSession, domain.ConversationSettings{Model: "5.6-luna"}); err != nil {
		t.Fatalf("SetTurnSettings (same): %v", err)
	}
	if !reflect.DeepEqual(log, []string{string(testSession) + ":5.6-luna"}) {
		t.Fatalf("model persistence log after no-op = %v, want unchanged", log)
	}

	// A later change to another model lands too, so the TUI rebuild picks up the
	// most recent choice rather than the first one.
	if _, err := svc.SetTurnSettings(ctx, testSession, domain.ConversationSettings{Model: "5.6-full"}); err != nil {
		t.Fatalf("SetTurnSettings (full): %v", err)
	}
	if !reflect.DeepEqual(log, []string{string(testSession) + ":5.6-luna", string(testSession) + ":5.6-full"}) {
		t.Fatalf("model persistence log = %v, want luna then full", log)
	}

	// Clearing the override must clear session metadata for a later TUI rebuild.
	if _, err := svc.SetTurnSettings(ctx, testSession, domain.ConversationSettings{}); err != nil {
		t.Fatalf("SetTurnSettings (clear): %v", err)
	}
	if !reflect.DeepEqual(log, []string{string(testSession) + ":5.6-luna", string(testSession) + ":5.6-full", string(testSession) + ":"}) {
		t.Fatalf("model persistence log = %v, want model override cleared", log)
	}
}

func TestControllerReadyDoesNotDependOnAFalliblePostCommitRead(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 18, 13, 0, 0, 0, time.UTC)
	conversation, err := st.CreateConversation(
		ctx, "switch-commit-conversation", domain.ConversationScopeProject,
		testProject, testSession, now,
	)
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if err := st.SetConversationSettings(ctx, conversation.ID, domain.ConversationSettings{
		Model: "source-provider-model", ReasoningEffort: "high",
		ApprovalMode: domain.PermissionModeAcceptEdits,
	}, now); err != nil {
		t.Fatalf("seed source settings: %v", err)
	}

	guardedStore := &failConversationReadStore{Store: st}
	conv := newFakeConversation()
	svc := chatsvc.New(chatsvc.Options{
		Store: guardedStore, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return "post-commit-read-id" },
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

	controller, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ControllerGeneration: "target-generation",
		ControllerReady: func(started chatsvc.StartResult) (chatsvc.ControllerCommit, error) {
			if err := st.SetConversationSettings(ctx, conversation.ID, domain.ConversationSettings{
				ApprovalMode: domain.PermissionModeAcceptEdits,
			}, now.Add(time.Second)); err != nil {
				return chatsvc.ControllerCommit{}, err
			}
			committed := started.Conversation
			committed.Settings = domain.ConversationSettings{ApprovalMode: domain.PermissionModeAcceptEdits}
			committed.UpdatedAt = now.Add(time.Second)
			return chatsvc.ControllerCommit{Conversation: committed}, nil
		},
	})
	if err != nil {
		t.Fatalf("Start after durable controller commit: %v", err)
	}
	if _, err := controller.Send(ctx, ports.ChatUserMessage{
		Text: "continue the handoff", ClientMessageID: "post-commit-activation",
		Origin: domain.MessageOriginAutomation,
	}); err != nil {
		t.Fatalf("Send activation: %v", err)
	}
	if guardedStore.reads != 0 {
		t.Fatalf("post-commit conversation reads = %d, want none", guardedStore.reads)
	}
	sent := conv.sentMessages()
	if len(sent) != 1 || sent[0].Settings.Model != "" || sent[0].Settings.Effort != "" {
		t.Fatalf("activation retained source settings: %+v", sent)
	}
}

// Dispatch reads the persisted mode. A TUI session must be refused even if a
// controller somehow exists, because the mode is the authority.
func TestSendRefusedForTUISession(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()

	tuiSession := domain.SessionID("p1-2")
	if _, err := h.st.CreateSession(ctx, domain.SessionRecord{
		ID:        tuiSession,
		ProjectID: testProject,
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessCodex,
		Mode:      domain.SessionModeTUI,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed tui session: %v", err)
	}

	_, err := h.svc.Send(ctx, tuiSession, ports.ChatUserMessage{Text: "hi", ClientMessageID: "c9"})
	if err == nil {
		t.Fatal("a TUI session accepted a chat send")
	}
	if !errorsIs(err, chatsvc.ErrNotChatMode) {
		t.Fatalf("err = %v, want ErrNotChatMode", err)
	}
}

// Every projected event is also archived, so a wrong projection can be repaired
// from the raw record instead of being the only surviving account.
func TestProviderEventsAreArchived(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "go", ClientMessageID: "c1"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"},
		ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderTurnID: "provider-turn-1", ProviderItemID: "msg-1", Delta: "hi"},
		ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted},
	)
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State.Terminal()
	})

	events, err := h.st.ProviderEventsSince(ctx, h.ctrl.ConversationID(), 0, 100)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if len(events) < 3 {
		t.Fatalf("archived %d events, want at least the 3 emitted", len(events))
	}
}

/* ---- the send queue ---------------------------------------------------- */

// A provider can start a turn Tallpine never dispatched: a compaction runs as its own
// turn, and so does work the provider resumes from its own history. Without a row
// for it, every item that turn emits correlates to no turn — the activities arrive
// with an empty turn id and the timeline silently stops grouping them, which reads
// to a user as the conversation falling apart.
func TestProviderStartedTurnIsAdoptedSoItsItemsCorrelate(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	// No Send: this turn is entirely the provider's doing.
	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-owned-1"},
		ports.ChatEvent{
			Kind: ports.ChatEventActivityCompleted, ProviderTurnID: "provider-owned-1",
			ProviderItemID: "exec-1", ActivityKind: domain.ActivityKindCommand,
			ActivityStatus: domain.ActivityStatusCompleted, Summary: "rg --files",
		},
		ports.ChatEvent{
			Kind: ports.ChatEventActivityCompleted, ProviderTurnID: "provider-owned-1",
			ProviderItemID: "exec-2", ActivityKind: domain.ActivityKindCommand,
			ActivityStatus: domain.ActivityStatusCompleted, Summary: "sed -n 1,40p x",
		},
	)

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Activities) == 2
	})

	if len(snapshot.Turns) != 1 {
		t.Fatalf("turns = %d, want the provider's turn adopted", len(snapshot.Turns))
	}
	if got := snapshot.Turns[0].ProviderTurnID; got != "provider-owned-1" {
		t.Errorf("adopted turn provider id = %q", got)
	}
	for _, activity := range snapshot.Activities {
		if activity.TurnID == "" {
			t.Errorf("activity %q has no turn id; the timeline cannot group it", activity.Summary)
		}
	}
}

// The exact #3749 sequence: the turn/completed projection fails (so the durable
// row stays 'running' and the UI keeps showing "Working"), then the user presses
// Stop and the provider refuses because the turn already ended on its side.
// Interrupt must settle the durable row as interrupted, cancel the queue behind
// it, and report idle — not answer CHAT_NO_ACTIVE_TURN and strand the user.
func TestProjectionFailureThenStopStillStopsTheTurn(t *testing.T) {
	t.Parallel()

	conv := newInterruptRecorder() // never marks anything active -> always refuses
	st := openStore(t)

	var counterMu sync.Mutex
	counter := 0
	activity := &recordingActivity{}
	clock := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	failingStore := &failingProjectStore{
		Store: st, failMethod: string(ports.ChatEventTurnCompleted), failed: make(chan struct{}),
	}
	svc := chatsvc.New(chatsvc.Options{
		Store:    failingStore,
		Sessions: st,
		Drivers:  fakeRegistry{driver: fakeDriver{conv: conv}},
		Activity: activity,
		Log:      slog.New(slog.DiscardHandler),
		NewID: func() string {
			counterMu.Lock()
			defer counterMu.Unlock()
			counter++
			return fmt.Sprintf("id-%03d", counter)
		},
		Now: func() time.Time { return clock },
	})

	ctrl, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID:     testSession,
		ProjectID:     testProject,
		Harness:       domain.HarnessCodex,
		WorkspacePath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })
	ctx := context.Background()

	if _, err := svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "running", ClientMessageID: "c1",
	}); err != nil {
		t.Fatalf("Send running: %v", err)
	}
	conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	awaitStoreSnapshot(t, st, ctrl.ConversationID(), func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
	})

	if _, err := svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "queued", ClientMessageID: "c2",
	}); err != nil {
		t.Fatalf("Send queued: %v", err)
	}

	// The completion's projection fails: the durable row stays 'running' and
	// afterProject never runs, so nothing drains and nothing reports idle.
	conv.emit(ports.ChatEvent{
		Kind:           ports.ChatEventTurnCompleted,
		ProviderTurnID: "provider-turn-1",
		TurnState:      domain.TurnStateCompleted,
	})
	select {
	case <-failingStore.failed:
	case <-time.After(4 * time.Second):
		t.Fatal("completion projection did not reach the injected rollback")
	}

	if err := svc.Interrupt(ctx, testSession); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}

	snapshot := awaitStoreSnapshot(t, st, ctrl.ConversationID(), func(s store.ConversationSnapshot) bool {
		states := turnStateByText(t, s)
		return states["running"] == domain.TurnStateInterrupted &&
			states["queued"] == domain.TurnStateInterrupted
	})
	states := turnStateByText(t, snapshot)
	if states["running"] != domain.TurnStateInterrupted {
		t.Errorf("running turn = %q, want interrupted", states["running"])
	}
	if states["queued"] != domain.TurnStateInterrupted {
		t.Errorf("queued turn = %q, want interrupted (cancelled, not dispatched)", states["queued"])
	}
	if got := conv.sentTexts(); len(got) != 1 {
		t.Fatalf("provider received %v; reconciliation must not release the queue", got)
	}
	if !hasActivitySignal(activity.snapshot(), domain.ActivityIdle, "chat.interrupt.reconciled") {
		t.Errorf("no idle signal after reconciliation; signals = %v", activity.snapshot())
	}
}

// Controller-state notifications perform durable cleanup before they change the
// volatile controller state. If that transaction rolls back, memory must continue
// reporting the last committed state rather than claiming the controller stopped.
func TestControllerStateChangesOnlyAfterProjectionCommits(t *testing.T) {
	t.Parallel()

	var failingStore *failingProjectStore
	h := newHarnessWithConversationAndStore(t, nil, func(st *sqlite.Store) chatsvc.Store {
		failingStore = &failingProjectStore{
			Store: st, failMethod: string(ports.ChatEventControllerState), failed: make(chan struct{}),
		}
		return failingStore
	})
	want := h.ctrl.State()

	h.conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventControllerState, ControllerState: ports.ChatControllerStopped,
	})
	select {
	case <-failingStore.failed:
	case <-time.After(4 * time.Second):
		t.Fatal("controller-state projection did not reach the injected rollback")
	}
	if got := h.ctrl.State(); got != want {
		t.Fatalf("controller state after rolled-back projection = %q, want committed %q", got, want)
	}
}

func TestControllerStoppedEventUsesGenerationOwnedCleanup(t *testing.T) {
	t.Parallel()

	var recordingStore *recordingCleanupStore
	h := newHarnessWithConversationAndStore(t, nil, func(st *sqlite.Store) chatsvc.Store {
		recordingStore = &recordingCleanupStore{Store: st, called: make(chan struct{}, 2)}
		return recordingStore
	})

	h.conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventControllerState, ControllerState: ports.ChatControllerStopped,
	})
	select {
	case <-recordingStore.called:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("explicit controller-stopped event did not use generation-owned cleanup")
	}
}

func TestApprovalAndQuestionSignalsCarryWhatTheyWaitFor(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "go", ClientMessageID: "c1"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	h.conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventApprovalRequested, ProviderTurnID: "provider-turn-1",
		ProviderItemID: "a1", RequestID: "a1", Summary: "bash",
		Detail:    []byte(`{"reason":"committing writes the git index","rawCommand":"git commit"}`),
		Decisions: []ports.ChatDecisionOption{{ID: "accept", Kind: ports.ChatDecisionAllowOnce}},
	})
	h.conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventInputRequested, ProviderTurnID: "provider-turn-1",
		RequestID: "q1", Summary: "Which database?", Input: &ports.ChatInputRequest{Message: "Which database?"},
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		var approval, question string
		for _, s := range h.activity.snapshot() {
			switch s.Event {
			case "chat.approval.requested":
				approval = s.ToolName + "|" + s.Waiting
			case "chat.input.requested":
				question = s.Waiting
			}
		}
		if approval == "bash|committing writes the git index" && question == "Which database?" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("approval %q question %q", approval, question)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
