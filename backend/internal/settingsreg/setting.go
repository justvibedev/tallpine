// Package settingsreg is the settings registry: one declaration per setting,
// app-wide and per project, with its type, default, scope, the environment
// variables that override it, whether a managed (MDM) policy may lock it, and
// who may change it. It is the single source of truth that the API, CLI,
// Settings UI and managed-policy enforcement are migrated onto (B202); this
// first step declares every setting and describes today's behaviour without
// changing any read or write path.
package settingsreg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
)

// Key is the stable, dotted name of a setting: "app.versionRefresh",
// "project.agentRules", "policy.permissionCap.max".
type Key string

// Scope says where a setting's value lives.
type Scope string

// Scopes.
const (
	// ScopeApp is one value for the whole install.
	ScopeApp Scope = "app"
	// ScopeProject is one value per project.
	ScopeProject Scope = "project"
	// ScopePolicy is an agent-policy field: a user (app) value that a project
	// may override as its rule allows, under env and managed layers.
	ScopePolicy Scope = "policy"
)

// Kind is the value type of a setting.
type Kind string

// Kinds.
const (
	KindBool       Kind = "bool"
	KindInt        Kind = "int"
	KindEnum       Kind = "enum"
	KindString     Kind = "string"
	KindText       Kind = "text"
	KindStringList Kind = "stringList"
	KindStringMap  Kind = "stringMap"
	// KindObject is a structured value validated by its owner (for now the
	// project config or cleanup policy validators).
	KindObject Kind = "object"
)

// Actor is a kind of principal that may change a setting.
type Actor string

// Actors.
const (
	ActorUser         Actor = "user"
	ActorOrchestrator Actor = "orchestrator"
	ActorWorker       Actor = "worker"
	// ActorCreatorOrchestrator is the orchestrator of the project that
	// registered this project, while that project's crossProjectOrchestration
	// policy is on (PUT /projects/{id}/config, checkCreatedProject). For object
	// settings it may set only the nested fields the httpd agentConfigKeys
	// allowlist names (agent/harness, model, effort, permissions).
	ActorCreatorOrchestrator Actor = "creatorOrchestrator"
)

// Setting declares one setting.
type Setting struct {
	Key   Key
	Scope Scope
	Kind  Kind
	// Default is the JSON value of an untouched install.
	Default json.RawMessage
	// Enum lists the allowed values of an enum setting, ascending strictness
	// for policy keys.
	Enum []string
	// Min and Max bound an int setting when set.
	Min, Max *int64
	// Nullable settings accept JSON null, meaning "unset" (project fields,
	// which the config omits when empty). Others reject null.
	Nullable bool
	// MaxBytes bounds a string or text setting when set.
	MaxBytes int
	// Validate is the owner's check of a value, run after the kind check; it
	// delegates to the same code the owner's write path uses.
	Validate func(json.RawMessage) error
	// Description is a one-line English summary for the CLI and docs.
	Description string
	// Env lists the environment variables that can decide the value.
	Env []string
	// EnvOverride maps the text of one of Env to the value it forces, if it
	// forces one; a forced value locks the setting. Nil means the variables
	// are read by the setting's owner (agent policy) or never force it.
	EnvOverride func(text string) (json.RawMessage, bool)
	// MDMLockable says a managed policy file may set and lock the value.
	MDMLockable bool
	// Sensitive values (they may hold secrets) are redacted in the settings
	// change log.
	Sensitive bool
	// Writers are the actors that may change the value when nothing locks it.
	Writers []Actor
}

// CanWrite reports whether a declares a as a writer.
func (s Setting) CanWrite(a Actor) bool { return slices.Contains(s.Writers, a) }

var keyPattern = regexp.MustCompile(`^(app|project|policy)(\.[a-zA-Z][a-zA-Z0-9]*)+$`)

// check validates the declaration itself.
func (s Setting) check() error {
	if !keyPattern.MatchString(string(s.Key)) {
		return fmt.Errorf("setting %q: key must be dotted camelCase under app., project. or policy", s.Key)
	}
	if prefix := string(s.Scope) + "."; len(s.Key) <= len(prefix) || string(s.Key[:len(prefix)]) != prefix {
		return fmt.Errorf("setting %q: key must start with its scope %q", s.Key, s.Scope)
	}
	if s.Description == "" {
		return fmt.Errorf("setting %q: description is required", s.Key)
	}
	if len(s.Writers) == 0 || !s.CanWrite(ActorUser) {
		return fmt.Errorf("setting %q: the user must be a writer", s.Key)
	}
	if !json.Valid(s.Default) {
		return fmt.Errorf("setting %q: default is not valid JSON", s.Key)
	}
	switch s.Kind {
	case KindEnum:
		var v string
		if len(s.Enum) == 0 || json.Unmarshal(s.Default, &v) != nil || !slices.Contains(s.Enum, v) {
			return fmt.Errorf("setting %q: enum default must be one of its values", s.Key)
		}
	case KindBool:
		var v bool
		if json.Unmarshal(s.Default, &v) != nil {
			return fmt.Errorf("setting %q: default must be a bool", s.Key)
		}
	case KindInt:
		var v int64
		if json.Unmarshal(s.Default, &v) != nil {
			return fmt.Errorf("setting %q: default must be a whole number", s.Key)
		}
		if (s.Min != nil && v < *s.Min) || (s.Max != nil && v > *s.Max) {
			return fmt.Errorf("setting %q: default %d is outside its bounds", s.Key, v)
		}
	case KindString, KindText:
		var v string
		if json.Unmarshal(s.Default, &v) != nil {
			return fmt.Errorf("setting %q: default must be a string", s.Key)
		}
	case KindStringList, KindStringMap, KindObject:
	default:
		return fmt.Errorf("setting %q: unknown kind %q", s.Key, s.Kind)
	}
	return nil
}

// jsonOf encodes v as a default; it panics on a programming error.
func jsonOf(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return bytes.TrimSpace(b)
}

func bound(n int64) *int64 { return &n }
