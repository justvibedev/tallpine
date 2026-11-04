package accountpool

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
)

type memUser struct{ doc []byte }

func (m *memUser) AgentPolicy(context.Context) ([]byte, error) { return m.doc, nil }
func (m *memUser) SetAgentPolicy(_ context.Context, d []byte, _ time.Time) error {
	m.doc = d
	return nil
}

func env(vars map[string]string) agentpolicy.Option {
	return agentpolicy.WithEnv(func(k string) (string, bool) { v, ok := vars[k]; return v, ok })
}

func engine(t *testing.T, user *memUser, vars map[string]string) *agentpolicy.Engine {
	t.Helper()
	e, err := agentpolicy.New([]agentpolicy.Rule{Rule{}}, user, env(vars))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestEngineDefaultsAreOff(t *testing.T) {
	o := Of(engine(t, &memUser{}, nil).DecideStatic(context.Background()))
	if o.Placement() != domain.AccountPlacementDefault || o.Failover() {
		t.Fatalf("untouched install = %q/%t", o.Placement(), o.Failover())
	}
}

func TestUserSettingTakesEffectLive(t *testing.T) {
	ctx := context.Background()
	e := engine(t, &memUser{}, nil)
	update := agentpolicy.Update{Name: {FieldPlacement: json.RawMessage(`"random"`), FieldFailover: json.RawMessage(`true`)}}
	if err := e.Update(ctx, "", update, time.Now()); err != nil {
		t.Fatal(err)
	}
	o := Of(e.DecideStatic(ctx))
	if o.Placement() != domain.AccountPlacementRandom || !o.Failover() {
		t.Fatalf("after Settings change = %q/%t", o.Placement(), o.Failover())
	}
}

func TestEnvironmentOverridesAndLocksTheUserSetting(t *testing.T) {
	ctx := context.Background()
	e := engine(t, &memUser{}, map[string]string{"TALLPINE_ACCOUNT_PLACEMENT": "most-headroom", "TALLPINE_ACCOUNT_FAILOVER": "on"})
	o := Of(e.DecideStatic(ctx))
	if o.Placement() != domain.AccountPlacementMostHeadroom || !o.Failover() {
		t.Fatalf("env = %q/%t", o.Placement(), o.Failover())
	}
	err := e.Update(ctx, "", agentpolicy.Update{Name: {FieldPlacement: json.RawMessage(`"random"`)}}, time.Now())
	var locked *agentpolicy.LockedError
	if err == nil || !errors.As(err, &locked) {
		t.Fatalf("changing an env-locked setting = %v, want a locked error", err)
	}
}

func TestInvalidEnvironmentValueIsIgnored(t *testing.T) {
	e := engine(t, &memUser{}, map[string]string{"TALLPINE_ACCOUNT_PLACEMENT": "fastest", "TALLPINE_ACCOUNT_FAILOVER": "maybe"})
	o := Of(e.DecideStatic(context.Background()))
	if o.Placement() != domain.AccountPlacementDefault || o.Failover() {
		t.Fatalf("invalid env switched the pool on: %q/%t", o.Placement(), o.Failover())
	}
}

func TestManagedFileBeatsEnvironmentAndUser(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "policy.json")
	doc := `{"schemaVersion":2,"rules":{"accountPool":{"placement":"least-used","failover":true}}}`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	e, err := agentpolicy.New([]agentpolicy.Rule{Rule{}}, &memUser{},
		agentpolicy.WithManagedFile(path, func(p string) ([]byte, error) { return os.ReadFile(p) }),
		env(map[string]string{"TALLPINE_ACCOUNT_PLACEMENT": "random", "TALLPINE_ACCOUNT_FAILOVER": "off"}))
	if err != nil {
		t.Fatal(err)
	}
	o := Of(e.DecideStatic(ctx))
	if o.Placement() != domain.AccountPlacementLeastUsed || !o.Failover() {
		t.Fatalf("managed = %q/%t, want least-used/on over the environment", o.Placement(), o.Failover())
	}
}

func TestEnvironmentValuesAreCaseInsensitive(t *testing.T) {
	e := engine(t, &memUser{}, map[string]string{"TALLPINE_ACCOUNT_PLACEMENT": "Most-Headroom", "TALLPINE_ACCOUNT_FAILOVER": "TRUE"})
	o := Of(e.DecideStatic(context.Background()))
	if o.Placement() != domain.AccountPlacementMostHeadroom || !o.Failover() {
		t.Fatalf("env = %q/%t, want most-headroom/on as before the setting moved into the policy", o.Placement(), o.Failover())
	}
}
