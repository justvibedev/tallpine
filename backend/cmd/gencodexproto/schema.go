package main

import (
	"encoding/json"
)

// schema is the subset of draft-07 the Codex protocol actually uses. Verified by
// walking every published file: no patternProperties, no const, no if/then.
type schema struct {
	Ref                  string             `json:"$ref"`
	Type                 json.RawMessage    `json:"type"`
	Enum                 []any              `json:"enum"`
	Properties           map[string]*schema `json:"properties"`
	Required             []string           `json:"required"`
	Items                *schema            `json:"items"`
	AdditionalProperties json.RawMessage    `json:"additionalProperties"`
	OneOf                []*schema          `json:"oneOf"`
	AnyOf                []*schema          `json:"anyOf"`
	AllOf                []*schema          `json:"allOf"`
	Title                string             `json:"title"`
	Description          string             `json:"description"`
	Format               string             `json:"format"`
	Definitions          map[string]*schema `json:"definitions"`

	// permissive marks a schema written as the bare literal `true` or `false`.
	permissive bool
}

// UnmarshalJSON accepts draft-07's boolean schemas. `true` means "any value" and
// `false` means "no value"; both render as raw JSON, which is the only honest Go
// type for a payload the schema declines to describe.
func (s *schema) UnmarshalJSON(data []byte) error {
	var anything bool
	if err := json.Unmarshal(data, &anything); err == nil {
		*s = schema{permissive: true}
		return nil
	}
	type plain schema // no UnmarshalJSON, so this does not recurse
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*s = schema(p)
	return nil
}

// types reports the declared JSON types, which the schema writes either as a
// string or as a list including "null" for an optional value.
func (s *schema) types() []string {
	if len(s.Type) == 0 {
		return nil
	}
	var one string
	if err := json.Unmarshal(s.Type, &one); err == nil {
		return []string{one}
	}
	var many []string
	if err := json.Unmarshal(s.Type, &many); err == nil {
		return many
	}
	return nil
}

// nullable reports whether the value may be absent, which decides whether the
// Go field is a pointer.
func (s *schema) nullable() bool {
	for _, t := range s.types() {
		if t == "null" {
			return true
		}
	}
	// anyOf: [{...}, {"type": "null"}] is how the schema spells an optional $ref.
	for _, v := range s.AnyOf {
		for _, t := range v.types() {
			if t == "null" {
				return true
			}
		}
	}
	return false
}

// base strips the null arm so the remaining schema describes the value itself.
func (s *schema) base() *schema {
	if len(s.AnyOf) > 0 {
		var concrete []*schema
		for _, v := range s.AnyOf {
			if len(v.types()) == 1 && v.types()[0] == "null" {
				continue
			}
			concrete = append(concrete, v)
		}
		if len(concrete) == 1 {
			return concrete[0]
		}
	}
	// allOf with a single arm is a wrapper the schema uses to attach a description
	// to a $ref.
	if len(s.AllOf) == 1 && s.Ref == "" && len(s.Properties) == 0 {
		return s.AllOf[0]
	}
	return s
}

/* ---- method table ------------------------------------------------------- */

type method struct {
	Name      string // wire method, e.g. "thread/start"
	Direction string // ClientRequest | ClientNotification | ServerRequest | ServerNotification
	Params    string // Go type of params, empty when the method carries none
	Result    string // Go type of the result, empty for notifications
	Doc       string
}
