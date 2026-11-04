package main

import (
	"fmt"
	"sort"
	"strings"
)

func (g *generator) renderDef(b *strings.Builder, name string, s *schema, inline *[]inlineType, emitted map[string]bool) {
	if emitted[name] {
		return
	}

	// A named string enum: a Go string type plus its values, so an invalid value
	// cannot be constructed by accident.
	if len(s.Enum) > 0 && isStringEnum(s) {
		emitted[name] = true
		doc(b, name, s.Description)
		fmt.Fprintf(b, "type %s string\n\nconst (\n", name)
		for _, v := range s.Enum {
			str, ok := v.(string)
			if !ok {
				continue
			}
			fmt.Fprintf(b, "\t%s%s %s = %q\n", name, exportName(str), name, str)
		}
		b.WriteString(")\n\n")
		return
	}

	if enumUnion(s) {
		emitted[name] = true
		doc(b, name, s.Description)
		fmt.Fprintf(b, "type %s string\n\nconst (\n", name)
		arms := s.OneOf
		if len(arms) == 0 {
			arms = s.AnyOf
		}
		seen := map[string]bool{}
		for _, a := range arms {
			for _, v := range a.Enum {
				str, ok := v.(string)
				if !ok || seen[str] {
					continue
				}
				seen[str] = true
				if a.Description != "" {
					fmt.Fprintf(b, "\t// %s\n", oneLine(a.Description))
				}
				fmt.Fprintf(b, "\t%s%s %s = %q\n", name, exportName(str), name, str)
			}
		}
		b.WriteString(")\n\n")
		return
	}

	// A tagged union flattens to one struct with the discriminator typed and every
	// arm's fields optional. This is how the driver already reads thread items:
	// switch on the tag, then read the fields that tag implies. A Go sum type
	// would be more precise and much less usable at a call site.
	if tag, ok := taggedUnion(s); ok {
		emitted[name] = true
		g.renderFlattenedUnion(b, name, s, tag, inline, emitted)
		return
	}

	if len(s.Properties) > 0 {
		g.renderStruct(b, name, s, inline, emitted)
		return
	}

	// Everything else is an alias: a scalar, an array, a map, or a union the
	// generator declines to guess at.
	emitted[name] = true
	doc(b, name, s.Description)
	target := g.goType(s, name, "", inline)

	// A DEFINED type over json.RawMessage does not inherit its UnmarshalJSON, so
	// encoding/json falls back to treating it as []byte and demands a base64
	// string. Every numeric value then fails to decode — silently, if the caller
	// ignores the error. That cost us every `serverRequest/resolved` notification,
	// because request ids arrive as numbers. A type ALIAS keeps the methods, so
	// callers cannot be trapped by a name that looks decodable and is not.
	if target == "json.RawMessage" {
		fmt.Fprintf(b, "type %s = %s\n\n", name, target)
		return
	}
	fmt.Fprintf(b, "type %s %s\n\n", name, target)
}

func (g *generator) renderStruct(b *strings.Builder, name string, s *schema, inline *[]inlineType, emitted map[string]bool) {
	if emitted[name] {
		return
	}
	emitted[name] = true

	required := map[string]bool{}
	for _, r := range s.Required {
		required[r] = true
	}

	fields := make([]string, 0, len(s.Properties))
	for f := range s.Properties {
		fields = append(fields, f)
	}
	sort.Strings(fields)

	doc(b, name, s.Description)
	fmt.Fprintf(b, "type %s struct {\n", name)
	used := map[string]bool{}
	for _, f := range fields {
		p := s.Properties[f]
		g.renderField(b, name, f, p, required[f], used, inline)
	}
	b.WriteString("}\n\n")
}

func (g *generator) renderField(
	b *strings.Builder, owner, jsonName string, p *schema,
	required bool, used map[string]bool, inline *[]inlineType,
) {
	goName := exportName(jsonName)
	if goName == "" {
		return
	}
	// Two JSON names can normalize to one Go name (`type` and `Type`); keep the
	// first and leave the second reachable only as raw JSON rather than emitting
	// a struct that does not compile.
	if used[goName] {
		goName += "_"
	}
	used[goName] = true

	typ := g.goType(p, owner, jsonName, inline)
	tag := jsonName
	optional := !required || p.nullable()
	if optional {
		tag += ",omitempty"
	}
	// Pointers only where zero and absent differ and the caller would care: a
	// missing exit code is not exit code 0, but an absent slice and an empty one
	// are the same thing to every reader.
	if optional && needsPointer(typ) {
		typ = "*" + typ
	}
	if p.Description != "" {
		fmt.Fprintf(b, "\t// %s\n", oneLine(p.Description))
	}
	fmt.Fprintf(b, "\t%s %s `json:%q`\n", goName, typ, tag)
}

// widen finds a single Go type for two arms of a tagged union that name the same
// field differently. Both must reduce to the same JSON kind, or the result is raw
// JSON — a wrong type here would decode to a zero value silently, which is the
// exact failure this generator exists to prevent.
func (g *generator) widen(a, b string) string {
	ka, kb := g.jsonKind(a), g.jsonKind(b)
	if ka != kb || ka == "" {
		return "json.RawMessage"
	}
	switch ka {
	case "string":
		return "string"
	case "integer":
		return "int64"
	case "number":
		return "float64"
	case "boolean":
		return "bool"
	default:
		return "json.RawMessage"
	}
}

