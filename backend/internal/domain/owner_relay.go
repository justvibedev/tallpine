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

// WorkerReportsOpen and WorkerReportsClose delimit the daemon-authored worker
// reports attached after an owner's text in one stored turn. Their spelling is
// a persisted wire format shared with the Chat UI.
const (
	WorkerReportsOpen  = "<ao-worker-reports>"
	WorkerReportsClose = "</ao-worker-reports>"
)

// StripWorkerReports returns the owner's own text from a stored turn that had
// worker reports attached. Text is returned unchanged unless it holds exactly
// one report block at its end: worker text can contain the tag, so a second
// one leaves the boundary unknown, and IsGenuineOwnerTurn then refuses the turn.
func StripWorkerReports(text string) string {
	if strings.Count(text, WorkerReportsOpen) != 1 || !strings.HasSuffix(text, "\n"+WorkerReportsClose) {
		return text
	}
	owner, _, found := strings.Cut(text, "\n\n"+WorkerReportsOpen+"\n")
	if !found {
		return text
	}
	return owner
}

// IsMachineAuthored reports whether m is a row the owner never typed, whatever
// else it contains: not a human user turn, sent by a session, an opening spawn
// brief, a Tallpine [auto] notice, or a [from ...] relay. "last" looks past these
// and treats any other row as the owner's turn. A frame is not a signal here: an
// owner can paste one, so a framed row stays the newest turn and is refused.
func (m OwnerChatMessage) IsMachineAuthored() bool {
	if m.Role != MessageRoleUser || m.Origin != MessageOriginHuman || m.SenderSessionID != "" || m.SpawnBrief {
		return true
	}
	text := strings.TrimSpace(m.Text)
	for _, prefix := range []string{"[auto]", "[from "} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

// IsGenuineOwnerTurn reports whether m is a turn the owner typed themselves and
// its text can be stamped as theirs: not machine-authored, not empty, and with
// no frame, owner stamp or worker-report tag left in the text.
func (m OwnerChatMessage) IsGenuineOwnerTurn() bool {
	if m.IsMachineAuthored() || strings.TrimSpace(m.Text) == "" {
		return false
	}
	for _, marker := range []string{"<<worker:", "<<orchestrator:", ownerStampLabel, WorkerReportsOpen, WorkerReportsClose} {
		if strings.Contains(m.Text, marker) {
			return false
		}
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
