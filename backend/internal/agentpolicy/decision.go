package agentpolicy

import (
	"encoding/json"
	"slices"
	"sort"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Decision is the policy for one session, computed once at spawn, relaunch or
// restore and handed to every enforcement seam. It is immutable: seams read
// effects from rule outcomes and never read settings. The zero Decision
// restricts nothing, so callers without a policy (tests, tools) pass it.
type Decision struct {
	d *decisionData
}

type decisionData struct {
	session  SessionRef
	profile  string
	outcomes map[string]Outcome
	fields   map[string]map[string]FieldState
	warnings []string
	live     func(rule, field string) (json.RawMessage, bool)
}

// Outcome returns the named rule's outcome; ok is false for the zero Decision.
// Rule packages wrap this in a typed accessor.
func (d Decision) Outcome(rule string) (Outcome, bool) {
	if d.d == nil {
		return nil, false
	}
	o, ok := d.d.outcomes[rule]
	return o, ok
}

// Profile is the managed profile in force for this decision ("" for none).
func (d Decision) Profile() string {
	if d.d == nil {
		return ""
	}
	return d.d.profile
}

// Session is what the decision was made for.
func (d Decision) Session() SessionRef {
	if d.d == nil {
		return SessionRef{}
	}
	return d.d.session
}

// WithSession returns a copy bound to a session id, for flows that decide before
// the id is allocated.
func (d Decision) WithSession(id domain.SessionID) Decision {
	if d.d == nil {
		return d
	}
	c := *d.d
	c.session.ID = id
	return Decision{d: &c}
}

// Live returns the current effective value of a field, re-resolved now. It is
// the one sanctioned live read: seams that act on turn-time input (a chat
// switching permission mode mid-session) use it so a managed tightening applies
// to sessions that were decided before it. Not for anything else.
func (d Decision) Live(rule, field string) (json.RawMessage, bool) {
	if d.d == nil || d.d.live == nil {
		return nil, false
	}
	return d.d.live(rule, field)
}

// Source reports the layer that set a field for this decision.
func (d Decision) Source(rule, field string) Source {
	if d.d == nil {
		return SourceDefault
	}
	if s, ok := d.d.fields[rule][field]; ok {
		return s.Source
	}
	return SourceDefault
}

// Active reports whether any setting of this decision was set by a layer above
// the default, i.e. someone chose something.
func (d Decision) Active() bool {
	if d.d == nil {
		return false
	}
	for _, fields := range d.d.fields {
		for _, st := range fields {
			if st.Source != SourceDefault {
				return true
			}
		}
	}
	return false
}

// Warnings are the problems found while resolving (invalid managed values,
// ignored project overrides). Never contains values.
func (d Decision) Warnings() []string {
	if d.d == nil {
		return nil
	}
	return slices.Clone(d.d.warnings)
}

// Record is the secret-free projection persisted in session metadata and logged:
// the rule settings and their sources, what each outcome adds, and warnings.
// Rules and seams contribute names and counts, never values of secrets.
type Record map[string]any

// RecordVersion is the version of the Record shape.
const RecordVersion = 2

// Record builds the decision record. extra carries facts a seam learned while
// applying the decision (for example the requested and effective permission
// mode), keyed by rule name.
func (d Decision) Record(extra map[string]map[string]any) Record {
	rec := Record{"v": RecordVersion}
	if d.d == nil {
		return rec
	}
	rules := map[string]any{}
	names := make([]string, 0, len(d.d.outcomes))
	for name := range d.d.outcomes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entry := map[string]any{}
		settings := map[string]json.RawMessage{}
		sources := map[string]Source{}
		for field, st := range d.d.fields[name] {
			settings[field] = st.Value
			sources[field] = st.Source
		}
		entry["settings"] = settings
		entry["sources"] = sources
		for k, v := range d.d.outcomes[name].Record() {
			entry[k] = v
		}
		for k, v := range extra[name] {
			entry[k] = v
		}
		rules[name] = entry
	}
	rec["rules"] = rules
	if d.d.profile != "" {
		rec["profile"] = d.d.profile
	}
	if len(d.d.warnings) > 0 {
		rec["warnings"] = d.d.warnings
	}
	return rec
}

// JSON is the record as JSON, for session metadata.
func (r Record) JSON() json.RawMessage {
	encoded, _ := json.Marshal(r)
	return encoded
}
