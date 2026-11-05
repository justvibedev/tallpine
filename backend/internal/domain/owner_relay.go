package domain

import (
	"fmt"
	"strings"
	"time"
)

// OwnerChatMessage is one message of a session's own chat, as the daemon
// stored it.
type OwnerChatMessage struct {
	ID              string
	Role            MessageRole
	Origin          MessageOrigin
	SenderSessionID string
	Text            string
	At              time.Time
	// SpawnBrief marks an opening task brief, stored as a human turn whoever
	// wrote it (an orchestrator, the daemon, or the owner); never relayable.
	SpawnBrief bool
}

// ownerStampLabel opens a daemon-verified owner block. Agent-authored text
// never carries it: NeutralizeOwnerStamp breaks it, and frames break every "<<".
const ownerStampLabel = "<<owner-verified"

// IsGenuineOwnerTurn reports whether m is a turn the owner typed themselves:
// a human user turn no session sent, not an opening spawn brief, not a
// Tallpine [auto] notice, and not text relayed or framed from another session.
func (m OwnerChatMessage) IsGenuineOwnerTurn() bool {
	if m.Role != MessageRoleUser || m.Origin != MessageOriginHuman || m.SenderSessionID != "" || m.SpawnBrief {
		return false
	}
	text := strings.TrimSpace(m.Text)
	switch {
	case text == "":
		return false
	case strings.HasPrefix(text, "[auto]"), strings.HasPrefix(text, "[from "):
		return false
	case strings.Contains(text, "<<worker:"), strings.Contains(text, "<<orchestrator:"), strings.Contains(text, ownerStampLabel):
		return false
	}
	return true
}

// NeutralizeOwnerStamp breaks every daemon owner-stamp look-alike in text an
// agent wrote, so only the daemon can open a verified owner block.
func NeutralizeOwnerStamp(s string) string {
	for strings.Contains(s, ownerStampLabel) {
		s = strings.ReplaceAll(s, ownerStampLabel, "< <owner-verified")
	}
	return s
}

// RenderOwnerRelay is the daemon-stamped block that carries an owner message
// verbatim from source's own chat. It goes outside any untrusted frame.
func RenderOwnerRelay(source SessionID, m OwnerChatMessage) string {
	n := randomNonce()
	return fmt.Sprintf("[auto] owner_message: Tallpine verified that the owner typed this in %s's own chat at %s (message %s). Verbatim:\n%s source=%s n=%s>>\n%s\n<</owner-verified n=%s>>",
		source, m.At.UTC().Format(time.RFC3339), m.ID, ownerStampLabel, neutralizeFrame(string(source)), n, neutralizeFrame(m.Text), n)
}
