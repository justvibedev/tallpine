package domain

import (
	"fmt"
	"regexp"
	"strings"
)

// Custom-field bounds: free-form key -> value pairs on an item.
const (
	MaxBacklogFields     = 32
	MaxBacklogFieldValue = 500
)

var backlogFieldKey = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,39}$`)

// ValidBacklogFieldKey reports whether key is an allowed custom-field key.
func ValidBacklogFieldKey(key string) bool { return backlogFieldKey.MatchString(key) }

// ApplyBacklogField sets key=value on fields (an empty value deletes the key)
// and enforces the key, value and count bounds.
func ApplyBacklogField(fields map[string]string, key, value string) (map[string]string, error) {
	if !ValidBacklogFieldKey(key) {
		return nil, fmt.Errorf("%w: key %q must match [a-z0-9][a-z0-9._-]{0,39}", ErrBacklogInvalidField, key)
	}
	out := make(map[string]string, len(fields)+1)
	for k, v := range fields {
		out[k] = v
	}
	value = strings.TrimSpace(value)
	if value == "" {
		delete(out, key)
		return out, nil
	}
	if len([]rune(value)) > MaxBacklogFieldValue {
		return nil, fmt.Errorf("%w: value for %q exceeds %d characters", ErrBacklogInvalidField, key, MaxBacklogFieldValue)
	}
	out[key] = value
	if len(out) > MaxBacklogFields {
		return nil, fmt.Errorf("%w: at most %d fields", ErrBacklogInvalidField, MaxBacklogFields)
	}
	return out, nil
}
