package agentpolicy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// FieldKind is the type of a setting. The Settings UI and the API render and
// validate by kind, so a new rule needs no UI plumbing unless it needs a new kind.
type FieldKind string

// Field kinds.
const (
	KindEnum       FieldKind = "enum"
	KindBool       FieldKind = "bool"
	KindStringList FieldKind = "stringList"
	KindInt        FieldKind = "int"
	// KindString is a free-text value (a model name); empty means unset.
	KindString FieldKind = "string"
)

// ProjectOverride says how a project may override a field.
type ProjectOverride string

// Project override modes.
const (
	// ProjectTightenOnly lets a project make agents stricter than the user's
	// global value, never looser. Requires an enum field with an ordered Enum.
	ProjectTightenOnly ProjectOverride = "tightenOnly"
	// ProjectAny lets a project set any valid value.
	ProjectAny ProjectOverride = "any"
	// ProjectNever ignores project overrides of the field.
	ProjectNever ProjectOverride = "never"
)

// OnInvalid says what an invalid value in the env or managed layer does.
type OnInvalid string

const (
	// InvalidIgnore drops the value and warns.
	InvalidIgnore OnInvalid = ""
	// InvalidStrictest applies the strictest enum value (the last) and warns, so
	// a typo in an admin's setting cannot silently leave the install uncapped.
	// Requires an enum field.
	InvalidStrictest OnInvalid = "strictest"
)

// OptionSource names where the UI gets the choices of a free-text field, so the
// field renders as a picker instead of a text box. The stored value stays a
// plain string and Validate still decides what is acceptable.
type OptionSource string

const (
	// SourceNone is a plain text box.
	SourceNone OptionSource = ""
	// SourceHarness offers the supported agent harnesses.
	SourceHarness OptionSource = "harness"
	// SourceModel offers the models of the harness in sibling field
	// Field.SourceField.
	SourceModel OptionSource = "model"
)

// Condition shows a row only when another field of the same rule has a value.
type Condition struct {
	Field  string `json:"field"`
	Equals string `json:"equals"`
}

// Field describes one setting of a rule.
type Field struct {
	Name string
	Kind FieldKind
	// Enum lists the allowed values of an enum field in ascending strictness:
	// the last value is the strictest. Tighten-only overrides and the
	// strictest-on-invalid fallback rely on that order.
	Enum []string
	// Default is the JSON default; it must be the no-restriction value so an
	// untouched install changes nothing.
	Default json.RawMessage
	// EnvAliases are extra environment variable names read for the field. The
	// derived name TALLPINE_AGENT_<RULE>_<FIELD> always applies.
	EnvAliases []string
	// LegacyKey is the flat key of the field in the pre-rule policy documents
	// (managed file v1, user layer v1), accepted forever.
	LegacyKey string

	LabelKey       string
	DescriptionKey string
	// OptionLabelKeys maps each enum value to the i18n key of its label.
	OptionLabelKeys map[string]string
	ProjectOverride ProjectOverride
	OnInvalid       OnInvalid
	// OptionSource and SourceField make a KindString field a harness or model
	// picker; SourceField names the harness field a model field depends on.
	OptionSource OptionSource
	SourceField  string
	VisibleWhen  *Condition
	Advanced     bool
	// ManagedOnly makes the field settable only by the managed file or the
	// environment: the user layer ignores a stored value and refuses a write.
	// The planner's mode and auto-approve use it (B525): the app defaults
	// carry the user's choice, the policy only locks.
	ManagedOnly bool
}

// Schema is the settings of one rule.
type Schema struct {
	TitleKey string
	Fields   []Field
}

