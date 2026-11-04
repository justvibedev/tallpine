package review

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// A terminal reviewer is capped like the worker it reviews: the launcher decides
// under the reviewed worker's project and hands the decision to the adapter.
func TestReviewerInvocationCarriesTheProjectsPolicyDecision(t *testing.T) {
	spawn := func(opts ...LauncherOption) *fakeReviewer {
		reviewer := &fakeReviewer{}
		l := NewLauncher(fakeReviewerResolver{reviewer: reviewer, ok: true}, &fakeRuntime{}, t.TempDir(), opts...)
		if _, err := l.Spawn(context.Background(), launchSpec()); err != nil {
			t.Fatalf("Spawn: %v", err)
		}
		return reviewer
	}
	if permissioncap.Of(spawn().gotInv.Policy).ForbidsBypass() {
		t.Fatal("no policy wired must restrict nothing")
	}
	engine, _ := policytest.Engine(t, map[string]map[string]any{"permissionCap": {"max": "auto"}})
	got := spawn(WithPolicy(engine)).gotInv
	if !permissioncap.Of(got.Policy).ForbidsBypass() || got.Policy.Session().Project != launchSpec().ProjectID {
		t.Fatalf("the reviewer launch must carry the policy of the reviewed worker's project: %+v", got.Policy.Session())
	}
}

// A reviewer terminal restored after a daemon restart runs under the same policy
// as a fresh review launch: RestoreTerminal builds its own (idle) invocation.
func TestRestoredReviewerTerminalCarriesTheProjectsPolicyDecision(t *testing.T) {
	restore := func(opts ...LauncherOption) ports.ReviewInvocation {
		reviewer := &fakeReviewer{}
		l := NewLauncher(fakeReviewerResolver{reviewer: reviewer, ok: true}, &fakeRuntime{}, t.TempDir(), opts...)
		if _, err := l.RestoreTerminal(context.Background(), launchSpec()); err != nil {
			t.Fatalf("RestoreTerminal: %v", err)
		}
		return reviewer.gotInv
	}
	if permissioncap.Of(restore().Policy).ForbidsBypass() {
		t.Fatal("no policy wired must restrict nothing")
	}
	engine, _ := policytest.Engine(t, map[string]map[string]any{"permissionCap": {"max": "accept_edits"}})
	got := restore(WithPolicy(engine))
	if !permissioncap.Of(got.Policy).ForbidsBypass() || got.Policy.Session().Project != launchSpec().ProjectID {
		t.Fatalf("a restored terminal reviewer must run under the project's policy: %+v", got.Policy.Session())
	}
}

// A reviewer reads untrusted PR content like a worker does, so it gets the same
// environment policy: under the allowlist its environment holds the allowed
// daemon variables and nothing else, and is marked as the whole environment.
func TestReviewerLaunchEnvironmentFollowsTheAllowlist(t *testing.T) {
	t.Setenv("B3_DB_PASSWORD", "leak")
	t.Setenv("ANTHROPIC_API_KEY", "needed")
	launch := func(opts ...LauncherOption) map[string]string {
		rt := &fakeRuntime{}
		l := NewLauncher(fakeReviewerResolver{reviewer: &fakeReviewer{env: map[string]string{"PATH": "/reviewer/bin"}}, ok: true}, rt, t.TempDir(), opts...)
		if _, err := l.Spawn(context.Background(), launchSpec()); err != nil {
			t.Fatalf("Spawn: %v", err)
		}
		return rt.createCfg.Env
	}
	if env := launch(); env[agentlaunch.EnvScrub] != "" || env["B3_DB_PASSWORD"] != "" {
		t.Fatalf("the default policy must not touch the reviewer env: %v", env)
	}
	engine, _ := policytest.Engine(t, map[string]map[string]any{"envFilter": {"mode": "allowlist"}})
	env := launch(WithPolicy(engine))
	if _, leaked := env["B3_DB_PASSWORD"]; leaked || env["ANTHROPIC_API_KEY"] != "needed" || env[agentlaunch.EnvScrub] != "1" || env["PATH"] == "" {
		t.Fatalf("reviewer env = allowlist only: %v", env)
	}
}
