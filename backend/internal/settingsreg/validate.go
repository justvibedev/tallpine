package settingsreg

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ErrUnknownKey is returned for a key the registry does not declare.
var ErrUnknownKey = errors.New("unknown setting")

// ErrInvalidValue marks a value a setting does not accept.
var ErrInvalidValue = errors.New("invalid setting value")

// Validate checks raw as a value of key k: its kind, enum and bounds, then the
// owner's validator. A valid value of one key can still be refused by its owner
// in combination with others (the owner validates the whole document on write).
func (r *Registry) Validate(k Key, raw json.RawMessage) error {
	s, ok := r.Lookup(k)
	if !ok {
		return fmt.Errorf("%w %q", ErrUnknownKey, k)
	}
	return s.ValidateValue(raw)
}

// ValidateChange checks raw as the new value of key k replacing previous. It
// is Validate except that a string over MaxBytes is accepted when it is no
// longer than previous, as the owners do (domain.ValidateAgentRulesCap), so a
// legacy oversized value can still be shortened.
func (r *Registry) ValidateChange(k Key, previous, raw json.RawMessage) error {
	s, ok := r.Lookup(k)
	if !ok {
		return fmt.Errorf("%w %q", ErrUnknownKey, k)
	}
	return s.validate(raw, stringLen(previous))
}

// ValidateValue checks raw as a value of s.
func (s Setting) ValidateValue(raw json.RawMessage) error { return s.validate(raw, -1) }

// stringLen is the byte length of a JSON string, or -1.
func stringLen(raw json.RawMessage) int {
	var v string
	if json.Unmarshal(raw, &v) != nil {
		return -1
	}
	return len(v)
}

// validate checks raw; a string up to previousLen bytes passes the MaxBytes cap.
func (s Setting) validate(raw json.RawMessage, previousLen int) error {
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte("null")) || len(raw) == 0 {
		if s.Nullable {
			return nil
		}
		return fmt.Errorf("%w: %s must have a value", ErrInvalidValue, s.Key)
	}
	if err := s.checkKind(raw, previousLen); err != nil {
		return fmt.Errorf("%w: %s %s", ErrInvalidValue, s.Key, err.Error())
	}
	if s.Validate != nil {
		if err := s.Validate(raw); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrInvalidValue, s.Key, err)
		}
	}
	return nil
}

// checkKind validates the shape, enum and bounds of raw.
func (s Setting) checkKind(raw json.RawMessage, previousLen int) error {
	strict := func(v any) error {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(v); err != nil || dec.More() {
			return errors.New("has the wrong type")
		}
		return nil
	}
	switch s.Kind {
	case KindBool:
		var v bool
		return strict(&v)
	case KindInt:
		var v int64
		if strict(&v) != nil {
			return errors.New("must be a whole number")
		}
		if (s.Min != nil && v < *s.Min) || (s.Max != nil && v > *s.Max) {
			return fmt.Errorf("must be between %d and %d", deref(s.Min), deref(s.Max))
		}
	case KindEnum:
		var v string
		if strict(&v) != nil || !slices.Contains(s.Enum, v) {
			return fmt.Errorf("must be one of %s", strings.Join(quoted(s.Enum), ", "))
		}
	case KindString, KindText:
		var v string
		if strict(&v) != nil {
			return errors.New("must be a string")
		}
		if s.MaxBytes > 0 && len(v) > s.MaxBytes && len(v) > previousLen {
			return fmt.Errorf("is %d bytes; the limit is %d", len(v), s.MaxBytes)
		}
	case KindStringList:
		var v []string
		return strict(&v)
	case KindStringMap:
		var v map[string]string
		return strict(&v)
	case KindObject:
		if !json.Valid(raw) {
			return errors.New("is not valid JSON")
		}
	default:
		return fmt.Errorf("has unknown kind %q", s.Kind)
	}
	return nil
}

func deref(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func quoted(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = fmt.Sprintf("%q", v)
	}
	return out
}
