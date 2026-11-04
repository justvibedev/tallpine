package usage

import (
	"strings"
)

// firstShortTrimmed returns the first value that, trimmed, is non-empty and at
// most 256 bytes. It is deliberately not cmp.Or: it trims and bounds.
func firstShortTrimmed(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" && len(value) <= 256 {
			return value
		}
	}
	return ""
}
