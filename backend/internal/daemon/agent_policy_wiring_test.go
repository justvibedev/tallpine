package daemon

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
)

type policyRecorder struct{ engine sessionmanager.PolicyEngine }

func (p *policyRecorder) SetAgentPolicy(e sessionmanager.PolicyEngine) { p.engine = e }

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestWireAgentPolicy_HandsTheEngineToTheManager(t *testing.T) {
	t.Setenv("TALLPINE_AGENT_MAX_PERMISSION", "auto")
	engine, err := newAgentPolicyEngine(&policytest.Store{})
	if err != nil {
		t.Fatal(err)
	}
	mgr := &policyRecorder{}
	wireAgentPolicy(engine, mgr, discardLog())
	if mgr.engine == nil {
		t.Fatal("session manager did not receive the policy engine")
	}
	decision := mgr.engine.Decide(context.Background(), agentpolicy.SessionRef{Project: "p"})
	if !permissioncap.Of(decision).ForbidsBypass() {
		t.Fatal("the environment's cap did not reach the decision the manager makes")
	}
}

func TestWireAgentPolicy_DefaultCapsNothing(t *testing.T) {
	t.Setenv("TALLPINE_AGENT_MAX_PERMISSION", "")
	engine, err := newAgentPolicyEngine(&policytest.Store{})
	if err != nil {
		t.Fatal(err)
	}
	mgr := &policyRecorder{}
	wireAgentPolicy(engine, mgr, discardLog())
	if permissioncap.Of(mgr.engine.Decide(context.Background(), agentpolicy.SessionRef{})).ForbidsBypass() {
		t.Fatal("default install capped agents")
	}
}

func TestLogAgentPolicy_ReportsActiveSettingsAndWarningsWithoutDefaults(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	t.Setenv("TALLPINE_AGENT_MAX_PERMISSION", "accept-edits") // typo: applies the strictest level and warns
	engine, err := newAgentPolicyEngine(&policytest.Store{})
	if err != nil {
		t.Fatal(err)
	}
	logAgentPolicy(engine, log)
	out := buf.String()
	for _, want := range []string{"setting active", "rule=permissionCap", "setting=max", "source=env", "invalid value"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log lacks %q:\n%s", want, out)
		}
	}
	buf.Reset()
	t.Setenv("TALLPINE_AGENT_MAX_PERMISSION", "")
	engine, _ = newAgentPolicyEngine(&policytest.Store{})
	logAgentPolicy(engine, log)
	if buf.Len() != 0 {
		t.Fatalf("an all-default policy must log nothing:\n%s", buf.String())
	}
}
