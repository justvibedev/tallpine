package chat

import (
	"context"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// ErrClearWhileBusy reports a /clear on a conversation with a turn in flight. A
// clear replaces the thread the turn runs on, so it waits for the turn to end.
var ErrClearWhileBusy = errors.New("wait for the current turn to finish before clearing the context") //nolint:staticcheck // user-facing copy

// contextClearedSummary is the transcript marker for a cleared context.
const contextClearedSummary = "Context cleared"

// branchProviderMover durably rebinds the active branch and session to a
// replacement provider conversation.
type branchProviderMover interface {
	RebindActiveBranchProvider(ctx context.Context, sessionID domain.SessionID, conversationID, previous, next, generation string, now time.Time) error
}

// ClearContext starts a fresh provider conversation with the same settings and
// moves the session onto it. The rebind is durable before the driver switches, so
// reattach and daemon-restart restore resume the new id; a failure at any step
// leaves the old conversation in place. It returns the marker row's id.
func (c *Controller) ClearContext(ctx context.Context) (string, error) {
	clearer, ok := c.conv.(ports.ChatContextClearer)
	mover, moverOK := c.store.(branchProviderMover)
	if !ok || !moverOK {
		return "", ErrClearUnsupported
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.handoffActive() {
		return "", ErrControllerHandoff
	}
	if c.busy() {
		return "", ErrClearWhileBusy
	}
	previous := c.conv.ProviderConversationID()
	err := clearer.ClearContext(ctx, func(next string) error {
		if err := mover.RebindActiveBranchProvider(context.WithoutCancel(ctx), c.sessionID, c.conversation.ID, previous, next, c.generation, c.now()); err != nil {
			return fmt.Errorf("persist the new thread: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	markerID := c.newID()
	if err := c.store.UpsertActivity(context.WithoutCancel(ctx), c.conversation.ID, "", domain.ConversationActivity{
		ID:             markerID,
		Kind:           domain.ActivityKindSystem,
		Status:         domain.ActivityStatusCompleted,
		Summary:        contextClearedSummary,
		ProviderItemID: "tallpine-context-cleared-" + c.conv.ProviderConversationID(),
	}, c.now()); err != nil {
		// The rebind is committed and the agent is on the new thread: the clear
		// succeeded and only the marker is missing, so it is not a failure.
		c.log.Warn("context cleared but the marker was not recorded", "session", c.sessionID, "err", err)
		return "", nil
	}
	return markerID, nil
}
