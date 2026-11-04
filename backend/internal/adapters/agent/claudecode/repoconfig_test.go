package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/repoagentconfig"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func boolPtr(b bool) *bool { return &b }

var userOnly = []string{"--setting-sources", "user"}

func hasSubseq(cmd, sub []string) bool {
	for i := 0; i+len(sub) <= len(cmd); i++ {
		if reflect.DeepEqual(cmd[i:i+len(sub)], sub) {
			return true
		}
	}
	return false
}

// repoConfigDecision decides for project "p" with the project's repoAgentConfig
// setting (nil leaves the default) under an optional managed policy document.
func repoConfigDecision(t *testing.T, project *bool, managed string) agentpolicy.Decision {
	t.Helper()
	opts := []agentpolicy.Option{agentpolicy.WithEnv(func(string) (string, bool) { return "", false })}
	if managed != "" {
		path := filepath.Join(t.TempDir(), "policy.json")
		if err := os.WriteFile(path, []byte(managed), 0o600); err != nil {
			t.Fatal(err)
		}
		opts = append(opts, agentpolicy.WithManagedFile(path, os.ReadFile))
	} else {
		opts = append(opts, agentpolicy.WithManagedFile("", nil))
	}
	engine, err := agentpolicy.New(rules.All(), &policytest.Store{}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	if project != nil {
		raw := []byte(`false`)
		if *project {
			raw = []byte(`true`)
		}
		// A managed-locked field is reported, not stored; the caller asserts on Decide.
		_ = engine.Update(context.Background(), "p", agentpolicy.Update{repoagentconfig.Name: {repoagentconfig.FieldEnabled: raw}}, time.Now())
	}
	return engine.Decide(context.Background(), agentpolicy.SessionRef{Project: "p", Harness: domain.HarnessClaudeCode})
}

// Workers load repo agent config by default (no --setting-sources); the project
// toggle off launches with the user's settings only. Launch and restore agree.
func TestWorkerRepoAgentConfigToggle(t *testing.T) {
	p := &Plugin{resolvedBinary: "claude"}
	restoreRef := ports.SessionRef{ID: "s", Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "native-1"}}
	for _, tc := range []struct {
		name string
		d    agentpolicy.Decision
		want bool
	}{
		{"zero decision", agentpolicy.Decision{}, false},
		{"project on (default)", repoConfigDecision(t, boolPtr(true), ""), false},
		{"project off", repoConfigDecision(t, boolPtr(false), ""), true},
	} {
		launch, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{Policy: tc.d})
		if err != nil {
			t.Fatal(err)
		}
		restore, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{Policy: tc.d, Session: restoreRef})
		if err != nil || !ok {
			t.Fatalf("restore: ok=%v err=%v", ok, err)
		}
		for name, cmd := range map[string][]string{"launch": launch, "restore": restore} {
			if got := hasSubseq(cmd, userOnly); got != tc.want {
				t.Errorf("%s %s: --setting-sources user = %v, want %v: %#v", tc.name, name, got, tc.want, cmd)
			}
			if contains(cmd, "--strict-mcp-config") {
				t.Errorf("%s %s: a worker must keep its user MCP servers: %#v", tc.name, name, cmd)
			}
		}
	}
}

// The managed policy file forces repo config off over a project that wants it.
func TestManagedPolicyForcesRepoAgentConfigOff(t *testing.T) {
	d := repoConfigDecision(t, boolPtr(true), `{"schemaVersion":2,"rules":{"repoAgentConfig":{"enabled":false}}}`)
	cmd, err := (&Plugin{resolvedBinary: "claude"}).GetLaunchCommand(context.Background(), ports.LaunchConfig{Policy: d})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSubseq(cmd, userOnly) {
		t.Fatalf("managed force-off did not restrict setting sources: %#v", cmd)
	}
}

// A reviewer never loads repo agent config, whatever the policy says, and takes
// no MCP servers but Tallpine's.
func TestReviewerSkipsRepoAgentConfig(t *testing.T) {
	p := &Plugin{resolvedBinary: "claude"}
	launch, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{SkipRepoAgentConfig: true})
	if err != nil {
		t.Fatal(err)
	}
	restore, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		SkipRepoAgentConfig: true,
		Session:             ports.SessionRef{ID: "s", Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "native-1"}},
	})
	if err != nil || !ok {
		t.Fatalf("restore: ok=%v err=%v", ok, err)
	}
	for name, cmd := range map[string][]string{"launch": launch, "restore": restore} {
		if !hasSubseq(cmd, userOnly) || !contains(cmd, "--strict-mcp-config") {
			t.Errorf("%s: reviewer must launch user-only with strict MCP: %#v", name, cmd)
		}
	}
}

// Under a permission cap or a managed policy only user settings load, whatever
// the project toggle says; with no policy the launch is unchanged.
func TestPolicyDropsLocalSettingSource(t *testing.T) {
	p := &Plugin{resolvedBinary: "claude"}
	capped := policytest.Cap(t, permissioncap.Auto)
	cappedToggleOn := repoConfigDecision(t, boolPtr(true), `{"schemaVersion":2,"rules":{"permissionCap":{"max":"auto"}}}`)
	managed := repoConfigDecision(t, nil, `{"schemaVersion":2,"rules":{"envFilter":{"mode":"allowlist"}}}`)
	for _, tc := range []struct {
		name string
		d    agentpolicy.Decision
		want bool
	}{
		{"no policy", agentpolicy.Decision{}, false},
		{"default decision", repoConfigDecision(t, nil, ""), false},
		{"cap active", capped, true},
		{"managed policy", managed, true},
		{"cap active, toggle on", cappedToggleOn, true},
	} {
		launch, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{Policy: tc.d})
		if err != nil {
			t.Fatal(err)
		}
		restore, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{Policy: tc.d,
			Session: ports.SessionRef{ID: "s", Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "native-1"}}})
		if err != nil || !ok {
			t.Fatalf("restore: ok=%v err=%v", ok, err)
		}
		for name, cmd := range map[string][]string{"launch": launch, "restore": restore} {
			if got := hasSubseq(cmd, userOnly); got != tc.want {
				t.Errorf("%s %s: --setting-sources user = %v, want %v: %#v", tc.name, name, got, tc.want, cmd)
			}
			if !tc.want && contains(cmd, "--setting-sources") {
				t.Errorf("%s %s: unchanged launch must not set sources: %#v", tc.name, name, cmd)
			}
		}
	}
}
