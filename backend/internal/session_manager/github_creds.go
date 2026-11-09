package sessionmanager

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/githubcreds"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/sessionscratch"
)

const githubCredsDecideTimeout = 5 * time.Second

// githubCredsFor is the githubCreds outcome of a session's project. The env
// builder has only ids, not the launch's Decision, so it asks the engine for the
// same project-scoped decision the launch made.
func (m *Manager) githubCredsFor(id domain.SessionID, project domain.ProjectID) githubcreds.Outcome {
	if m.policy == nil {
		return githubcreds.Outcome{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), githubCredsDecideTimeout)
	defer cancel()
	return githubcreds.Of(m.policy.Decide(ctx, agentpolicy.SessionRef{ID: id, Project: project}))
}

// applyGitHubCredentials gives the session its GitHub access the way the policy
// says. Mode env (the default) is today's behaviour: the project's gh account
// token goes into the environment. Modes helper and none keep tokens out of it:
// no auto-picked token, the daemon's own inherited tokens withheld, a private gh
// config directory (so gh's stored login is not available; in helper mode a
// harness that bypasses the shim also gets the project account's login there),
// and, in helper mode, git's credential helper and the gh shim, each asking the
// daemon per use.
func (m *Manager) applyGitHubCredentials(id domain.SessionID, project domain.ProjectID, env, projectEnv map[string]string, caseInsensitive bool) {
	creds := m.githubCredsFor(id, project)
	if !creds.KeepsTokensOutOfEnv() {
		m.applyGitHubAccountEnv(project, env, projectEnv, caseInsensitive)
		return
	}
	if creds.UsesHelper() && m.hostOS() == "windows" {
		// The shim and helper are not implemented for Windows yet. Falling back to
		// the token in the environment keeps GitHub working, and says so. Only the
		// helper falls back: mode none is an explicit deny and must never gain a
		// token, on any OS.
		m.logger.Warn("github credential helper is not supported on Windows yet; the GitHub token stays in the session environment", "session", id)
		m.applyGitHubAccountEnv(project, env, projectEnv, caseInsensitive)
		return
	}
	agentlaunch.SetUnset(env, creds.WithheldVariables())
	if !envHasKey(projectEnv, "GH_CONFIG_DIR", caseInsensitive) {
		if dir := m.sessionGHConfigDir(id); dir != "" {
			setProtectedEnv(env, "GH_CONFIG_DIR", dir, caseInsensitive)
			// Whatever an earlier launch of this session left is gone first: the
			// directory outlives a relaunch, and mode none must never keep a login.
			_ = os.Remove(filepath.Join(dir, "hosts.yml"))
			if creds.UsesHelper() && m.shimCanBeBypassed(id) {
				m.writeGHHosts(dir, project, projectEnv, caseInsensitive)
			}
		}
	}
	helperCommand := ""
	if creds.UsesHelper() {
		exe, err := m.githubHelperExecutable()
		if err != nil {
			// Fail closed: no helper and no token. git and gh are simply not
			// authenticated, and the daemon log says why.
			m.logger.Warn("github credential helper unavailable; this session has no GitHub access", "session", id, "error", err)
		} else {
			helperCommand = shellQuote(exe) + " git-credential"
			if shimDir, err := m.ensureGHShim(exe); err != nil {
				m.logger.Warn("gh shim unavailable; gh in this session is not authenticated", "session", id, "error", err)
			} else {
				setProtectedEnv(env, agentlaunch.EnvGHShimDir, shimDir, caseInsensitive)
			}
		}
	}
	for key, value := range githubcreds.GitConfigEnv(env, helperCommand, m.githubHosts(project)) {
		setProtectedEnv(env, key, value, caseInsensitive)
	}
}

// githubHosts are the hosts git credentials are scoped to: github.com and the
// host of the project's remote (GitHub Enterprise).
func (m *Manager) githubHosts(project domain.ProjectID) []string {
	hosts := []string{"github.com"}
	if project == "" {
		return hosts
	}
	ctx, cancel := context.WithTimeout(context.Background(), githubAccountEnvTimeout)
	defer cancel()
	if row, err := m.loadProject(ctx, project); err == nil {
		// The origin host stays: checkout and push remain on origin after a move.
		for _, remote := range []string{row.RepoOriginURL, row.Config.CredentialRepoURL(row.RepoOriginURL)} {
			if host := githubcreds.HostOf(remote); host != "" && !slices.Contains(hosts, host) {
				hosts = append(hosts, host)
			}
		}
	}
	return hosts
}

func (m *Manager) githubHelperExecutable() (string, error) {
	if m.executable == nil {
		return "", fmt.Errorf("daemon executable is unknown")
	}
	exe, err := m.executable()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(exe) {
		if exe, err = filepath.Abs(exe); err != nil {
			return "", err
		}
	}
	return exe, nil
}

// sessionGHConfigDir is the per-session gh config directory (inside the
// session's scratch directory, removed with it). It holds none of the user's
// stored logins; the only login it can hold is the one writeGHHosts adds.
func (m *Manager) sessionGHConfigDir(id domain.SessionID) string {
	if m.dataDir == "" {
		return ""
	}
	tmp, err := sessionscratch.EnsureTempDir(m.dataDir, id)
	if err != nil {
		m.logger.Warn("github session config dir unavailable; gh may use the user's stored login", "session", id, "error", err)
		return ""
	}
	dir := filepath.Join(tmp, "gh-config")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		m.logger.Warn("github session config dir unavailable; gh may use the user's stored login", "session", id, "error", err)
		return ""
	}
	return dir
}