func (s Schema) field(name string) (Field, bool) {
	for _, f := range s.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// maxListEntries bounds a stringList field.
const maxListEntries = 100

// maxStringBytes bounds a string field.
const maxStringBytes = 200

// Parse validates raw against the field's kind and returns its canonical JSON.
func (f Field) Parse(raw json.RawMessage) (json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	switch f.Kind {
	case KindEnum:
		var v string
		if err := json.Unmarshal(raw, &v); err != nil || !slices.Contains(f.Enum, v) {
			return nil, fmt.Errorf("%s must be one of %s", f.Name, strings.Join(f.Enum, ", "))
		}
	case KindBool:
		var v bool
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("%s must be true or false", f.Name)
		}
	case KindInt:
		var v int
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("%s must be a whole number", f.Name)
		}
	case KindString:
		var v string
		if err := json.Unmarshal(raw, &v); err != nil || len(v) > maxStringBytes || strings.ContainsRune(v, '\x00') {
			return nil, fmt.Errorf("%s must be text of at most %d bytes", f.Name, maxStringBytes)
		}
	case KindStringList:
		var v []string
		if err := json.Unmarshal(raw, &v); err != nil || v == nil {
			return nil, fmt.Errorf("%s must be a list of strings", f.Name)
		}
		if len(v) > maxListEntries {
			return nil, fmt.Errorf("%s has %d entries; the limit is %d", f.Name, len(v), maxListEntries)
		}
	default:
		return nil, fmt.Errorf("%s has unknown kind %q", f.Name, f.Kind)
	}
	return json.RawMessage(bytes.Clone(raw)), nil
}

// parseText turns a plain-text value (an environment variable) into canonical JSON.
func (f Field) parseText(text string) (json.RawMessage, error) {
	text = strings.TrimSpace(text)
	switch f.Kind {
	case KindEnum:
		// Every enum value is lowercase; the environment is typed by hand.
		encoded, _ := json.Marshal(strings.ToLower(text))
		return f.Parse(encoded)
	case KindString:
		encoded, _ := json.Marshal(text)
		return f.Parse(encoded)
	case KindStringList:
		names := []string{}
		for _, part := range strings.Split(text, ",") {
			if part = strings.TrimSpace(part); part != "" {
				names = append(names, part)
			}
		}
		encoded, _ := json.Marshal(names)
		return f.Parse(encoded)
	case KindBool:
		// Switches are written on/off in the environment as well as true/false.
		text = strings.ToLower(text)
		switch text {
		case "on", "yes", "1":
			text = "true"
		case "off", "no", "0":
			text = "false"
		}
		return f.Parse(json.RawMessage(text))
	default:
		return f.Parse(json.RawMessage(text))
	}
}

// strictness is the position of an enum value in ascending strictness, -1 when unknown.
func (f Field) strictness(raw json.RawMessage) int {
	var v string
	if json.Unmarshal(raw, &v) != nil {
		return -1
	}
	return slices.Index(f.Enum, v)
}

func (f Field) strictest() json.RawMessage {
	if f.Kind == KindBool {
		return json.RawMessage(`true`) // a strictest-on-invalid bool is a safeguard switch: on
	}
	encoded, _ := json.Marshal(f.Enum[len(f.Enum)-1])
	return encoded
}

// checkSchema reports a schema that cannot work; run by the registry test and
// by New so a bad rule fails at startup, not at the first session.
func checkSchema(rule string, s Schema) error {
	seen := map[string]bool{}
	for _, f := range s.Fields {
		if f.Name == "" || seen[f.Name] {
			return fmt.Errorf("rule %s: field name %q is empty or repeated", rule, f.Name)
		}
		seen[f.Name] = true
		if f.Kind == KindEnum && len(f.Enum) < 2 {
			return fmt.Errorf("rule %s field %s: an enum needs at least two values", rule, f.Name)
		}
		if f.ProjectOverride == ProjectTightenOnly && f.Kind != KindEnum {
			return fmt.Errorf("rule %s field %s: tightenOnly needs an enum field", rule, f.Name)
		}
		if f.OnInvalid == InvalidStrictest && f.Kind != KindEnum && f.Kind != KindBool {
			return fmt.Errorf("rule %s field %s: strictest needs an enum or bool field", rule, f.Name)
		}
		if f.OptionSource != SourceNone && f.Kind != KindString {
			return fmt.Errorf("rule %s field %s: an option source needs a string field", rule, f.Name)
		}
		if f.OptionSource == SourceModel {
			dep, ok := s.field(f.SourceField)
			if !ok || dep.OptionSource != SourceHarness {
				return fmt.Errorf("rule %s field %s: a model source needs a harness-source field in sourceField", rule, f.Name)
			}
		}
		if f.Default == nil {
			return fmt.Errorf("rule %s field %s: default is required", rule, f.Name)
		}
		if _, err := f.Parse(f.Default); err != nil {
			return fmt.Errorf("rule %s field %s: invalid default: %w", rule, f.Name, err)
		}
	}
	return nil
}
