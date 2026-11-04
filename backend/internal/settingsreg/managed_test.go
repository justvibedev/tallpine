package settingsreg

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
)

// managedKeysFile is the key list the desktop app reads (update-policy.ts).
const managedKeysFile = "../../../frontend/src/shared/managed-keys.json"

// TestManagedKeysFileIsCurrent keeps the generated TS key list in step with the
// registry. Regenerate with UPDATE_MANAGED_KEYS=1 go test ./internal/settingsreg.
func TestManagedKeysFileIsCurrent(t *testing.T) {
	want, err := json.MarshalIndent(Default().ManagedKeys(), "", "\t")
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')
	if os.Getenv("UPDATE_MANAGED_KEYS") != "" {
		if err := os.WriteFile(filepath.FromSlash(managedKeysFile), want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(filepath.FromSlash(managedKeysFile))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("frontend/src/shared/managed-keys.json is stale (err=%v); run UPDATE_MANAGED_KEYS=1 go test ./internal/settingsreg", err)
	}
}

// TestLockableKeysAreExactlyTheApprovedSet pins the first lockable set; making
// another key lockable is a deliberate one-line change plus this list.
func TestLockableKeysAreExactlyTheApprovedSet(t *testing.T) {
	var got []string
	for _, s := range Default().All() {
		if s.MDMLockable && s.Scope != ScopePolicy {
			got = append(got, string(s.Key))
		}
	}
	slices.Sort(got)
	want := []string{
		"app.cleanup.artifacts", "app.cleanup.automatic", "app.cleanup.backlog", "app.cleanup.branches", "app.cleanup.containers",
		"app.cleanup.goBuildCache", "app.cleanup.harnessCaches", "app.cleanup.sessionTemp", "app.cleanup.systemTemp", "app.cleanup.worktrees",
		"app.dynamicOrchestratorContext", "app.mobile.enabled", "app.providerEventRetentionDays",
		"project.autoReview", "project.models", "project.orchestrator", "project.planner", "project.reviewRisk", "project.reviewers", "project.terminateOnPrMerge", "project.trackerIntake", "project.worker",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("lockable non-policy keys = %v, want %v", got, want)
	}
}

func readerOf(ms agentpolicy.ManagedSettings) func() agentpolicy.ManagedSettings {
	return func() agentpolicy.ManagedSettings { return ms }
}

func TestManagedLocks(t *testing.T) {
	reg := Default()
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	t.Run("no file locks nothing", func(t *testing.T) {
		if reg.ManagedLocks(readerOf(agentpolicy.ManagedSettings{})).Locked("app.providerEventRetentionDays") {
			t.Fatal("locked without a managed file")
		}
	})
	t.Run("a set key is locked at its value; others are not", func(t *testing.T) {
		l := reg.ManagedLocks(readerOf(agentpolicy.ManagedSettings{Values: map[string]json.RawMessage{"app.providerEventRetentionDays": raw("7")}}))
		if v, ok := l.Value("app.providerEventRetentionDays"); !ok || string(v) != "7" {
			t.Fatalf("got %s %v", v, ok)
		}
		if l.Locked("app.cleanup.automatic") {
			t.Fatal("unset key locked")
		}
	})
	t.Run("a key that is not lockable is ignored", func(t *testing.T) {
		l := reg.ManagedLocks(readerOf(agentpolicy.ManagedSettings{Values: map[string]json.RawMessage{"app.defaultSessionMode": raw(`"tui"`)}}))
		if l.Locked("app.defaultSessionMode") {
			t.Fatal("non-lockable key was locked")
		}
	})
	t.Run("an invalid value locks at the stored value", func(t *testing.T) {
		l := reg.ManagedLocks(readerOf(agentpolicy.ManagedSettings{Values: map[string]json.RawMessage{"app.providerEventRetentionDays": raw(`"x"`)}}))
		if v, ok := l.Value("app.providerEventRetentionDays"); !ok || v != nil {
			t.Fatalf("got %s %v, want locked without a value", v, ok)
		}
	})
	t.Run("an unreadable file locks every lockable key", func(t *testing.T) {
		l := reg.ManagedLocks(readerOf(agentpolicy.ManagedSettings{Unreadable: true}))
		for _, k := range []string{"app.cleanup.automatic", "project.models", "project.trackerIntake"} {
			if v, ok := l.Value(k); !ok || v != nil {
				t.Errorf("%s: got %s %v, want locked without a value", k, v, ok)
			}
		}
		if l.Locked("app.versionRefresh") {
			t.Error("a non-lockable key was locked")
		}
	})
}

func TestMobileEnabledIsManagedLockable(t *testing.T) {
	def, ok := Default().Lookup("app.mobile.enabled")
	if !ok || !def.MDMLockable || def.Kind != KindBool {
		t.Fatalf("app.mobile.enabled = %+v ok=%v", def, ok)
	}
}