// shimCanBeBypassed reports whether the session's harness runs its commands in
// a way that puts the real gh ahead of the shim. Claude Code keeps the PATH the
// daemon built, so its shim always wins and it gets no login file. Any other
// harness, and an unknown one, runs commands through the user's shell.
func (m *Manager) shimCanBeBypassed(id domain.SessionID) bool {
	h := m.sessionHarness(id)
	return h != "" && h != string(domain.HarnessClaudeCode)
}

// writeGHHosts gives the real `gh` a login inside the session's own gh config
// directory, for harnesses that bypass the shim (see shimCanBeBypassed). The gh
// shim only works while it is first on PATH, and the shells of
// Codex and Kiro re-run the user's rc files, which put the real gh (for example
// /opt/homebrew/bin) back in front of it; that gh then has no login at all. The
// file lives in the session's private temp dir (mode 0700, file 0600; removed with
// the session), never in the environment, the worktree or the user's home. It is
// rewritten at every launch, and removed when no account token is available. A
// token the project's own environment sets keeps winning, then one the daemon
// pins (TALLPINE_GITHUB_TOKEN), then the account: the order the helper uses.
func (m *Manager) writeGHHosts(dir string, project domain.ProjectID, projectEnv map[string]string, caseInsensitive bool) {
	path := filepath.Join(dir, "hosts.yml")
	if m.githubAccounts == nil || project == "" {
		return
	}
	for _, key := range explicitGitHubTokenEnv {
		if v, ok := lookupEnv(projectEnv, key, caseInsensitive); ok && strings.TrimSpace(v) != "" {
			return
		}
	}
	// Same order as githubcred.Service.token: a token the daemon pins explicitly
	// (narrow by intent) beats the project's account, so the real gh is never
	// given a broader token than the helper would serve.
	pinned := strings.TrimSpace(os.Getenv("TALLPINE_GITHUB_TOKEN"))
	var row domain.ProjectRecord
	if pinned == "" {
		ctx, cancel := context.WithTimeout(context.Background(), githubAccountEnvTimeout)
		defer cancel()
		var err error
		if row, err = m.loadProject(ctx, project); err != nil || row.RepoOriginURL == "" {
			return
		}
	}
	var b strings.Builder
	for _, host := range m.githubHosts(project) {
		token := pinned
		if token == "" {
			// The account is resolved per host so a token for a moved-to repo is
			// never written under the old origin host.
			ctx, cancel := context.WithTimeout(context.Background(), githubAccountEnvTimeout)
			remote, ok := row.Config.AccountRemoteForHost(row.RepoOriginURL, host)
			var t string
			var err error
			if ok {
				t, err = m.githubAccounts.TokenForProject(ctx, remote, row.Config.GitHubUser)
			}
			cancel()
			if token = strings.TrimSpace(t); !ok || err != nil || token == "" {
				continue
			}
		}
		b.WriteString(host + ":\n    oauth_token: " + shellQuote(token) + "\n    git_protocol: https\n")
	}
	if b.Len() == 0 {
		return
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		m.logger.Warn("gh login for the session unavailable; gh may be unauthenticated", "project", project, "error", err)
	}
}

// ensureGHShim writes the `gh` shim (it runs `tallpine gh-exec`) into a directory
// shared by sessions of the same daemon binary and returns the directory.
func (m *Manager) ensureGHShim(exe string) (string, error) {
	if m.dataDir == "" {
		return "", fmt.Errorf("data directory is required")
	}
	sum := sha256.Sum256([]byte(exe))
	dir := filepath.Join(m.dataDir, "runtime", "gh-shim", fmt.Sprintf("%x", sum[:8]))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	script := "#!/bin/sh\nexec " + shellQuote(exe) + ` gh-exec -- "$@"` + "\n"
	path := filepath.Join(dir, "gh")
	if existing, err := os.ReadFile(path); err == nil && string(existing) == script {
		return dir, nil
	}
	tmp, err := os.CreateTemp(dir, ".gh-*")
	if err != nil {
		return "", err
	}
	if _, err := tmp.WriteString(script); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	if err := os.Chmod(tmp.Name(), 0o700); err != nil { // #nosec G302 -- the shim must be executable by the Tallpine user.
		_ = os.Remove(tmp.Name())
		return "", err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	return dir, nil
}

// prependGHShimToPATH puts the gh shim directory first on the session's PATH,
// after the hook PATH is final, so `gh` resolves to the shim.
func prependGHShimToPATH(env map[string]string, caseInsensitive bool) {
	dir, ok := lookupEnv(env, agentlaunch.EnvGHShimDir, caseInsensitive)
	if !ok || dir == "" {
		return
	}
	path, _ := lookupEnv(env, "PATH", caseInsensitive)
	if path == "" {
		setProtectedEnv(env, "PATH", dir, caseInsensitive)
		return
	}
	setProtectedEnv(env, "PATH", dir+string(os.PathListSeparator)+path, caseInsensitive)
}

// githubFacts is what the decision record says about GitHub credentials: only
// that a token the project's own environment sets keeps winning, by variable
// name (never its value).
func githubFacts(decision agentpolicy.Decision, projectEnv map[string]string) map[string]map[string]any {
	if !githubcreds.Of(decision).KeepsTokensOutOfEnv() {
		return nil
	}
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN", "TALLPINE_GITHUB_TOKEN"} {
		if strings.TrimSpace(projectEnv[name]) != "" {
			return map[string]map[string]any{githubcreds.Name: {"explicitProjectToken": name}}
		}
	}
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

// hostOS is the operating system the session launches on; tests override it.
func (m *Manager) hostOS() string {
	if m.goos != "" {
		return m.goos
	}
	return runtime.GOOS
}
