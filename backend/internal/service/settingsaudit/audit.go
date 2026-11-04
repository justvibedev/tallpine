// Package settingsaudit records every settings change with its actor in the
// settings change log (B202). Callers flatten the owner's state before and after
// a write into registry keys; the recorder stores one row per changed key and
// logs it. Recording never fails the write it describes: the setting is already
// saved, so a log failure is reported in the daemon log instead.
package settingsaudit

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	"tallpine.eu/tallpine/backend/internal/settingsreg"
)

// Redacted replaces the value of a sensitive setting in the log.
const Redacted = `"<redacted>"`

// Values are settings by registry key, as JSON.
type Values map[settingsreg.Key]json.RawMessage

// Store persists change rows.
type Store interface {
	AppendSettingsChanges(ctx context.Context, changes []domain.SettingsChange) error
}

// Recorder writes the settings change log. A nil Recorder records nothing.
type Recorder struct {
	store Store
	log   *slog.Logger
	reg   *settingsreg.Registry
	now   func() time.Time
}

// New returns a recorder over store.
func New(store Store, log *slog.Logger) *Recorder {
	if log == nil {
		log = slog.Default()
	}
	return &Recorder{store: store, log: log, reg: settingsreg.Default(), now: time.Now}
}

// ActorFrom names who is making the request: "user", or "<role>:<session>" for
// a scoped agent token.
func ActorFrom(ctx context.Context) string {
	if agent, ok := requestscope.AgentFrom(ctx); ok {
		return string(agent.Role) + ":" + string(agent.SessionID)
	}
	return "user"
}

// Diff returns the changed keys between before and after, in key order, with
// sensitive values redacted. A key missing on one side counts as null.
func Diff(reg *settingsreg.Registry, project domain.ProjectID, before, after Values) []domain.SettingsChange {
	keys := make([]settingsreg.Key, 0, len(before)+len(after))
	for k := range before {
		keys = append(keys, k)
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	var out []domain.SettingsChange
	for _, k := range keys {
		oldValue, newValue := canonical(before[k]), canonical(after[k])
		if oldValue == newValue {
			continue
		}
		if s, ok := reg.Lookup(k); ok && s.Sensitive {
			oldValue, newValue = Redacted, Redacted
		}
		out = append(out, domain.SettingsChange{ProjectID: project, Key: string(k), OldValue: oldValue, NewValue: newValue})
	}
	return out
}

func canonical(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return "null"
	}
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return string(raw)
	}
	return b.String()
}

// Record stores and logs the changes between before and after, made by the
// request's actor. project is empty for app-wide settings.
func (r *Recorder) Record(ctx context.Context, project domain.ProjectID, before, after Values) {
	if r == nil {
		return
	}
	changes := Diff(r.reg, project, before, after)
	if len(changes) == 0 {
		return
	}
	actor, at := ActorFrom(ctx), r.now().UTC()
	for i := range changes {
		changes[i].Actor, changes[i].ChangedAt = actor, at
		r.log.Info("setting changed", "actor", actor, "project", string(project), "key", changes[i].Key)
	}
	// The request may be cancelled right after its write; keep the record.
	if err := r.store.AppendSettingsChanges(context.WithoutCancel(ctx), changes); err != nil {
		r.log.Error("settings change log write failed", "actor", actor, "project", string(project), "changes", len(changes), "err", err)
	}
}
