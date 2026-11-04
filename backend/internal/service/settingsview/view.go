// Package settingsview reports the effective value of every registered setting
// (B202): its value, the layer it came from, whether managed config or the
// environment locks it, and which actors the registry lets change it. It reads
// each owner (app settings, cleanup policy, agent policy, project config)
// without changing any of them.
package settingsview

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/managedcfg"
	"tallpine.eu/tallpine/backend/internal/service/cleanup"
	projectsvc "tallpine.eu/tallpine/backend/internal/service/project"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
	"tallpine.eu/tallpine/backend/internal/service/settingsaudit"
	"tallpine.eu/tallpine/backend/internal/settingsreg"
)

// Sources of an effective value.
const (
	SourceDefault = "default"
	SourceUser    = "user"
	SourceProject = "project"
	SourceEnv     = "env"
	SourceManaged = "managed"
)

// Value is the effective state of one setting.
type Value struct {
	Key    settingsreg.Key
	Value  json.RawMessage
	Source string
	// Locked is true when managed config or an environment variable decides
	// the value; LockedBy names which ("managed" or the variable).
	Locked   bool
	LockedBy string
	// Writable lists the actors the registry lets change the value now.
	Writable []settingsreg.Actor
}

// AppSettings reads app_settings. A nil owner leaves its settings out.
type AppSettings interface {
	Get(ctx context.Context) (settingssvc.Snapshot, error)
}

// CleanupPolicy reads the storage cleanup policy.
type CleanupPolicy interface {
	Status(ctx context.Context) (cleanup.Status, error)
}

// AgentPolicy describes the agent policy install-wide or for a project.
type AgentPolicy interface {
	Describe(ctx context.Context, project string) agentpolicy.View
}

// Projects reads a project's config.
type Projects interface {
	Get(ctx context.Context, id domain.ProjectID) (projectsvc.GetResult, error)
}

// Service builds effective values.
type Service struct {
	Registry      *settingsreg.Registry
	AppSettings   AppSettings
	CleanupPolicy CleanupPolicy
	AgentPolicy   AgentPolicy
	Projects      Projects
	// LookupEnv reads the daemon environment; nil means os.LookupEnv is not
	// consulted (tests).
	LookupEnv func(string) (string, bool)
	// Managed holds the values the managed file sets and locks (nil: none).
	Managed *managedcfg.Locks
}

// App returns the app-wide settings (app.*) and the agent policy as it
// applies install-wide (policy.*), in registry order.
func (s *Service) App(ctx context.Context) ([]Value, error) {
	known := map[settingsreg.Key]json.RawMessage{}
	if s.AppSettings != nil {
		snapshot, err := s.AppSettings.Get(ctx)
		if err != nil {
			return nil, err
		}
		for k, v := range settingsaudit.AppSettings(snapshot) {
			known[k] = v
		}
	}
	if s.CleanupPolicy != nil {
		status, err := s.CleanupPolicy.Status(ctx)
		if err != nil {
			return nil, err
		}
		for k, v := range settingsaudit.JSONObject("app.cleanup.", status.Policy) {
			known[k] = v
		}
	}
	var out []Value
	for _, def := range s.registry().Scope(settingsreg.ScopeApp) {
		v, ok := known[def.Key]
		if !ok {
			continue
		}
		out = append(out, s.stored(def, v, SourceUser))
	}
	return append(out, s.policy(ctx, "")...), nil
}

// Project returns a project's settings (project.*) and the agent policy as it
// applies to that project (policy.*), in registry order.
func (s *Service) Project(ctx context.Context, id domain.ProjectID) ([]Value, error) {
	var out []Value
	if s.Projects != nil {
		res, err := s.Projects.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		var cfg domain.ProjectConfig
		if res.Project != nil && res.Project.Config != nil {
			cfg = *res.Project.Config
		}
		doc, err := json.Marshal(cfg)
		if err != nil {
			return nil, err
		}
		for _, def := range s.registry().Scope(settingsreg.ScopeProject) {
			name := strings.TrimPrefix(string(def.Key), "project.")
			if v, ok := lookupPath(doc, strings.Split(name, ".")); ok && !emptyJSON(v) {
				out = append(out, s.stored(def, v, SourceProject))
				continue
			}
			out = append(out, s.stored(def, nil, SourceProject))
		}
	}
	return append(out, s.policy(ctx, string(id))...), nil
}

