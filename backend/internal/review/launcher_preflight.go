package review

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Preflight checks whether the reviewer for the given harness can be launched
// without starting a runtime pane. It uses the same source of truth as Spawn:
// resolve the adapter, build the real ReviewCommand, and validate the
// executable. The only difference from Spawn is that Preflight stops before
// runtime.Create().
func (l *agentLauncher) Preflight(ctx context.Context, workerID domain.SessionID, harness domain.ReviewerHarness, workspacePath string, mode ...domain.ReviewerInterfaceMode) error {
	reviewer, ok := l.reviewers.Reviewer(harness)
	if !ok {
		return fmt.Errorf("no reviewer adapter for harness %q", harness)
	}
	if profile, ok := reviewer.(ports.ReviewerChatProfile); ok && (len(mode) == 0 || mode[0] != domain.ReviewerInterfaceTUI) && l.reviewChatSupported(profile) {
		return l.chat.PreflightReviewChat(ctx, profile.ReviewChatHarness())
	}
	cmd, err := reviewer.ReviewCommand(ctx, ports.ReviewInvocation{WorkspacePath: workspacePath})
	if err != nil {
		return fmt.Errorf("reviewer command: %w", err)
	}
	if len(cmd.Argv) == 0 {
		return fmt.Errorf("reviewer produced empty command")
	}
	// Unwrap any leading env KEY=value prefix so the real binary is validated.
	bin, ok := agentlaunch.LaunchBinary(cmd.Argv)
	if !ok {
		bin = cmd.Argv[0]
	}
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("reviewer binary %q not found: %w", bin, err)
	}
	authStatus, authKnown, err := l.preflightAuthStatus(ctx, workerID, harness, workspacePath)
	if err != nil {
		return err
	}
	if authKnown && authStatus == ports.AgentAuthStatusUnauthorized {
		return fmt.Errorf("reviewer harness %q: %w", harness, ports.ErrChatAuthRequired)
	}
	if pf, ok := reviewer.(preflightReviewer); ok {
		if err := pf.ReviewPreflight(ctx, workspacePath); err != nil {
			if authKnown && authStatus == ports.AgentAuthStatusAuthorized && looksLikeAuthFailure(err) {
				return nil
			}
			return err
		}
	}
	return nil
}

func (l *agentLauncher) reviewChatSupported(profile ports.ReviewerChatProfile) bool {
	return l.chat != nil && l.chat.SupportsReviewChat(profile.ReviewChatHarness())
}

// InterfaceMode returns the reviewer's native interaction surface.
func (l *agentLauncher) InterfaceMode(harness domain.ReviewerHarness) domain.ReviewerInterfaceMode {
	reviewer, ok := l.reviewers.Reviewer(harness)
	if !ok {
		return domain.ReviewerInterfaceTUI
	}
	profile, ok := reviewer.(ports.ReviewerChatProfile)
	if ok && l.reviewChatSupported(profile) {
		return domain.ReviewerInterfaceChat
	}
	return domain.ReviewerInterfaceTUI
}

func (l *agentLauncher) agentAuthStatus(ctx context.Context, harness domain.ReviewerHarness) (ports.AgentAuthStatus, bool, error) {
	if l.auth == nil {
		return "", false, nil
	}
	status, ok, err := l.auth.AuthStatus(ctx, harness)
	if err != nil {
		return "", false, fmt.Errorf("agent auth catalog for reviewer harness %q: %w", harness, err)
	}
	return status, ok, nil
}

func looksLikeAuthFailure(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, token := range []string{"auth", "login", "logged in", "credential", "token", "api key", "unauthor"} {
		if strings.Contains(msg, token) {
			return true
		}
	}
	return false
}

// reviewerHandleID is the stable runtime handle for a worker's reviewer pane, so
// one live reviewer is reused across passes.
func reviewerHandleID(workerID domain.SessionID) string {
	return "review-" + string(workerID)
}
