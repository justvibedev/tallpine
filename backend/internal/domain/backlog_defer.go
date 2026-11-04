package domain

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// ErrInvalidDefer reports a `--defer` set that breaks the per-report limits.
var ErrInvalidDefer = errors.New("invalid --defer lines")

// DeferStatus is the outcome of one deferred line.
type DeferStatus string

// Deferred-line outcomes.
const (
	DeferFiled      DeferStatus = "filed"
	DeferMerged     DeferStatus = "merged"
	DeferInboxFull  DeferStatus = "inbox_full"
	DeferSessionCap DeferStatus = "session_cap"
	DeferFailed     DeferStatus = "failed"
)

// DeferOutcome reports what happened to one `tallpine report --defer` line. Reason
// is set whenever the line was not filed, so callers can say why.
type DeferOutcome struct {
	Line   string
	Status DeferStatus
	Ref    string
	Reason string
}

// ValidateDeferLines trims the lines and enforces the per-report limits: at
// most MaxDeferPerReport lines, each 1-MaxDeferLineLength characters.
func ValidateDeferLines(lines []string) ([]string, error) {
	if len(lines) > MaxDeferPerReport {
		return nil, ErrInvalidDefer
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" || utf8.RuneCountInString(l) > MaxDeferLineLength {
			return nil, ErrInvalidDefer
		}
		out = append(out, l)
	}
	return out, nil
}
