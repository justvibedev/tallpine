package domain

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"
)

// TurnFallbackDelay allows a worker to file its own report before fallback
// delivery. It is a var only so e2e builds can shorten it (turn_fallback_e2e.go).
var TurnFallbackDelay = time.Minute

// TurnFallbackExcerptCharacters bounds persisted final-answer excerpts.
const TurnFallbackExcerptCharacters = 500

// ErrTurnFallbackInactive means a completion was already handled or superseded.
var ErrTurnFallbackInactive = errors.New("turn fallback is no longer pending")

// TurnFallback identifies one committed primary completion, not a display status.
// Chat TurnID is Tallpine's durable turn ID. TUI uses a native turn within a launch/thread.
type TurnFallback struct {
	ID                           string
	SessionID                    SessionID
	Mode                         SessionMode
	Generation, NativeID, TurnID string
	CompletedAt, DueAt           time.Time
	Excerpt                      string
}

// StableID deduplicates one identified completion across process restarts.
func (f TurnFallback) StableID() string {
	identity := string(f.SessionID) + "\x00" + string(f.Mode) + "\x00" + f.TurnID
	if f.Mode == SessionModeTUI {
		identity += "\x00" + f.Generation + "\x00" + f.NativeID
	}
	return fmt.Sprintf("auto-turn-ended:%x", sha256.Sum256([]byte(identity)))
}

// TurnFallbackExcerpt trims and bounds text without splitting Unicode characters.
func TurnFallbackExcerpt(text string) string {
	r := []rune(strings.TrimSpace(text))
	if len(r) > TurnFallbackExcerptCharacters {
		return string(r[:TurnFallbackExcerptCharacters-1]) + "…"
	}
	return string(r)
}

// chatTurnFallbackDelay is the grace for a chat turn: ACP prompt completion is
// a definite end, so only a brief window lets an in-flight own report land first.
const chatTurnFallbackDelay = 3 * time.Second

// ChatTurnFallbackDelay is the fallback delay for chat-mode turns, never longer
// than TurnFallbackDelay (which e2e builds may shorten). A worker report
// backgrounded past prompt completion loses to the synthesized turn_ended.
func ChatTurnFallbackDelay() time.Duration {
	return min(chatTurnFallbackDelay, TurnFallbackDelay)
}

const (
	usageLimitExcerptPrefix = "usage-limit-until:"
	usageLimitAutoSuffix    = "|auto"
)

// UsageLimitFallbackExcerpt is the fallback excerpt of a turn that stopped on the
// provider's usage limit. The excerpt column carries it so the delivered notice
// can say the worker is paused instead of quoting the earlier work. auto says
// Tallpine will resume the turn itself at until.
func UsageLimitFallbackExcerpt(until time.Time, auto bool) string {
	out := usageLimitExcerptPrefix + until.UTC().Format(time.RFC3339)
	if auto {
		out += usageLimitAutoSuffix
	}
	return out
}

// ParseUsageLimitFallbackExcerpt reports the pause end a fallback excerpt
// carries and whether Tallpine resumes the turn by itself.
func ParseUsageLimitFallbackExcerpt(excerpt string) (until time.Time, auto, ok bool) {
	rest, found := strings.CutPrefix(excerpt, usageLimitExcerptPrefix)
	if !found {
		return time.Time{}, false, false
	}
	rest, auto = strings.CutSuffix(rest, usageLimitAutoSuffix)
	until, err := time.Parse(time.RFC3339, rest)
	return until, auto, err == nil
}
