package claudecode

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// AuthStatusWithDiagnosticsEnv is AuthStatusWithDiagnostics for the login a
// launch with env would use (a project-level CLAUDE_CONFIG_DIR).
func (p *Plugin) AuthStatusWithDiagnosticsEnv(ctx context.Context, env map[string]string) (ports.AgentAuthStatus, domain.AgentAccountIdentity, []domain.AgentAuthDiagnostic, error) {
	status, report, steps, err := p.authStatusAndDiagnostics(ctx, "", env, false)
	return status, report.account(), steps, err
}

// LocalLoginConfigured reports whether the folder a launch with env uses holds
// a sign-in marker (an environment credential or Claude's own account record).
// It reads local state only: nothing is validated, refreshed or returned.
func LocalLoginConfigured(ctx context.Context, env map[string]string) bool {
	status, err := claudeLocalAuthStatus(ctx, launchResolveOptions("", env).WithClaudeSettings(ctx))
	return err == nil && status == ports.AgentAuthStatusConfigured
}
