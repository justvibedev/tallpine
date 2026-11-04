package domain

import (
	"strconv"
	"strings"
)

// FormatBacklogRef renders a per-project sequence number as "B12".
func FormatBacklogRef(seq int64) string { return "B" + strconv.FormatInt(seq, 10) }

// ParseBacklogRef accepts "B12", "b12" or "12" and returns the sequence number.
func ParseBacklogRef(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "B"), "b")
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}
