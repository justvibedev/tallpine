package review

import (
	"context"
	"log/slog"
	"runtime"
	"strings"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/envfilter"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/githubcreds"
	"tallpine.eu/tallpine/backend/internal/domain"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
)

// reviewerGitHub is what a reviewer launch needs to pick the project's account.
type reviewerGitHub struct {
	accounts sessionmanager.GitHubAccountTokens
	projects Projects
}

// WithGitHubAccounts runs reviewers as the gh account chosen for the project's
// repo, as worker sessions do. Without it a reviewer posts as whichever login
// is active machine-wide, which may not reach a private repo at all.
func WithGitHubAccounts(accounts sessionmanager.GitHubAccountTokens, projects Projects) LauncherOption {
	return func(l *agentLauncher) { l.github = reviewerGitHub{accounts: accounts, projects: projects} }
}

// applyGitHubAccountEnv puts the project's account token in a reviewer's env.
// The worker rule is shared (ApplyProjectGitHubToken). A policy that keeps
// tokens out of the environment wins: the reviewer then gets none from here.
func (l *agentLauncher) applyGitHubAccountEnv(ctx context.Context, spec LaunchSpec, env map[string]string) {
	if l.github.accounts == nil || l.github.projects == nil || spec.ProjectID == "" {
		return
	}
	decision := l.decide(ctx, spec)
	if githubcreds.Of(decision).KeepsTokensOutOfEnv() {
		return
	}
	windows := runtime.GOOS == "windows"
	load := func(ctx context.Context) (domain.ProjectRecord, error) {
		row, _, err := l.github.projects.GetProject(ctx, string(spec.ProjectID))
		return row, err
	}
	daemonPasses := func(key string) bool {
		out := envfilter.Of(decision)
		return !out.Allowlist() || len(out.Filter([]string{key + "=x"}, windows, nil).Keep) > 0
	}
	if err := sessionmanager.ApplyProjectGitHubToken(l.github.accounts, load, env, spec.ProjectEnv, windows, daemonPasses); err != nil {
		slog.Warn("github account token unavailable; reviewer keeps the default login", "project", spec.ProjectID, "review", spec.ReviewSessionID, "error", err)
	}
}

// applyGitGuard gives a reviewer the same git shim as every other session: see
// sessionmanager's applyGitGuard. It runs last so nothing moves the shim off the
// head of PATH.
func (l *agentLauncher) applyGitGuard(ctx context.Context, spec LaunchSpec, env map[string]string) {
	if l.github.projects == nil || spec.ProjectID == "" || l.executable == nil {
		return
	}
	row, _, err := l.github.projects.GetProject(ctx, string(spec.ProjectID))
	if err != nil || strings.TrimSpace(row.Path) == "" {
		return
	}
	exe, err := l.executable()
	if err != nil {
		slog.Warn("git shim unavailable for reviewer", "project", spec.ProjectID, "error", err)
		return
	}
	dir, err := agentlaunch.EnsureGitShim(exe, l.dataDir)
	if err != nil {
		slog.Warn("git shim unavailable for reviewer", "project", spec.ProjectID, "error", err)
		return
	}
	if dir == "" {
		return
	}
	env[agentlaunch.EnvGitGuardRoot] = row.Path
	env["PATH"] = prependPathDir(dir, env["PATH"])
}