// jsonKind reduces a generated Go type to the JSON kind it encodes, resolving
// named types through the schema that produced them.
func (g *generator) jsonKind(typ string) string {
	typ = strings.TrimPrefix(typ, "*")
	switch typ {
	case "string":
		return "string"
	case "int64", "uint64":
		return "integer"
	case "float64":
		return "number"
	case "bool":
		return "boolean"
	case "json.RawMessage":
		return ""
	}
	if strings.HasPrefix(typ, "[]") || strings.HasPrefix(typ, "map[") {
		return ""
	}
	// A named definition: resolve one level. Every named type the protocol uses in
	// a conflicting position is a string enum or a scalar alias.
	for name, def := range g.defs {
		if exportName(name) != typ {
			continue
		}
		if isStringEnum(def) || enumUnion(def) {
			return "string"
		}
		base := def.base()
		for _, t := range base.types() {
			switch t {
			case "string", "integer", "number", "boolean":
				return t
			}
		}
		if len(base.AllOf) == 1 {
			return g.jsonKind(refName(base.AllOf[0].Ref))
		}
		return ""
	}
	return ""
}

func needsPointer(typ string) bool {
	switch {
	case strings.HasPrefix(typ, "[]"), strings.HasPrefix(typ, "map["):
		return false
	case typ == "json.RawMessage":
		return false
	default:
		return true
	}
}

// renderFlattenedUnion emits one struct covering every arm, with the shared
// discriminator first. A field that appears in more than one arm with the same
// type is emitted once.
func (g *generator) renderFlattenedUnion(
	b *strings.Builder, name string, s *schema, tag string,
	inline *[]inlineType, emitted map[string]bool,
) {
	type field struct {
		json     string
		typ      string
		desc     string
		required bool
	}
	order := []string{}
	byName := map[string]field{}
	tagValues := []string{}

	for _, arm := range s.OneOf {
		armRequired := map[string]bool{}
		for _, r := range arm.Required {
			armRequired[r] = true
		}
		props := make([]string, 0, len(arm.Properties))
		for p := range arm.Properties {
			props = append(props, p)
		}
		sort.Strings(props)

		for _, p := range props {
			ps := arm.Properties[p]
			if p == tag {
				for _, v := range ps.Enum {
					if str, ok := v.(string); ok {
						tagValues = append(tagValues, str)
					}
				}
				continue
			}
			typ := g.goType(ps, name, p, inline)
			existing, seen := byName[p]
			if !seen {
				order = append(order, p)
				byName[p] = field{json: p, typ: typ, desc: ps.Description, required: armRequired[p]}
				continue
			}
			// The same field name with a different type across arms needs one Go
			// type. Where the arms agree on the underlying JSON kind — six string
			// enums for `status`, four integer widths for `durationMs` — widening
			// to that kind keeps the field readable and stays true to the wire.
			// Where they genuinely disagree, raw JSON is the honest rendering.
			if existing.typ != typ {
				existing.typ = g.widen(existing.typ, typ)
				byName[p] = existing
			}
		}
	}

	tagType := name + "Type"
	doc(b, name, s.Description)
	fmt.Fprintf(b, "//\n// A tagged union: %s selects which fields carry values.\n", exportName(tag))
	fmt.Fprintf(b, "type %s struct {\n", name)
	fmt.Fprintf(b, "\t%s %s `json:%q`\n", exportName(tag), tagType, tag)

	used := map[string]bool{exportName(tag): true}
	for _, p := range order {
		f := byName[p]
		goName := exportName(p)
		if used[goName] {
			goName += "_"
		}
		used[goName] = true
		typ := f.typ
		// Every non-discriminator field is optional: it belongs to some arms only.
		if needsPointer(typ) {
			typ = "*" + typ
		}
		if f.desc != "" {
			fmt.Fprintf(b, "\t// %s\n", oneLine(f.desc))
		}
		fmt.Fprintf(b, "\t%s %s `json:\"%s,omitempty\"`\n", goName, typ, p)
	}
	b.WriteString("}\n\n")

	emitted[tagType] = true
	fmt.Fprintf(b, "// %s is the discriminator of %s.\ntype %s string\n\nconst (\n", tagType, name, tagType)
	seen := map[string]bool{}
	sort.Strings(tagValues)
	for _, v := range tagValues {
		if seen[v] {
			continue
		}
		seen[v] = true
		fmt.Fprintf(b, "\t%s%s %s = %q\n", tagType, exportName(v), tagType, v)
	}
	b.WriteString(")\n\n")
}

func isStringEnum(s *schema) bool {
	for _, t := range s.types() {
		if t == "string" {
			return true
		}
	}
	if len(s.types()) > 0 {
		return false
	}
	for _, v := range s.Enum {
		if _, ok := v.(string); !ok {
			return false
		}
	}
	return len(s.Enum) > 0
}
