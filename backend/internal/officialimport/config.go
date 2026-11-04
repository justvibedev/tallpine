package officialimport

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// mappedConfig is a source project config translated into this fork's typed
// ProjectConfig, with everything that changed on the way.
type mappedConfig struct {
	Config   domain.ProjectConfig
	Items    []string // human-readable names of the settings carried over
	Warnings []string
}

// projectConfigKeys lists the JSON keys ProjectConfig understands.
var projectConfigKeys = func() map[string]bool {
	keys := map[string]bool{}
	t := reflect.TypeOf(domain.ProjectConfig{})
	for i := 0; i < t.NumField(); i++ {
		if name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ","); name != "" && name != "-" {
			keys[name] = true
		}
	}
	return keys
}()

// secretEnvName matches variable names that usually hold credentials.
var secretEnvFragments = []string{"KEY", "TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL", "AUTH"}

// mapConfig translates the official per-project config JSON. Keys this fork
// does not model are dropped with a warning that names the key but never its
// value, since a value may be sensitive. Harnesses the fork cannot run are
// replaced by claude-code the same way ProjectConfig.NormalizeHarnesses does,
// and the model/effort/mode picked for them are cleared.
func mapConfig(raw, repoOriginURL string) (mappedConfig, error) {
	var out mappedConfig
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return out, nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &top); err != nil {
		return out, fmt.Errorf("source config is not a JSON object: %w", err)
	}

	known := map[string]json.RawMessage{}
	var dropped []string
	for _, key := range sortedKeys(top) {
		if projectConfigKeys[key] {
			known[key] = top[key]
		} else {
			dropped = append(dropped, key)
		}
	}
	for _, key := range dropped {
		out.Warnings = append(out.Warnings, fmt.Sprintf("config key %q is not supported by Tallpine and was not imported", key))
	}

	knownJSON, err := json.Marshal(known)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(knownJSON, &out.Config); err != nil {
		return out, fmt.Errorf("source config does not fit the project config shape: %w", err)
	}
	out.Warnings = append(out.Warnings, nestedDrops(known, out.Config)...)

	out.Warnings = append(out.Warnings, normalizeHarnesses(&out.Config)...)

	if out.Config.CanonicalRepoURL != "" && repoOriginURL != "" {
		if err := out.Config.ValidateCanonicalRepository(repoOriginURL); err != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("canonicalRepoURL not imported: %v", err))
			out.Config.CanonicalRepoURL = ""
		}
	}

	out.Warnings = append(out.Warnings, officialReferences(out.Config)...)
	out.Warnings = append(out.Warnings, secretEnvWarnings(out.Config)...)
	if err := out.Config.Validate(); err != nil {
		return out, fmt.Errorf("source config is not valid here: %w", err)
	}
	out.Items = describeConfig(out.Config)
	return out, nil
}

// normalizeHarnesses replaces every worker/orchestrator/reviewer harness this
// build cannot run. It is stricter than ProjectConfig.NormalizeHarnesses, which
// only maps ids it knew once shipped: an id the official install added after
// this fork branched is just as unrunnable here, so it falls back too.
func normalizeHarnesses(c *domain.ProjectConfig) []string {
	var warnings []string
	roles := []struct {
		name string
		ro   *domain.RoleOverride
	}{{"worker", &c.Worker}, {"orchestrator", &c.Orchestrator}}
	for _, r := range roles {
		if r.ro.Harness.IsUnsupported() {
			warnings = append(warnings, fmt.Sprintf("%s harness %q is not available in Tallpine; using %s and clearing its model/effort/mode",
				r.name, r.ro.Harness, domain.FallbackHarness))
			r.ro.Harness = domain.FallbackHarness
			r.ro.AgentConfig.Model, r.ro.AgentConfig.Effort, r.ro.AgentConfig.Mode = "", "", ""
		}
	}
	for i := range c.Reviewers {
		rv := &c.Reviewers[i]
		if rv.Harness.IsUnsupported() {
			warnings = append(warnings, fmt.Sprintf("reviewer %d harness %q is not available in Tallpine; using %s and clearing its model/effort/mode",
				i+1, rv.Harness, domain.FallbackReviewerHarness))
			rv.Harness = domain.FallbackReviewerHarness
			rv.AgentConfig.Model, rv.AgentConfig.Effort, rv.AgentConfig.Mode = "", "", ""
		}
	}
	return warnings
}

// nestedDrops reports fields inside known sections (agentConfig.foo, ...) that
// the typed decode silently discarded.
func nestedDrops(known map[string]json.RawMessage, cfg domain.ProjectConfig) []string {
	got, err := json.Marshal(cfg)
	if err != nil {
		return nil
	}
	var gotTree map[string]any
	_ = json.Unmarshal(got, &gotTree)
	var warnings []string
	for _, key := range sortedKeys(known) {
		var orig any
		if err := json.Unmarshal(known[key], &orig); err != nil {
			continue
		}
		for _, p := range missingPaths(orig, gotTree[key], key) {
			warnings = append(warnings, fmt.Sprintf("config field %q is not supported by Tallpine and was not imported", p))
		}
	}
	return warnings
}

