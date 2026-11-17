package turnnotify

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"tallpine.eu/tallpine/backend/internal/brand"
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

// framedExcerpt frames a worker's last message within n characters. When the
// message does not fit whole (cut at the stored excerpt cap or by the report
// limit) it ends with a visible marker, outside the frame, pointing at the
// full text. A message that truly ends in "…" at exactly the cap is marked too;
// that is harmless.
func framedExcerpt(rec domain.SessionRecord, last string, n int) string {
	whole := framedWithin(rec, last, 1<<20)
	if utf8.RuneCountInString(whole) <= n && (utf8.RuneCountInString(last) < MaxExcerptCharacters || !strings.HasSuffix(last, "…")) {
		return whole
	}
	marker := fmt.Sprintf(" [truncated, full: %s session history %s]", brand.CLIName, rec.ID)
	return framedWithin(rec, truncate(last, MaxExcerptCharacters), n-utf8.RuneCountInString(marker)) + marker
}
