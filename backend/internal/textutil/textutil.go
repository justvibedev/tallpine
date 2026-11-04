// Package textutil holds small string helpers shared across packages.
package textutil

import "strings"

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
