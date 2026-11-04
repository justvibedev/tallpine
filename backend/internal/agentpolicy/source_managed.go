package agentpolicy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/buildinfo"
)

// EnvManagedPolicyFile points the daemon at a managed policy file. It is
// honoured only in development and test builds: in a release binary a user who
// controls the daemon's environment could otherwise point it at a weaker file.
const EnvManagedPolicyFile = "TALLPINE_MANAGED_POLICY_FILE"

// ManagedSchemaVersion is the newest managed payload version this daemon
// understands. Version 1 is the original flat file ({"maxPermission": ...},
// still accepted); version 2 nests settings by rule:
//
//	{"schemaVersion": 2, "rules": {"permissionCap": {"max": "auto"}}}
const ManagedSchemaVersion = 2

// managedPath returns the managed policy file location for this platform.
func managedPath() string {
	if buildinfo.IsDevBuild() {
		if p := os.Getenv(EnvManagedPolicyFile); p != "" {
			return p
		}
	}
	switch runtime.GOOS {
	case "darwin":
		return "/Library/Managed Preferences/" + brand.BundleID + ".plist"
	case "windows":
		// Not the ProgramData env var: a user can set it in their own
		// environment and point the daemon at a policy they wrote.
		if pd := programDataDir(); pd != "" {
			return filepath.Join(pd, brand.ProductName, "policy.json")
		}
		return ""
	default:
		return "/etc/" + brand.DaemonServiceName + "/policy.json"
	}
}

// managedResult is the managed layer plus where it came from.
type managedResult struct {
	layer layer
	path  string
}

// readManaged loads the managed layer. A missing file is no layer. A file that
// exists but cannot be read (for example an ACL the daemon user lacks) applies
// the strictest policy with a warning: dropping it would void the admin's lock. A present but malformed one applies the strictest value of every field
// that declares one, locked, plus a warning: an admin clearly meant to restrict
// agents, so failing open would void the lock.
func readManaged(rules []Rule, path string, convert func(string) ([]byte, error)) managedResult {
	if path == "" {
		return managedResult{}
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return managedResult{path: path, layer: strictestLayer(rules, "managed policy file is not accessible; the strictest policy is applied")}
		}
		return managedResult{}
	}
	data, err := convert(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return managedResult{}
		}
		return managedResult{path: path, layer: strictestLayer(rules, "managed policy file could not be read; the strictest policy is applied")}
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		return managedResult{path: path, layer: strictestLayer(rules, "managed policy file is invalid ("+err.Error()+"); the strictest policy is applied")}
	}
	return managedResult{path: path, layer: parseManagedDoc(rules, doc)}
}

// strictestLayer sets every strictest-on-invalid field to its strictest value.
func strictestLayer(rules []Rule, warning string) layer {
	l := layer{values: values{}, warnings: []string{warning}}
	for _, r := range rules {
		for _, f := range r.Schema().Fields {
			if f.OnInvalid == InvalidStrictest {
				setValue(l.values, r.Name(), f.Name, f.strictest())
			}
		}
	}
	return l
}

func setValue(v values, rule, field string, raw json.RawMessage) {
	if v[rule] == nil {
		v[rule] = Settings{}
	}
	v[rule][field] = raw
}

func parseManagedDoc(rules []Rule, doc map[string]json.RawMessage) (l layer) {
	l = layer{values: values{}}
	version := 1
	if raw, ok := doc["schemaVersion"]; ok {
		if err := json.Unmarshal(raw, &version); err != nil || version < 1 {
			l.warnings = append(l.warnings, "managed policy schemaVersion is invalid; treated as 1")
			version = 1
		}
	}
	if version > ManagedSchemaVersion {
		l.warnings = append(l.warnings, fmt.Sprintf("managed policy schemaVersion %d is newer than this app understands (%d); applying the settings it knows", version, ManagedSchemaVersion))
	}
	defer func() {
		raw, present := doc["profile"]
		profile, warn := parseProfile(raw, present)
		if warn != "" {
			l.warnings = append(l.warnings, warn)
		}
		if profile != "" {
			l.profile = profile
			applyProfileFloors(rules, &l)
		}
	}()
	if _, nested := doc["rules"]; nested || version >= 2 {
		parseManagedRules(rules, doc["rules"], &l)
		return l
	}
	for _, r := range rules {
		for _, f := range r.Schema().Fields {
			if f.LegacyKey == "" {
				continue
			}
			if raw, ok := doc[f.LegacyKey]; ok {
				applyManagedValue(&l, r.Name(), f, raw)
			}
		}
	}
	return l
}

func parseManagedRules(rules []Rule, raw json.RawMessage, l *layer) {
	var nested map[string]map[string]json.RawMessage
	if raw != nil {
		if err := json.Unmarshal(raw, &nested); err != nil {
			*l = mergeLayers(*l, strictestLayer(rules, "managed policy rules are invalid ("+err.Error()+"); the strictest policy is applied"))
			return
		}
	}
	byName := map[string]Rule{}
	for _, r := range rules {
		byName[r.Name()] = r
	}
	for name, fields := range nested {
		r, ok := byName[name]
		if !ok {
			l.warnings = append(l.warnings, "managed policy names an unknown rule \""+name+"\"; ignored")
			continue
		}
		for field, value := range fields {
			f, ok := r.Schema().field(field)
			if !ok {
				l.warnings = append(l.warnings, "managed policy names an unknown setting \""+name+"."+field+"\"; ignored")
				continue
			}
			applyManagedValue(l, name, f, value)
		}
	}
}

func mergeLayers(a, b layer) layer {
	// b is a strictest layer; a value it sets is no longer the profile floor.
	for rule, settings := range b.values {
		for field := range settings {
			delete(a.profileSet, rule+"."+field)
		}
	}
	for rule, settings := range b.values {
		for field, v := range settings {
			setValue(a.values, rule, field, v)
		}
	}
	a.warnings = append(a.warnings, b.warnings...)
	return a
}

func applyManagedValue(l *layer, rule string, f Field, raw json.RawMessage) {
	value, err := f.Parse(raw)
	if err != nil {
		l.warnings = append(l.warnings, "managed policy "+rule+"."+f.Name+" is invalid ("+err.Error()+")")
		if f.OnInvalid == InvalidStrictest {
			setValue(l.values, rule, f.Name, f.strictest())
			l.warnings[len(l.warnings)-1] += "; the strictest value is applied"
		}
		return
	}
	setValue(l.values, rule, f.Name, value)
}
