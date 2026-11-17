package domain

import (
	"regexp"
	"strings"
	"time"
)

// OwnerQuestionNotifyAfter is how long an orchestrator must sit idle on a reply
// that asks the owner something before the owner is notified (B750). Long
// enough that an owner who is reading the chat answers first.
const OwnerQuestionNotifyAfter = 3 * time.Minute

// Owner questions (B750) reuse the plan_waiting kind like owner asks do: an
// orchestrator that ended its turn on a question to the owner waits on them
// the same way. The source key is the prefix, then the session id, ":" and
// the reply time, so one reply notifies once.
const (
	// OwnerQuestionSourcePrefix marks a question in the project's main chat.
	OwnerQuestionSourcePrefix = "owner-question:"
	// OwnerQuestionLaneSourcePrefix marks a question in a lane orchestrator's chat.
	OwnerQuestionLaneSourcePrefix = "owner-question-lane:"
)

var (
	ownerNeedsYouMarker = regexp.MustCompile(`(?i)^\s*(?:\*\*|__)?\s*needs you:`)
	ownerCodeAndLinks   = regexp.MustCompile("(?s)```.*?(?:```|$)|`[^`\n]*`|\\]\\([^)]*\\)|\\bhttps?://\\S+")
	ownerQuestionMark   = regexp.MustCompile(`[?？](?:[\s"'”’)\]*_]|$)`)
	ownerParagraphBreak = regexp.MustCompile(`\n[ \t]*\n`)
	// A next step that needs a yes, offered without a question mark.
	ownerOffer = regexp.MustCompile("(?i)\\b(?:want me to|shall i|should i|do you want|would you like|let me know (?:if|whether|when|which|what)|your call|ok to (?:land|merge|ship|release|proceed|go ahead)|say the word)\\b|\\bsay [\"“'*]+[^\"”'*\n]{1,24}[\"”'*]+")
)

// AsksOwner reports whether an orchestrator reply ends its turn on something
// the owner must answer: the "Needs you:" marker, a question in its closing
// paragraphs, or an offered next step that needs a yes ("Say \"merge\" and I
// land it."). A `?` inside code or a link, or running into more text (`a?.b`),
// is not a question, and a question early in a long status is not the ending.
// The frontend mirror is asksOwnerAtEnd in chat/WorkerUpdates.tsx; keep both
// in step (the shared test cases live in each side's tests).
func AsksOwner(text string) bool {
	if ownerNeedsYouMarker.MatchString(text) {
		return true
	}
	tail := ownerClosingProse(text)
	return ownerQuestionMark.MatchString(tail) || ownerOffer.MatchString(tail)
}

// ownerClosingProse is the last two non-empty paragraphs with code and links
// blanked, so a trailing link line does not hide the question above it.
func ownerClosingProse(text string) string {
	prose := ownerCodeAndLinks.ReplaceAllString(text, " ")
	var kept []string
	for _, p := range ownerParagraphBreak.Split(prose, -1) {
		if strings.TrimSpace(p) != "" {
			kept = append(kept, p)
		}
	}
	if len(kept) > 2 {
		kept = kept[len(kept)-2:]
	}
	return strings.Join(kept, "\n\n")
}