// stored builds the value of a setting read from its owner: the default when
// unset or equal to the default, otherwise from source. An environment
// variable locks it only when the setting's EnvOverride says the variable
// forces a value, and then that forced value is the effective one.
func (s *Service) stored(def settingsreg.Setting, v json.RawMessage, source string) Value {
	out := Value{Key: def.Key, Value: v, Source: source}
	if len(v) == 0 || bytes.Equal(v, []byte("null")) || equalJSON(v, def.Default) {
		out.Value, out.Source = def.Default, SourceDefault
	}
	if s.LookupEnv != nil && def.EnvOverride != nil {
		for _, name := range def.Env {
			text, set := s.LookupEnv(name)
			if !set {
				continue
			}
			if forced, ok := def.EnvOverride(text); ok {
				out.Value, out.Source, out.Locked, out.LockedBy = forced, SourceEnv, true, name
				break
			}
		}
	}
	if managed, locked := s.managedValue(def); locked {
		if managed != nil {
			out.Value = managed
		}
		out.Source, out.Locked, out.LockedBy = SourceManaged, true, SourceManaged
	}
	out.Writable = writable(def, out.Locked)
	return out
}

// managedValue reports whether the managed file locks def, directly or through
// a locked ancestor key (project.orchestrator locks
// project.orchestrator.autoCompactWindow), and the managed value to show.
func (s *Service) managedValue(def settingsreg.Setting) (json.RawMessage, bool) {
	if v, ok := s.Managed.Value(string(def.Key)); ok && def.MDMLockable {
		return v, true
	}
	key := string(def.Key)
	for i := strings.LastIndex(key, "."); i > 0; i = strings.LastIndex(key[:i], ".") {
		parent, ok := s.registry().Lookup(settingsreg.Key(key[:i]))
		if !ok || !parent.MDMLockable {
			continue
		}
		v, locked := s.Managed.Value(key[:i])
		if !locked {
			continue
		}
		if v != nil {
			if nested, found := lookupPath(v, strings.Split(key[i+1:], ".")); found {
				return nested, true
			}
			return nil, true
		}
		return nil, true
	}
	return nil, false
}

// policy maps the agent-policy view onto policy.* keys.
func (s *Service) policy(ctx context.Context, project string) []Value {
	if s.AgentPolicy == nil {
		return nil
	}
	view := s.AgentPolicy.Describe(ctx, project)
	var out []Value
	for _, rule := range view.Rules {
		for _, f := range rule.Fields {
			def, ok := s.registry().Lookup(settingsreg.Key("policy." + rule.Name + "." + f.Name))
			if !ok {
				continue
			}
			v := Value{Key: def.Key, Value: f.Value, Source: string(f.Source), Locked: f.Locked}
			if f.Locked {
				v.LockedBy = string(f.Source)
				// A hardened-profile floor is locked by the managed file too.
				if f.Source == agentpolicy.SourceManagedProfile {
					v.LockedBy = SourceManaged
				}
			}
			v.Writable = writable(def, v.Locked)
			out = append(out, v)
		}
	}
	return out
}

func (s *Service) registry() *settingsreg.Registry {
	if s.Registry != nil {
		return s.Registry
	}
	return settingsreg.Default()
}

// writable lists the registry's writers unless the value is locked.
func writable(def settingsreg.Setting, locked bool) []settingsreg.Actor {
	if locked {
		return []settingsreg.Actor{}
	}
	return append([]settingsreg.Actor{}, def.Writers...)
}

// lookupPath reads a nested JSON field.
func lookupPath(doc json.RawMessage, path []string) (json.RawMessage, bool) {
	for _, name := range path {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(doc, &fields); err != nil {
			return nil, false
		}
		next, ok := fields[name]
		if !ok {
			return nil, false
		}
		doc = next
	}
	return doc, true
}

func equalJSON(a, b json.RawMessage) bool {
	var ca, cb bytes.Buffer
	if json.Compact(&ca, a) != nil || json.Compact(&cb, b) != nil {
		return false
	}
	return bytes.Equal(ca.Bytes(), cb.Bytes())
}

// emptyJSON reports whether every leaf of v is empty (null, "", false, 0, {} or
// []): a struct-typed project field that was never set marshals that way.
func emptyJSON(v json.RawMessage) bool {
	var x any
	if json.Unmarshal(v, &x) != nil {
		return false
	}
	return emptyValue(x)
}

func emptyValue(x any) bool {
	switch t := x.(type) {
	case nil:
		return true
	case bool:
		return !t
	case float64:
		return t == 0
	case string:
		return t == ""
	case []any:
		return len(t) == 0
	case map[string]any:
		for _, v := range t {
			if !emptyValue(v) {
				return false
			}
		}
		return true
	}
	return false
}
