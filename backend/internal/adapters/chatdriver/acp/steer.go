package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/ports"
)

const steeringMethod = "_session/steering"

// steerCancelNotice leads every steered prompt. The ACP agent delivers a steer
// at a priority that pre-empts the running generation, so a tool call in
// flight is aborted and the provider reports it to the model with a generic
// "The user doesn't want to take this action right now. STOP ..." result. The
// model reads that as an owner denial and stops (B491). The notice tells it
// what actually happened. It is wire-only: Tallpine records the unprefixed
// text, and flushHistoryUserMessage strips it from replays so reconcile matches.
const steerCancelNotice = "[Tallpine: this message arrived while you were working. If a tool call of " +
	"yours was just cancelled with \"The user doesn't want to take this action right now\", " +
	"that was this incoming message, not a decision by the user. Read the message below " +
	"and, unless it says otherwise, re-run the cancelled call.]\n\n"

type steeringResponse struct {
	Outcome string `json:"outcome"`
	Reason  string `json:"reason,omitempty"`
}

// providerRefusal marks a request the ACP agent rejected on its own terms, as
// opposed to one that never got through. The Chat service classifies errors that
// implement ChatRefusal() as definitive, so a reserved steer delivery handle can
// settle as rejected instead of remaining uncertain forever.
type providerRefusal struct{ err error }

func (e *providerRefusal) Error() string     { return e.err.Error() }
func (e *providerRefusal) Unwrap() error     { return e.err }
func (e *providerRefusal) ChatRefusal() bool { return true }

// Steer maps Tallpine's existing mid-turn guidance contract onto ACP's steering
// extension. promptRequired is load-bearing: if the turn wins the race and ends
// before this request arrives, the agent returns the text to Tallpine instead of
// silently starting a detached provider turn with no durable Tallpine turn row.
func (c *conversation) Steer(
	ctx context.Context,
	providerTurnID string,
	msg ports.ChatUserMessage,
) (ports.ChatTurnRef, error) {
	if strings.TrimSpace(msg.Text) == "" {
		return ports.ChatTurnRef{}, errors.New("steer message text is empty")
	}
	msg.Text = steerCancelNotice + msg.Text
	prompt, err := c.promptContent(msg)
	if err != nil {
		return ports.ChatTurnRef{}, fmt.Errorf("%w: %w", ports.ErrChatSteerContentUnsupported, err)
	}

	c.mu.Lock()
	activeTurn := c.activeTurn
	sessionID := c.sessionID
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ports.ChatTurnRef{}, errConversationClosed
	}
	if providerTurnID == "" {
		providerTurnID = activeTurn
	}
	if providerTurnID == "" || providerTurnID != activeTurn {
		return ports.ChatTurnRef{}, ports.ErrChatNoSteerableTurn
	}

	raw, err := c.conn.CallExtension(ctx, steeringMethod, map[string]any{
		"sessionId": sessionID,
		"prompt":    prompt,
		"_meta": map[string]any{
			"steering": map[string]any{"idleBehavior": "promptRequired"},
		},
	})
	if err != nil {
		wrapped := fmt.Errorf("ACP %s: %w", steeringMethod, err)
		// -32601 means the agent answered the JSON-RPC request and does not
		// implement this optional extension. That is a definite refusal, not a
		// lost transport reply that might have delivered the guidance.
		if isACPMethodNotFound(err) {
			return ports.ChatTurnRef{}, &providerRefusal{err: wrapped}
		}
		return ports.ChatTurnRef{}, wrapped
	}
	var response steeringResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return ports.ChatTurnRef{}, fmt.Errorf("decode ACP steering response: %w", err)
	}
	switch response.Outcome {
	case "injected":
		return ports.ChatTurnRef{ProviderTurnID: providerTurnID}, nil
	case "promptRequired":
		return ports.ChatTurnRef{}, ports.ErrChatNoSteerableTurn
	case "startedNewTurn":
		// Tallpine explicitly requested promptRequired, so this means the extension
		// contract was not honored. Claiming this joined the active turn would
		// misattribute the provider's detached work in durable history.
		return ports.ChatTurnRef{}, fmt.Errorf("ACP steering started a detached turn")
	default:
		return ports.ChatTurnRef{}, fmt.Errorf("ACP steering returned unknown outcome %q", response.Outcome)
	}
}
