package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// FrameUntrusted wraps text a worker authored before it is shown to an
// orchestrator, so the orchestrator can tell data from instructions:
//
//	<<worker:SESSION n=9f3a1c2d>>
//	text
//	<</worker n=9f3a1c2d>>
//
// The nonce is random per frame, and "<<" / ">>" inside the text are broken
// up, so the text can neither guess nor forge the closing line. Framing is a
// signal to the model, not a guarantee; it does not authorize anything.
func FrameUntrusted(session, text string) string {
	return frameLabeled("worker", session, text, randomNonce())
}

func randomNonce() string {
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("domain: no randomness for untrusted frame: " + err.Error())
	}
	return hex.EncodeToString(raw[:])
}

// FrameUntrustedFor is FrameUntrusted with a nonce derived from the delivery
// (session, handle and text), so a retried delivery with the same handle
// produces the same bytes and stays idempotent. The nonce is then guessable by
// the sender, so it is only a second layer; the delimiter neutralisation is
// what stops a forged closing line (also when handle is empty).
func FrameUntrustedFor(session, handle, text string) string {
	return frameLabeled("worker", session, text, deliveryNonce(session, handle, text))
}

func deliveryNonce(session, handle, text string) string {
	sum := sha256.Sum256([]byte(session + "\x00" + handle + "\x00" + text))
	return hex.EncodeToString(sum[:4])
}

// FrameUntrustedOrchestrator is FrameUntrusted for text another orchestrator
// (for example a lane's) authored; the label is "orchestrator" so the reader
// can tell the sender kind.
func FrameUntrustedOrchestrator(session, text string) string {
	return frameLabeled("orchestrator", session, text, randomNonce())
}

// FrameUntrustedOrchestratorFor is FrameUntrustedFor for an orchestrator sender.
func FrameUntrustedOrchestratorFor(session, handle, text string) string {
	return frameLabeled("orchestrator", session, text, deliveryNonce(session, handle, text))
}

func frameLabeled(label, session, text, n string) string {
	return "<<" + label + ":" + neutralizeFrame(session) + " n=" + n + ">>\n" + neutralizeFrame(text) + "\n<</" + label + " n=" + n + ">>"
}

// neutralizeFrame breaks every frame delimiter look-alike in s.
func neutralizeFrame(s string) string {
	// ReplaceAll does not overlap: "<<<" becomes "< <<", so repeat until no pair is left.
	for strings.Contains(s, "<<") {
		s = strings.ReplaceAll(s, "<<", "< <")
	}
	for strings.Contains(s, ">>") {
		s = strings.ReplaceAll(s, ">>", "> >")
	}
	return s
}
