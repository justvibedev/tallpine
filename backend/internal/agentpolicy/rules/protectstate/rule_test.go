package protectstate_test

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/protectstate"
	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestOffByDefault(t *testing.T) {
	if protectstate.Of(policytest.Decision(t, nil)).Enabled() {
		t.Fatal("protectState must be off by default")
	}
	if protectstate.Of(agentpolicy.Decision{}).Enabled() {
		t.Fatal("zero decision must be off")
	}
}

func TestUserOptIn(t *testing.T) {
	d := policytest.Decision(t, map[string]map[string]any{protectstate.Name: {protectstate.FieldMode: protectstate.On}})
	if !protectstate.Of(d).Enabled() {
		t.Fatal("user opt-in must turn it on")
	}
}

func TestEnvLocksItOn(t *testing.T) {
	engine, err := agentpolicy.New(rules.All(), &policytest.Store{},
		agentpolicy.WithManagedFile("", nil),
		agentpolicy.WithEnv(func(k string) (string, bool) {
			return "on", k == "TALLPINE_AGENT_PROTECT_STATE_MODE"
		}))
	if err != nil {
		t.Fatal(err)
	}
	if !protectstate.Of(engine.DecideStatic(context.Background())).Enabled() {
		t.Fatal("TALLPINE_AGENT_PROTECT_STATE_MODE=on must turn it on")
	}
}

func TestProjectMayTightenNotLoosen(t *testing.T) {
	ctx := context.Background()
	engine, _ := policytest.Engine(t, map[string]map[string]any{protectstate.Name: {protectstate.FieldMode: protectstate.On}})
	_ = engine.Update(ctx, "p", agentpolicy.Update{protectstate.Name: {protectstate.FieldMode: []byte(`"off"`)}}, time.Now())
	if !protectstate.Of(engine.Decide(ctx, agentpolicy.SessionRef{Project: "p"})).Enabled() {
		t.Fatal("a project must not turn protectState off")
	}
	engine2, _ := policytest.Engine(t, nil)
	if err := engine2.Update(ctx, "q", agentpolicy.Update{protectstate.Name: {protectstate.FieldMode: []byte(`"on"`)}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if !protectstate.Of(engine2.Decide(ctx, agentpolicy.SessionRef{Project: "q"})).Enabled() {
		t.Fatal("a project may turn protectState on")
	}
}

// B435: dev tooling is allowed by default, a project can tighten it to strict
// but never loosen a user's strict, and the managed env locks it.
func TestDevToolingDefaultsAllowProjectTightensOnly(t *testing.T) {
	ctx := context.Background()
	on := map[string]map[string]any{protectstate.Name: {protectstate.FieldMode: protectstate.On}}
	if !protectstate.Of(policytest.Decision(t, on)).DevTooling() {
		t.Fatal("devTooling must default to allow when the fence is on")
	}
	if protectstate.Of(policytest.Decision(t, nil)).DevTooling() {
		t.Fatal("devTooling means nothing while the fence is off")
	}
	engine, _ := policytest.Engine(t, on)
	if err := engine.Update(ctx, "p", agentpolicy.Update{protectstate.Name: {protectstate.FieldDevTooling: []byte(`"strict"`)}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if protectstate.Of(engine.Decide(ctx, agentpolicy.SessionRef{Project: "p"})).DevTooling() {
		t.Fatal("a project may tighten devTooling to strict")
	}
	strict := map[string]map[string]any{protectstate.Name: {protectstate.FieldMode: protectstate.On, protectstate.FieldDevTooling: protectstate.DevStrict}}
	engine2, _ := policytest.Engine(t, strict)
	_ = engine2.Update(ctx, "q", agentpolicy.Update{protectstate.Name: {protectstate.FieldDevTooling: []byte(`"allow"`)}}, time.Now())
	if protectstate.Of(engine2.Decide(ctx, agentpolicy.SessionRef{Project: "q"})).DevTooling() {
		t.Fatal("a project must not loosen a strict devTooling")
	}
}

// Dev mode (owner-set) treats protectState as off for orchestrators only:
// workers keep the fence, so they never reach the install token.
func TestDevModeTreatsItAsOffForOrchestratorsOnly(t *testing.T) {
	engine, _ := policytest.Engine(t, map[string]map[string]any{
		protectstate.Name:       {protectstate.FieldMode: protectstate.On},
		agentpolicy.DevModeRule: {"enabled": true},
	})
	decide := func(kind domain.SessionKind) bool {
		return protectstate.Of(engine.Decide(context.Background(), agentpolicy.SessionRef{Project: "p", Kind: kind})).Enabled()
	}
	if decide(domain.KindOrchestrator) {
		t.Fatal("devMode must turn protectState off for an orchestrator")
	}
	for _, kind := range []domain.SessionKind{domain.KindWorker, domain.SessionKind("reviewer")} {
		if !decide(kind) {
			t.Fatalf("devMode must not turn protectState off for %s", kind)
		}
	}
}
