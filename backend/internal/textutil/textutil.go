// Package textutil holds small string helpers shared across packages.
package textutil

import (
	"strings"
	"unicode/utf8"
)

// FirstNonBlank returns the first value that is not empty after trimming
// whitespace, unmodified, or "" if there is none. (For plain non-empty use
// cmp.Or.)
func FirstNonBlank(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// FirstTrimmed is FirstNonBlank but returns the trimmed value.
func FirstTrimmed(values ...string) string {
	for _, v := range values {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
}

// HeadBytes returns the first limit bytes of s, cut back to a rune boundary,
// and whether anything was cut.
func HeadBytes(s string, limit int) (string, bool) {
	if limit < 0 {
		limit = 0
	}
	if len(s) <= limit {
		return s, false
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}
