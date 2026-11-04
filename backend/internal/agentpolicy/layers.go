package agentpolicy

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Source names the layer a value came from. Highest precedence first:
// managed, env, project, user, default.
type Source string

// Layer sources.
const (
	SourceManaged Source = "managed"
	SourceEnv     Source = "env"
	SourceProject Source = "project"
	SourceUser    Source = "user"
	SourceDefault Source = "default"
	// SourceSecurityMode marks a field the Security mode floor set.
	SourceSecurityMode Source = "security_mode"
)

// values is one layer's settings: rule name -> field name -> JSON value.
type values map[string]Settings

// layer is a validated set of values from one source.
type layer struct {
	values   values
	warnings []string
	// profile is the managed profile name ("" for none); profileSet holds the
	// "rule.field" keys whose value is the profile's floor.
	profile    string
	profileSet map[string]bool
}

func (l layer) get(rule, field string) (json.RawMessage, bool) {
	v, ok := l.values[rule][field]
	return v, ok
}

// FieldState is the effective value of one field and where it came from.
type FieldState struct {
	Value  json.RawMessage
	Source Source
	// Locked is true when the managed file or the environment set the field, so
	// no lower layer can change it.
	Locked bool
}

// ruleState is the merged result for one rule.
type ruleState struct {
	settings Settings
	fields   map[string]FieldState
}

// merge applies the layers to a rule's schema in precedence order and returns
// the effective settings plus warnings. project may be empty.
func merge(rule Rule, user, project, env, managed layer) (ruleState, []string) {
	schema := rule.Schema()
	state := ruleState{settings: Settings{}, fields: map[string]FieldState{}}
	var warnings []string
	for _, f := range schema.Fields {
		cur := FieldState{Value: bytes.Clone(f.Default), Source: SourceDefault}
		if v, ok := user.get(rule.Name(), f.Name); ok {
			cur = FieldState{Value: v, Source: SourceUser}
		}
		if v, ok := project.get(rule.Name(), f.Name); ok {
			if next, warn := applyProject(f, cur, v); warn != "" {
				warnings = append(warnings, fmt.Sprintf("project override of %s.%s ignored: %s", rule.Name(), f.Name, warn))
			} else {
				cur = next
			}
		}
		if v, ok := env.get(rule.Name(), f.Name); ok {
			cur = FieldState{Value: v, Source: SourceEnv, Locked: true}
		}
		if v, ok := managed.get(rule.Name(), f.Name); ok {
			cur = FieldState{Value: v, Source: SourceManaged, Locked: true}
			if managed.profileSet[rule.Name()+"."+f.Name] {
				cur.Source = SourceManagedProfile
			}
		}
		state.fields[f.Name] = cur
		state.settings[f.Name] = cur.Value
	}
	return state, warnings
}

// applyProject decides whether a project override takes effect over cur. A
// non-empty warning means it does not.
func applyProject(f Field, cur FieldState, v json.RawMessage) (FieldState, string) {
	switch f.ProjectOverride {
	case ProjectAny:
		return FieldState{Value: v, Source: SourceProject}, ""
	case ProjectTightenOnly:
		if f.strictness(v) >= f.strictness(cur.Value) {
			return FieldState{Value: v, Source: SourceProject}, ""
		}
		return cur, "it would loosen the user's setting"
	default:
		return cur, "this setting is not project-overridable"
	}
}
