package controllers

import (
	"context"
	"net/http"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// OwnerMessageLookup finds a message in a session's own chat (an id, or "last"
// for the newest genuine owner turn).
type OwnerMessageLookup interface {
	OwnChatMessage(ctx context.Context, session domain.SessionID, ref string) (domain.OwnerChatMessage, bool, error)
}

// maxRelayedOwnerText bounds the verbatim owner text a relay carries.
const maxRelayedOwnerText = 4096

// ownerRelayBlock verifies ref as an owner turn in the calling agent's own chat
// and renders the daemon-stamped block. A non-empty code is the refusal.
func (c *SessionsController) ownerRelayBlock(r *http.Request, ref string) (block, code, msg string) {
	agent, ok := scopedAgent(r)
	if !ok {
		return "", "OWNER_RELAY_AGENT_ONLY", "Only an agent session relays an owner message from its own chat"
	}
	if c.OwnerMessages == nil {
		return "", "OWNER_RELAY_UNAVAILABLE", "Owner-message relay is unavailable"
	}
	ref = strings.TrimSpace(ref)
	m, found, err := c.OwnerMessages.OwnChatMessage(r.Context(), agent.SessionID, ref)
	switch {
	case err != nil:
		return "", "OWNER_RELAY_LOOKUP_FAILED", "Could not read your chat; try again"
	case !found && ref == "last":
		return "", "OWNER_RELAY_NOT_FOUND", "Your chat has no message the owner typed: ask the owner"
	case !found:
		return "", "OWNER_RELAY_NOT_FOUND", "That message is not in your own chat"
	case !m.IsGenuineOwnerTurn() && ref == "last":
		return "", "OWNER_RELAY_LAST_UNUSABLE", "The owner's newest message (" + m.ID + ") cannot be verified as plain owner text, so nothing was sent and no older message was used: quote what you need and ask the owner to confirm"
	case !m.IsGenuineOwnerTurn():
		return "", "OWNER_RELAY_NOT_OWNER", "That message is not one the owner typed (an agent turn, a relayed or framed message, or an [auto] notice)"
	case len(m.Text) > maxRelayedOwnerText:
		return "", "OWNER_RELAY_TOO_LONG", "That owner message is too long to relay; quote the part that matters and ask the owner"
	}
	return domain.RenderOwnerRelay(agent.SessionID, m), "", ""
}
