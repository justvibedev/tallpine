package review

import (
	"context"
	"log/slog"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// ReviewerLaunchAuth checks a Claude login in the exact environment a reviewer
// will launch with (ports.AgentLaunchAuthValidator).
type ReviewerLaunchAuth interface {
	ValidateLaunchAuth(ctx context.Context, workingDir string, env map[string]string) (ports.AgentAuthStatus, error)
}

// WithReviewerLaunchAuth makes reviewer preflight check the worker's agent
// account instead of the device login.
func WithReviewerLaunchAuth(auth ReviewerLaunchAuth) LauncherOption {
	return func(l *agentLauncher) { l.launchAuth = auth }
}

// preflightAuthStatus is the auth verdict reviewer preflight acts on. A Claude
// reviewer whose worker runs on an agent account is checked in that account's
// environment; everything else (device-bound worker, other harnesses, no
// validator wired) uses the device-wide readiness. Only the typed Chat
// preflight stays device-scoped, as no Claude reviewer uses Chat.
func (l *agentLauncher) preflightAuthStatus(ctx context.Context, workerID domain.SessionID, harness domain.ReviewerHarness, workspacePath string) (ports.AgentAuthStatus, bool, error) {
	if harness == domain.ReviewerClaudeCode && l.launchAuth != nil {
		account, err := l.claudeAccountEnv(ctx, workerID)
		if err != nil {
			return "", false, err
		}
		if len(account) > 0 {
			status, authErr := l.launchAuth.ValidateLaunchAuth(ctx, workspacePath, account)
			if authErr != nil {
				// Inconclusive probes never block, as in the worker launch.
				slog.Debug("reviewer launch authentication probe inconclusive; continuing", "worker", workerID, "err", authErr)
				return "", false, nil
			}
			return status, true, nil
		}
	}
	return l.agentAuthStatus(ctx, harness)
}
