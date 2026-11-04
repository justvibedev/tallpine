// Package sessionretention is the retention policy for ended sessions (B161):
// how long a terminated session keeps its raw provider events, its worktree
// and, optionally, its transcript. It is on by default (B201); a user opts out, or a
// managed policy file sets it and locks it. The cleanup engine's
// session_retention rule reads it per project and does the purging.
//
// What is never purged, whatever the settings: the session row, its
// conversations, turns, usage and model usage, 'system' activities
// (compaction markers), reviews and PRs. Live sessions are never touched;
// orchestrator sessions only with includeOrchestrators, and the orchestrator's
// shared worktree never.
package sessionretention

import (
	"encoding/json"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
)

// Name is the rule's key in settings documents and the managed policy file.
const Name = "sessionRetention"

// Field names.
const (
	FieldEnabled = "enabled"
	FieldDays    = "days"
	// FieldDropProviderEvents is deprecated and ignored by the retention sweep; it stays
	// readable so old configs and managed files keep loading. Provider events follow
	// app.providerEventRetentionDays.
	FieldDropProviderEvents = "dropProviderEvents"
	FieldDropWorktrees      = "dropWorktrees"
	FieldDropTranscripts    = "dropTranscripts"
	// FieldIncludeOrchestrators opts orchestrator sessions in. Off: a fresh
	// orchestrator chat reads its predecessors' history, so it is kept.
	FieldIncludeOrchestrators = "includeOrchestrators"
)

// Bounds of the age threshold, in days.
const (
	MinDays     = 1
	MaxDays     = 3650
	DefaultDays = 7
)

// Rule implements agentpolicy.Rule.
type Rule struct{}

// Name implements agentpolicy.Rule.
func (Rule) Name() string { return Name }

// Schema implements agentpolicy.Rule.
func (Rule) Schema() agentpolicy.Schema {
	whenOn := &agentpolicy.Condition{Field: FieldEnabled, Equals: "true"}
	field := func(name, kind string, def string) agentpolicy.Field {
		f := agentpolicy.Field{
			Name:            name,
			Kind:            agentpolicy.FieldKind(kind),
			Default:         json.RawMessage(def),
			LabelKey:        "settings.agentPolicy.sessionRetention." + name + ".label",
			DescriptionKey:  "settings.agentPolicy.sessionRetention." + name + ".description",
			ProjectOverride: agentpolicy.ProjectAny,
		}
		if name != FieldEnabled {
			f.VisibleWhen = whenOn
		}
		return f
	}
	return agentpolicy.Schema{
		TitleKey: "settings.agentPolicy.title",
		Fields: []agentpolicy.Field{
			field(FieldEnabled, string(agentpolicy.KindBool), `true`),
			field(FieldDays, string(agentpolicy.KindInt), fmt.Sprint(DefaultDays)),
			field(FieldDropProviderEvents, string(agentpolicy.KindBool), `true`),
			field(FieldDropWorktrees, string(agentpolicy.KindBool), `true`),
			field(FieldDropTranscripts, string(agentpolicy.KindBool), `false`),
			field(FieldIncludeOrchestrators, string(agentpolicy.KindBool), `false`),
		},
	}
}

// Validate implements agentpolicy.Rule.
func (Rule) Validate(s agentpolicy.Settings) error {
	if _, ok := s[FieldDays]; !ok {
		return nil
	}
	if d := s.Int(FieldDays); d < MinDays || d > MaxDays {
		return fmt.Errorf("%s must be %d-%d", FieldDays, MinDays, MaxDays)
	}
	return nil
}

// Decide implements agentpolicy.Rule.
func (Rule) Decide(_ agentpolicy.Input, s agentpolicy.Settings) agentpolicy.Outcome {
	return FromSettings(s)
}

// Outcome is the effective retention policy. The zero Outcome is off.
type Outcome struct {
	Enabled            bool
	Days               int
	DropProviderEvents bool
	DropWorktrees      bool
	DropTranscripts    bool
	// IncludeOrchestrators purges ended orchestrator sessions too. Their
	// shared worktree is never removed either way.
	IncludeOrchestrators bool
}

// Record implements agentpolicy.Outcome; the settings are already in the
// decision record.
func (Outcome) Record() map[string]any { return nil }

// FromSettings reads resolved settings. A days value outside the bounds (only
// possible from a hand-edited document) is clamped rather than purging early.
func FromSettings(s agentpolicy.Settings) Outcome {
	days := s.Int(FieldDays)
	if days < MinDays {
		days = DefaultDays
	}
	if days > MaxDays {
		days = MaxDays
	}
	return Outcome{
		Enabled:              s.Bool(FieldEnabled),
		Days:                 days,
		DropProviderEvents:   s.Bool(FieldDropProviderEvents),
		DropWorktrees:        s.Bool(FieldDropWorktrees),
		DropTranscripts:      s.Bool(FieldDropTranscripts),
		IncludeOrchestrators: s.Bool(FieldIncludeOrchestrators),
	}
}

// Of returns the outcome of a decision; the zero Decision is off.
func Of(d agentpolicy.Decision) Outcome {
	if out, ok := d.Outcome(Name); ok {
		if typed, ok := out.(Outcome); ok {
			return typed
		}
	}
	return Outcome{}
}
