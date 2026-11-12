package settingsreg

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/cleanup"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
)

// jsonFields lists the JSON names of a struct's fields.
func jsonFields(t *testing.T, v any) []string {
	t.Helper()
	var out []string
	typ := reflect.TypeOf(v)
	for i := range typ.NumField() {
		f := typ.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if !f.IsExported() || name == "-" {
			continue
		}
		if name == "" {
			t.Fatalf("%s.%s has no json name", typ.Name(), f.Name)
		}
		out = append(out, name)
	}
	return out
}

func keysWithPrefix(scope Scope, prefix string) []string {
	var out []string
	for _, s := range Default().Scope(scope) {
		if rest, ok := strings.CutPrefix(string(s.Key), prefix); ok {
			out = append(out, rest)
		}
	}
	return out
}

func sameSet(t *testing.T, what string, want, got []string) {
	t.Helper()
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("%s: %q exists but is not in the settings registry", what, w)
		}
	}
	for _, g := range got {
		if !slices.Contains(want, g) {
			t.Errorf("%s: registry declares %q, which does not exist", what, g)
		}
	}
}

// A new project config field must be declared in the registry.
func TestEveryProjectConfigFieldIsRegistered(t *testing.T) {
	var top []string
	for _, k := range keysWithPrefix(ScopeProject, "project.") {
		parent, child, nested := strings.Cut(k, ".")
		if !nested {
			top = append(top, k)
			continue
		}
		// A nested key (orchestrator.autoCompactWindow) declares one field of a
		// registered object field; it must exist on that field's type.
		field, ok := reflect.TypeOf(domain.ProjectConfig{}).FieldByNameFunc(func(name string) bool {
			f, _ := reflect.TypeOf(domain.ProjectConfig{}).FieldByName(name)
			tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			return tag == parent
		})
		if !ok || !slices.Contains(keysWithPrefix(ScopeProject, "project."), parent) ||
			!slices.Contains(jsonFields(t, reflect.New(field.Type).Elem().Interface()), child) {
			t.Errorf("project config: registry declares %q, which does not exist", k)
		}
	}
	sameSet(t, "project config", jsonFields(t, domain.ProjectConfig{}), top)
}

// A new cleanup policy field must be declared in the registry.
func TestEveryCleanupPolicyFieldIsRegistered(t *testing.T) {
	sameSet(t, "cleanup policy", jsonFields(t, cleanup.Policy{}), keysWithPrefix(ScopeApp, "app.cleanup."))
	var automatic bool
	s, _ := Default().Lookup("app.cleanup.automatic")
	_ = json.Unmarshal(s.Default, &automatic)
	if automatic != cleanup.DefaultPolicy().Automatic {
		t.Error("app.cleanup.automatic default differs from cleanup.DefaultPolicy")
	}
}

// snapshotKeys maps each app settings snapshot field to its registry keys.
var snapshotKeys = map[string][]Key{
	"HarnessDefaults":            {"app.harnessDefaults"},
	"DefaultSessionMode":         {"app.defaultSessionMode"},
	"ChatHibernation":            {"app.chatHibernation.enabled", "app.chatHibernation.workerIdleSeconds", "app.chatHibernation.orchestratorIdleSeconds"},
	"VersionRefresh":             {"app.versionRefresh"},
	"DynamicOrchestratorContext": {"app.dynamicOrchestratorContext"},
	"WorkflowDefaults":           {"app.workflowDefaults.autoReview", "app.workflowDefaults.planningMode", "app.workflowDefaults.planAutoApprove", "app.workflowDefaults.reviewerHarness", "app.workflowDefaults.reviewerModel"},
	"StorageCompaction":          {"app.storageCompaction"},
	"ProviderEventRetentionDays": {"app.providerEventRetentionDays"},
	"PeerCheck":                  {"app.daemon.peerCheck"},
	"UpdatedAt":                  nil, // bookkeeping, not a setting
}

