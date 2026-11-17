package machineload

import (
	"strconv"
	"strings"
)

// Environment variables that force a limit, and the bounds both the settings
// registry and the migration's CHECK constraints enforce.
const (
	EnvMaxReviewers = "TALLPINE_MACHINE_MAX_REVIEWERS"
	EnvLoadPercent  = "TALLPINE_MACHINE_LOAD_PERCENT"

	MaxReviewersCeiling = 100
	LoadPercentCeiling  = 1000
)

// ParseLimit reads an environment value as a limit in [0, ceiling]. Empty or
// invalid text forces nothing.
func ParseLimit(text string, ceiling int) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || n < 0 || n > ceiling {
		return 0, false
	}
	return n, true
}

// WithEnv returns l with each limit replaced by its environment variable when
// that holds a valid value. lookup reports a variable's text and presence.
func (l Limits) WithEnv(lookup func(name string) (string, bool)) Limits {
	if lookup == nil {
		return l
	}
	if text, ok := lookup(EnvMaxReviewers); ok {
		if n, valid := ParseLimit(text, MaxReviewersCeiling); valid {
			l.MaxReviewers = n
		}
	}
	if text, ok := lookup(EnvLoadPercent); ok {
		if n, valid := ParseLimit(text, LoadPercentCeiling); valid {
			l.LoadPercent = n
		}
	}
	return l
}
