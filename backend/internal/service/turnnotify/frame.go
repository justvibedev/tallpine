package turnnotify

import (
	"time"
	"unicode/utf8"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// framedWithin frames worker-derived text as untrusted and keeps the whole
// frame, closing line included, within n characters, shortening the text
// (neutralising delimiter look-alikes can lengthen it) rather than cutting the
// frame. The Tallpine-authored words around it stay outside the frame.
func framedWithin(rec domain.SessionRecord, text string, n int) string {
	text = truncate(text, n)
	for text != "" {
		framed := domain.FrameUntrusted(string(rec.ID), text)
		over := utf8.RuneCountInString(framed) - n
		if over <= 0 {
			return framed
		}
		// A rune can grow to under two when delimiters are broken, so half the
		// overflow always makes progress.
		r := []rune(text)
		cut := over/2 + 1
		if cut >= len(r) {
			return ""
		}
		text = string(r[:len(r)-cut-1]) + "…"
	}
	return ""
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// usageLimitDetail is Message's detail for KindUsageLimited.
func usageLimitDetail(until time.Time, auto bool) string {
	out := until.UTC().Format(time.RFC3339)
	if auto {
		out += "|auto"
	}
	return out
}
