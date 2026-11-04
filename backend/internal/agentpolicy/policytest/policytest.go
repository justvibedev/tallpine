// Package policytest builds hermetic agent policy engines and decisions for
// tests of the seams: no managed file, no TALLPINE_AGENT_* variables.
package policytest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
)

// Store is an in-memory agentpolicy.UserStore.
type Store struct{ Data []byte }

// AgentPolicy implements agentpolicy.UserStore.
func (s *Store) AgentPolicy(context.Context) ([]byte, error) { return s.Data, nil }

// SetAgentPolicy implements agentpolicy.UserStore.
func (s *Store) SetAgentPolicy(_ context.Context, d []byte, _ time.Time) error {
	s.Data = d
	return nil
}

// Engine returns an engine over every rule whose user layer holds settings
// (rule -> field -> value). The store is returned so tests can inspect writes.
func Engine(t testing.TB, settings map[string]map[string]any) (*agentpolicy.Engine, *Store) {
	t.Helper()
	store := &Store{}
	if settings != nil {
		doc, err := json.Marshal(map[string]any{"version": agentpolicy.UserDocVersion, "rules": settings})
		if err != nil {
			t.Fatal(err)
		}
		store.Data = doc
	}
	engine, err := agentpolicy.New(rules.All(), store,
		agentpolicy.WithManagedFile("", nil),
		agentpolicy.WithEnv(func(string) (string, bool) { return "", false }))
	if err != nil {
		t.Fatal(err)
	}
	return engine, store
}

// Decision returns a session-less decision for the given user settings.
func Decision(t testing.TB, settings map[string]map[string]any) agentpolicy.Decision {
	t.Helper()
	engine, _ := Engine(t, settings)
	return engine.DecideStatic(context.Background())
}

// Cap returns a decision whose permission cap is level.
func Cap(t testing.TB, level permissioncap.Level) agentpolicy.Decision {
	t.Helper()
	return Decision(t, map[string]map[string]any{permissioncap.Name: {permissioncap.FieldMax: string(level)}})
}

// Managed returns an engine over every rule whose managed file holds doc (JSON)
// and whose user layer holds settings. No TALLPINE_AGENT_* variables apply.
func Managed(t testing.TB, doc string, settings map[string]map[string]any) (*agentpolicy.Engine, *Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "managed.json")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &Store{}
	if settings != nil {
		data, err := json.Marshal(map[string]any{"version": agentpolicy.UserDocVersion, "rules": settings})
		if err != nil {
			t.Fatal(err)
		}
		store.Data = data
	}
	engine, err := agentpolicy.New(rules.All(), store,
		agentpolicy.WithManagedFile(path, os.ReadFile),
		agentpolicy.WithEnv(func(string) (string, bool) { return "", false }))
	if err != nil {
		t.Fatal(err)
	}
	return engine, store
}

// Hardened returns a session-less decision under the managed hardened profile.
func Hardened(t testing.TB) agentpolicy.Decision {
	t.Helper()
	engine, _ := Managed(t, `{"schemaVersion":2,"profile":"hardened"}`, nil)
	return engine.DecideStatic(context.Background())
}
