package settingsreg

import (
	"fmt"
	"slices"
	"sync"

	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules"
)

// Registry is an ordered, validated set of settings.
type Registry struct {
	order []Setting
	byKey map[Key]int
}

// New validates and indexes settings; keys must be unique.
func New(settings ...Setting) (*Registry, error) {
	r := &Registry{byKey: make(map[Key]int, len(settings))}
	for _, s := range settings {
		if err := s.check(); err != nil {
			return nil, err
		}
		if _, dup := r.byKey[s.Key]; dup {
			return nil, fmt.Errorf("setting %q is declared twice", s.Key)
		}
		r.byKey[s.Key] = len(r.order)
		r.order = append(r.order, s)
	}
	return r, nil
}

// Lookup returns the setting with key k.
func (r *Registry) Lookup(k Key) (Setting, bool) {
	i, ok := r.byKey[k]
	if !ok {
		return Setting{}, false
	}
	return r.order[i], true
}

// All returns every setting in declaration order.
func (r *Registry) All() []Setting { return slices.Clone(r.order) }

// Scope returns the settings of one scope in declaration order.
func (r *Registry) Scope(scope Scope) []Setting {
	var out []Setting
	for _, s := range r.order {
		if s.Scope == scope {
			out = append(out, s)
		}
	}
	return out
}

var (
	defaultOnce sync.Once
	defaultReg  *Registry
)

// Default is the registry of this build: app, project and cleanup settings plus
// every agent-policy field. It panics on an invalid declaration, which the
// package tests catch.
func Default() *Registry {
	defaultOnce.Do(func() {
		all := append(appSettings(), cleanupSettings()...)
		all = append(all, projectSettings()...)
		all = append(all, policySettings(rules.All())...)
		reg, err := New(all...)
		if err != nil {
			panic("settingsreg: " + err.Error())
		}
		defaultReg = reg
	})
	return defaultReg
}
