package main

import (
	"encoding/json"
	"sort"
)

// goType renders the Go type for a schema. `owner` and `field` name inline
// object types, which the schema declares anonymously inside properties.
func (g *generator) goType(s *schema, owner, field string, inline *[]inlineType) string {
	s = s.base()

	if s.Ref != "" {
		return refName(s.Ref)
	}
	if len(s.AllOf) == 1 {
		return g.goType(s.AllOf[0], owner, field, inline)
	}

	// A string enum inline in a property: the schema names most of these through a
	// $ref, so what is left is a one-off. Rendering it as a plain string keeps the
	// generated surface small without losing any wire information.
	if len(s.Enum) > 0 {
		return "string"
	}

	ts := s.types()
	concrete := ""
	for _, t := range ts {
		if t != "null" {
			concrete = t
			break
		}
	}

	switch concrete {
	case "string":
		return "string"
	case "boolean":
		return "bool"
	case "integer":
		if s.Format == "uint64" || s.Format == "uint32" {
			return "uint64"
		}
		return "int64"
	case "number":
		return "float64"
	case "array":
		if s.Items == nil {
			return "[]json.RawMessage"
		}
		return "[]" + g.goType(s.Items, owner, field, inline)
	case "object":
		// A map when the schema constrains additional properties and declares no
		// fixed fields; a named struct otherwise.
		if len(s.Properties) == 0 && len(s.AdditionalProperties) > 0 {
			var sub schema
			if err := json.Unmarshal(s.AdditionalProperties, &sub); err == nil && len(s.AdditionalProperties) > 0 {
				if string(s.AdditionalProperties) != "true" && string(s.AdditionalProperties) != "false" {
					return "map[string]" + g.goType(&sub, owner, field, inline)
				}
			}
			return "map[string]json.RawMessage"
		}
		if len(s.Properties) == 0 {
			return "json.RawMessage"
		}
		name := exportName(owner + "_" + field)
		*inline = append(*inline, inlineType{name: name, s: s})
		return name
	}

	// oneOf/anyOf that survived: a union the generator will not guess at. Left as
	// raw JSON so the caller decides, rather than picking an arm and being wrong.
	if len(s.OneOf) > 0 || len(s.AnyOf) > 0 {
		if enumUnion(s) {
			return "string"
		}
		return "json.RawMessage"
	}
	return "json.RawMessage"
}

type inlineType struct {
	name string
	s    *schema
}

// enumUnion reports a oneOf whose every arm is a documented string enum — the
// schema's way of writing a Rust enum of unit variants.
func enumUnion(s *schema) bool {
	arms := s.OneOf
	if len(arms) == 0 {
		arms = s.AnyOf
	}
	if len(arms) == 0 {
		return false
	}
	for _, a := range arms {
		if len(a.Enum) == 0 {
			return false
		}
		for _, t := range a.types() {
			if t != "string" && t != "null" {
				return false
			}
		}
	}
	return true
}

// taggedUnion reports the discriminator shared by every arm of a oneOf, if there
// is one. Codex uses this for its Rust enums with struct variants.
func taggedUnion(s *schema) (string, bool) {
	arms := s.OneOf
	if len(arms) < 2 {
		return "", false
	}
	var common map[string]bool
	for _, a := range arms {
		if a.Properties == nil {
			return "", false
		}
		here := map[string]bool{}
		for name, p := range a.Properties {
			if len(p.Enum) == 1 {
				here[name] = true
			}
		}
		if len(here) == 0 {
			return "", false
		}
		if common == nil {
			common = here
			continue
		}
		for name := range common {
			if !here[name] {
				delete(common, name)
			}
		}
	}
	keys := make([]string, 0, len(common))
	for name := range common {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return "", false
	}
	return keys[0], true
}

/* ---- rendering ---------------------------------------------------------- */