// missingPaths lists object paths present in orig but absent in got. Arrays are
// compared element-wise; scalar differences are not drops and are ignored.
func missingPaths(orig, got any, prefix string) []string {
	switch o := orig.(type) {
	case map[string]any:
		g, _ := got.(map[string]any)
		var out []string
		for _, k := range sortedKeys(o) {
			child, ok := g[k]
			if !ok {
				// A nil/empty source value decoding to an omitted field is not a loss.
				if isEmptyJSON(o[k]) {
					continue
				}
				out = append(out, prefix+"."+k)
				continue
			}
			out = append(out, missingPaths(o[k], child, prefix+"."+k)...)
		}
		return out
	case []any:
		g, _ := got.([]any)
		var out []string
		for i, item := range o {
			if i < len(g) {
				out = append(out, missingPaths(item, g[i], fmt.Sprintf("%s[%d]", prefix, i))...)
			}
		}
		return out
	}
	return nil
}

func isEmptyJSON(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case bool:
		return !t
	case []any:
		return len(t) == 0
	case map[string]any:
		return len(t) == 0
	}
	return false
}

// officialReferences flags free-text settings that mention the official
// install's state dir or its AO_* variables, which have no meaning here.
func officialReferences(c domain.ProjectConfig) []string {
	var fields []string
	check := func(name, value string) {
		if strings.Contains(value, "~/.ao") || strings.Contains(value, "$HOME/.ao") || strings.Contains(value, "AO_") {
			fields = append(fields, name)
		}
	}
	check("agentRules", c.AgentRules)
	check("orchestratorRules", c.OrchestratorRules)
	check("verifyCommand", c.VerifyCommand)
	for i, s := range c.PostCreate {
		check(fmt.Sprintf("postCreate[%d]", i), s)
	}
	for i, s := range c.PreRemove {
		check(fmt.Sprintf("preRemove[%d]", i), s)
	}
	for k, v := range c.Env {
		check("env."+k, v)
	}
	sort.Strings(fields)
	if len(fields) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("%s mention the official install (~/.ao or AO_* variables); review them for Tallpine (~/.tallpine, TALLPINE_*)", strings.Join(fields, ", "))}
}

// secretEnvWarnings flags env entries whose names look like credentials. The
// values are copied exactly like any other project env, but are never printed.
func secretEnvWarnings(c domain.ProjectConfig) []string {
	var names []string
	for _, k := range sortedKeys(c.Env) {
		up := strings.ToUpper(k)
		for _, frag := range secretEnvFragments {
			if strings.Contains(up, frag) {
				names = append(names, k)
				break
			}
		}
	}
	if len(names) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("env %s look like credentials; their values were copied from project config as-is (never printed here)", strings.Join(names, ", "))}
}

// describeConfig names what is carried, without any values.
func describeConfig(c domain.ProjectConfig) []string {
	var items []string
	add := func(ok bool, s string) {
		if ok {
			items = append(items, s)
		}
	}
	add(c.DefaultBranch != "", "defaultBranch")
	add(c.CanonicalRepoURL != "", "canonicalRepoURL")
	add(c.SessionPrefix != "", "sessionPrefix")
	add(!c.AgentConfig.IsZero(), "agentConfig")
	add(c.Worker.Harness != "" || !c.Worker.AgentConfig.IsZero(), "worker")
	add(c.Orchestrator.Harness != "" || !c.Orchestrator.AgentConfig.IsZero(), "orchestrator")
	add(len(c.Reviewers) > 0, fmt.Sprintf("reviewers (%d)", len(c.Reviewers)))
	add(c.AgentRules != "" || c.AgentRulesFile != "", "agentRules")
	add(c.OrchestratorRules != "", "orchestratorRules")
	add(c.VerifyCommand != "", "verifyCommand")
	add(len(c.PostCreate) > 0, fmt.Sprintf("postCreate (%d)", len(c.PostCreate)))
	add(len(c.PreRemove) > 0, fmt.Sprintf("preRemove (%d)", len(c.PreRemove)))
	add(len(c.Env) > 0, fmt.Sprintf("env (%d vars)", len(c.Env)))
	add(len(c.Symlinks) > 0, fmt.Sprintf("symlinks (%d)", len(c.Symlinks)))
	add(c.CloneWorktreeInclude, "cloneWorktreeInclude")
	add(!reflect.DeepEqual(c.TrackerIntake, domain.TrackerIntakeConfig{}), "trackerIntake")
	add(c.ContainerReap.Disabled, "containerReap")
	add(c.AutoReview != nil && *c.AutoReview, "autoReview")
	add(c.NoCI, "noCI")
	return items
}
