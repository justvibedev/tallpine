package agentpolicy

import (
	"strings"
	"unicode"
)

// envName is the derived variable for a field: TALLPINE_AGENT_<RULE>_<FIELD>, with
// camelCase split into words (permissionCap.max -> TALLPINE_AGENT_PERMISSION_CAP_MAX).
func envName(rule, field string) string {
	return "TALLPINE_AGENT_" + screamingSnake(rule) + "_" + screamingSnake(field)
}

func screamingSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) && i > 0 {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToUpper(r))
	}
	return b.String()
}

// envNames lists the variables read for a field, derived name first.
func envNames(rule string, f Field) []string {
	return append([]string{envName(rule, f.Name)}, f.EnvAliases...)
}

// readEnv builds the environment layer. A value that is set but invalid must
// not leave the install silently unrestricted: a strictest-on-invalid field
// (the permission cap) takes its strictest value; others are ignored. Both warn.
func readEnv(rules []Rule, lookup func(string) (string, bool)) layer {
	l := layer{values: values{}}
	for _, r := range rules {
		for _, f := range r.Schema().Fields {
			for _, name := range envNames(r.Name(), f) {
				text, ok := lookup(name)
				if !ok || (strings.TrimSpace(text) == "" && f.Kind != KindStringList) {
					continue
				}
				value, err := f.parseText(text)
				if err == nil {
					setValue(l.values, r.Name(), f.Name, value)
					break
				}
				if f.OnInvalid == InvalidStrictest {
					setValue(l.values, r.Name(), f.Name, f.strictest())
					l.warnings = append(l.warnings, name+" has an invalid value ("+err.Error()+"); the strictest value is applied")
				} else {
					l.warnings = append(l.warnings, name+" has an invalid value ("+err.Error()+") and is ignored")
				}
				break
			}
		}
	}
	return l
}

// EnvNames lists the environment variables read for the field of rule, derived
// name first. The settings registry declares them as the field's overrides.
func (f Field) EnvNames(rule string) []string { return envNames(rule, f) }
