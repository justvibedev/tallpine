// Package ruletest builds a single-rule agent policy engine for the in-package
// tests of rules/*. It must not import agentpolicy/rules (import cycle), so it
// stays separate from policytest.
package ruletest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
)

// MemStore is an in-memory agentpolicy.UserStore.
type MemStore struct{ doc []byte }

// AgentPolicy implements agentpolicy.UserStore.
func (m *MemStore) AgentPolicy(context.Context) ([]byte, error) { return m.doc, nil }

// SetAgentPolicy implements agentpolicy.UserStore.
func (m *MemStore) SetAgentPolicy(_ context.Context, b []byte, _ time.Time) error {
	m.doc = b
	return nil
}

// Engine returns an engine holding only rule; a non-empty managed is written
// to a temp file and used as the managed policy layer.
func Engine(t *testing.T, rule agentpolicy.Rule, managed string) *agentpolicy.Engine {
	t.Helper()
	var opts []agentpolicy.Option
	if managed != "" {
		path := filepath.Join(t.TempDir(), "policy.json")
		if err := os.WriteFile(path, []byte(managed), 0o600); err != nil {
			t.Fatal(err)
		}
		opts = append(opts, agentpolicy.WithManagedFile(path, os.ReadFile))
	}
	e, err := agentpolicy.New([]agentpolicy.Rule{rule}, &MemStore{}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// SetProjectEnabled writes the project's "enabled" field for the named rule.
// A locked field is reported, not stored; the caller asserts on Decide.
func SetProjectEnabled(e *agentpolicy.Engine, name string, on bool) {
	raw := json.RawMessage(`false`)
	if on {
		raw = json.RawMessage(`true`)
	}
	_ = e.Update(context.Background(), "p", agentpolicy.Update{name: {"enabled": raw}}, time.Now())
}

// OptInRule is the shared contract of an off-by-default, project-opt-in rule:
// off with no settings, on after a project opt-in, and forced off by a managed
// policy that disables it. newEngine builds the single-rule engine.
type OptInRule struct {
	Name      string // rule key in policy documents
	Label     string // human name used in failure messages
	NewEngine func(t *testing.T, managed string) *agentpolicy.Engine
	Enabled   func(*agentpolicy.Engine) bool
}

// OffByDefault asserts the rule is disabled with no settings.
func (r OptInRule) OffByDefault(t *testing.T) {
	t.Helper()
	if r.Enabled(r.NewEngine(t, "")) {
		t.Fatalf("%s must be off by default", r.Label)
	}
}

// ProjectOptsIn asserts a project opt-in enables the rule.
func (r OptInRule) ProjectOptsIn(t *testing.T) {
	t.Helper()
	e := r.NewEngine(t, "")
	SetProjectEnabled(e, r.Name, true)
	if !r.Enabled(e) {
		t.Fatal("project opt-in did not enable the rule")
	}
}

// ManagedFileForcesOff asserts a managed policy overrides a project opt-in.
func (r OptInRule) ManagedFileForcesOff(t *testing.T) {
	t.Helper()
	e := r.NewEngine(t, `{"schemaVersion":2,"rules":{"`+r.Name+`":{"enabled":false}}}`)
	SetProjectEnabled(e, r.Name, true)
	if r.Enabled(e) {
		t.Fatal("managed policy must force the setting off over a project opt-in")
	}
}
