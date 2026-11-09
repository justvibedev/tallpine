package agent

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type envReadinessAgent struct {
	accountReadinessAgent
	gotEnv map[string]string
}

func (a *envReadinessAgent) AuthStatusWithDiagnosticsEnv(_ context.Context, env map[string]string) (ports.AgentAuthStatus, domain.AgentAccountIdentity, []domain.AgentAuthDiagnostic, error) {
	a.gotEnv = env
	return ports.AgentAuthStatusAuthorized, domain.AgentAccountIdentity{}, nil, nil
}

// B439: an agent signed in through a project folder is not reported signed out.
func TestReadinessCoordinatorChecksAuthUnderTheLaunchEnv(t *testing.T) {
	a := &envReadinessAgent{accountReadinessAgent: accountReadinessAgent{status: ports.AgentAuthStatusUnauthorized}}
	c := newReadinessCoordinator(readinessCoordinatorConfig{
		AuthenticationEnv: func(context.Context, string) map[string]string { return map[string]string{"CLAUDE_CONFIG_DIR": "/p"} },
	})
	observation, failed := c.checkAuthentication(readinessHarness("test", "Test", a), domain.AgentReadinessPurposeDisplay)
	if failed || observation.State != domain.AgentAuthenticationAuthorized || a.gotEnv["CLAUDE_CONFIG_DIR"] != "/p" {
		t.Fatalf("observation=%+v failed=%v env=%v", observation, failed, a.gotEnv)
	}
}
