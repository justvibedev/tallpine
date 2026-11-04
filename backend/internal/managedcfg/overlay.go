package managedcfg

import (
	"encoding/json"
	"strings"
)

// Overlay applies the managed values for one JSON-encoded config struct. A
// field is addressed by Prefix plus its JSON name (for example "project." +
// "models"). It is the single implementation of the read-side overlay, the
// write-side restore and the locked-change check; callers keep only their own
// type and prefix.
type Overlay[T any] struct {
	Locks  *Locks
	Prefix string
	// Base returns the value a managed field is decoded over. Nil means the
	// zero value.
	Base func() T
	// Validate, when set, must accept an overlaid result; otherwise the
	// overlay is skipped and the input returned unchanged.
	Validate func(T) error
}

func (o Overlay[T]) base() T {
	if o.Base != nil {
		return o.Base()
	}
	var zero T
	return zero
}

// Apply sets every locked field that has a managed value into v. A value that
// does not decode is ignored (the field stays at its stored value).
func (o Overlay[T]) Apply(v T) T {
	managed := map[string]json.RawMessage{}
	for key, raw := range o.Locks.values() {
		if name, ok := strings.CutPrefix(key, o.Prefix); ok && raw != nil {
			managed[name] = raw
		}
	}
	if len(managed) == 0 {
		return v
	}
	doc := fields(v)
	if doc == nil {
		return v
	}
	changed := false
	for name, raw := range managed {
		// Decode over the base so the field is exactly what the profile says.
		probe := o.base()
		patch, _ := json.Marshal(map[string]json.RawMessage{name: raw})
		if json.Unmarshal(patch, &probe) != nil {
			continue
		}
		if f, ok := fields(probe)[name]; ok {
			doc[name] = f
		} else {
			delete(doc, name)
		}
		changed = true
	}
	if !changed {
		return v
	}
	next, ok := decode[T](doc)
	if !ok {
		return v
	}
	if o.Validate != nil && o.Validate(next) != nil {
		return v
	}
	return next
}

// Restore returns in with every locked field taken from stored, so a write
// never persists a managed value as the user's own.
func (o Overlay[T]) Restore(stored, in T) T {
	var names []string
	for key := range o.Locks.values() {
		if name, ok := strings.CutPrefix(key, o.Prefix); ok {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return in
	}
	din, ds := fields(in), fields(stored)
	if din == nil || ds == nil {
		return in
	}
	for _, name := range names {
		if f, ok := ds[name]; ok {
			din[name] = f
		} else {
			delete(din, name)
		}
	}
	out, ok := decode[T](din)
	if !ok {
		return in
	}
	return out
}

// Changed returns the locked keys whose value differs between a and b.
func (o Overlay[T]) Changed(a, b T) []string {
	var keys []string
	var da, db map[string]json.RawMessage
	for key := range o.Locks.values() {
		name, ok := strings.CutPrefix(key, o.Prefix)
		if !ok {
			continue
		}
		if da == nil {
			da, db = fields(a), fields(b)
		}
		if !jsonEqualOrBothEmpty(da[name], db[name]) {
			keys = append(keys, key)
		}
	}
	return keys
}

func jsonEqualOrBothEmpty(a, b json.RawMessage) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	return Equal(a, b)
}

func fields[T any](v T) map[string]json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(raw, &doc) != nil {
		return nil
	}
	return doc
}

func decode[T any](doc map[string]json.RawMessage) (T, bool) {
	var out T
	merged, err := json.Marshal(doc)
	if err != nil || json.Unmarshal(merged, &out) != nil {
		return out, false
	}
	return out, true
}