// A new app settings field must be declared in the registry.
func TestEveryAppSettingIsRegistered(t *testing.T) {
	var fields, mapped []string
	typ := reflect.TypeOf(settingssvc.Snapshot{})
	for i := range typ.NumField() {
		fields = append(fields, typ.Field(i).Name)
	}
	for name, keys := range snapshotKeys {
		mapped = append(mapped, name)
		for _, k := range keys {
			if _, ok := Default().Lookup(k); !ok {
				t.Errorf("snapshot field %s maps to undeclared key %s", name, k)
			}
		}
	}
	sameSet(t, "app settings snapshot", fields, mapped)
	var declared []string
	for _, s := range Default().Scope(ScopeApp) {
		// app.mobile.enabled is the managed mobile switch; mobile state lives in its
		// own file, not the settings snapshot.
		if !strings.HasPrefix(string(s.Key), "app.cleanup.") && s.Key != "app.mobile.enabled" {
			declared = append(declared, string(s.Key))
		}
	}
	var keys []string
	for _, ks := range snapshotKeys {
		for _, k := range ks {
			keys = append(keys, string(k))
		}
	}
	sameSet(t, "app settings keys", keys, declared)
}

func intDefault(t *testing.T, k Key) int64 {
	t.Helper()
	s, ok := Default().Lookup(k)
	if !ok {
		t.Fatalf("%s is not declared", k)
	}
	var v int64
	if err := json.Unmarshal(s.Default, &v); err != nil {
		t.Fatalf("%s default: %v", k, err)
	}
	return v
}

// Declared app defaults match the owners' constants.
func TestAppDefaultsMatchOwners(t *testing.T) {
	if got := intDefault(t, "app.providerEventRetentionDays"); got != settingssvc.DefaultProviderEventRetentionDays {
		t.Errorf("providerEventRetentionDays default = %d, want %d", got, settingssvc.DefaultProviderEventRetentionDays)
	}
	h := domain.DefaultChatHibernationPolicy()
	if got := intDefault(t, "app.chatHibernation.workerIdleSeconds"); got != int64(h.WorkerIdle.Seconds()) {
		t.Errorf("workerIdleSeconds default = %d, want %v", got, h.WorkerIdle)
	}
	if got := intDefault(t, "app.chatHibernation.orchestratorIdleSeconds"); got != int64(h.OrchestratorIdle.Seconds()) {
		t.Errorf("orchestratorIdleSeconds default = %d, want %v", got, h.OrchestratorIdle)
	}
	for _, k := range []Key{"app.chatHibernation.workerIdleSeconds", "app.chatHibernation.orchestratorIdleSeconds"} {
		s, _ := Default().Lookup(k)
		if *s.Min != int64(domain.MinChatHibernationIdle.Seconds()) || *s.Max != int64(domain.MaxChatHibernationIdle.Seconds()) {
			t.Errorf("%s bounds differ from domain", k)
		}
	}
	s, _ := Default().Lookup("app.providerEventRetentionDays")
	if *s.Min != settingssvc.MinProviderEventRetentionDays || *s.Max != settingssvc.MaxProviderEventRetentionDays {
		t.Error("providerEventRetentionDays bounds differ from the settings service")
	}
}

// Every agent-policy field is declared with the rule's default, enum and env.
func TestEveryPolicyFieldIsRegistered(t *testing.T) {
	var want []string
	for _, r := range rules.All() {
		for _, f := range r.Schema().Fields {
			key := Key("policy." + r.Name() + "." + f.Name)
			want = append(want, strings.TrimPrefix(string(key), "policy."))
			s, ok := Default().Lookup(key)
			if !ok {
				continue
			}
			if !bytes.Equal(s.Default, f.Default) || !slices.Equal(s.Enum, f.Enum) ||
				!slices.Equal(s.Env, f.EnvNames(r.Name())) || !s.MDMLockable {
				t.Errorf("%s does not mirror its rule field", key)
			}
		}
	}
	sameSet(t, "agent policy", want, keysWithPrefix(ScopePolicy, "policy."))
}
