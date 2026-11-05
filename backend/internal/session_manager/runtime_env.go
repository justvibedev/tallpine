package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// spawnEnv builds the runtime environment: the per-project env vars first, then
// the Tallpine-internal vars last so they always win (a project cannot override
// TALLPINE_SESSION_ID and friends).
var envKeysCaseInsensitive = runtime.GOOS == "windows"

func spawnEnv(id domain.SessionID, project domain.ProjectID, issue domain.IssueID, dataDir string, projectEnv map[string]string) map[string]string {
	return spawnEnvForOS(id, project, issue, dataDir, projectEnv, envKeysCaseInsensitive)
}

func spawnEnvForOS(id domain.SessionID, project domain.ProjectID, issue domain.IssueID, dataDir string, projectEnv map[string]string, caseInsensitive bool) map[string]string {
	env := agentlaunch.MergeEnv(projectEnv, nil)
	setProtected := func(key, value string) {
		if caseInsensitive {
			for existing := range env {
				if strings.EqualFold(existing, key) {
					delete(env, existing)
				}
			}
		}
		env[key] = value
	}
	setProtected(EnvSessionID, string(id))
	setProtected(EnvProjectID, string(project))
	setProtected(EnvIssueID, string(issue))
	setProtected(EnvDataDir, dataDir)
	return env
}

// runtimeEnv is spawnEnv plus the hook PATH pin: the session's PATH puts the
// running daemon's own directory first, so the bare `tallpine` in workspace hook
// commands resolves to the daemon that installed them rather than whatever
// `tallpine` is first on the inherited PATH (e.g. a legacy CLI without the hooks
// command, which fails every callback and silently kills activity tracking).
// When the pin cannot be applied the inherited PATH is kept and a warning is
// logged so the degradation isn't silent.
func (m *Manager) runtimeEnv(id domain.SessionID, project domain.ProjectID, issue domain.IssueID, projectEnv map[string]string) map[string]string {
	env, err := m.runtimeEnvChecked(id, project, issue, projectEnv, m.accountOverride(id), "")
	if err != nil {
		m.logger.Warn("session agent account env incomplete", "session", id, "error", err)
	}
	return env
}

// runtimeEnvChecked is runtimeEnv that also reports why the session's account
// environment could not be resolved, for callers that must not go on under
// another login (an account move's preflight). A nonempty accountID replaces
// the session's binding.
func (m *Manager) runtimeEnvChecked(id domain.SessionID, project domain.ProjectID, issue domain.IssueID, projectEnv map[string]string, accountID string, harness domain.AgentHarness) (map[string]string, error) {
	return m.runtimeEnvFor(id, project, issue, projectEnv, accountID, true, harness)
}

// teardownRuntimeEnv is runtimeEnv for commands that run while a session is
// being torn down. It leaves out the scratch env so tearing a session down
// never recreates the temp directory its termination discards.
func (m *Manager) teardownRuntimeEnv(id domain.SessionID, project domain.ProjectID, issue domain.IssueID, projectEnv map[string]string) map[string]string {
	env, err := m.runtimeEnvFor(id, project, issue, projectEnv, m.accountOverride(id), false, "")
	if err != nil {
		m.logger.Warn("session agent account env incomplete", "session", id, "error", err)
	}
	return env
}

// runtimeEnvFor builds the env of a launch of harness; an empty harness means the stored session's own.
func (m *Manager) runtimeEnvFor(id domain.SessionID, project domain.ProjectID, issue domain.IssueID, projectEnv map[string]string, accountID string, scratch bool, harness domain.AgentHarness) (map[string]string, error) {
	env, err := m.runtimeEnvBase(id, project, issue, projectEnv, accountID, scratch, harness)
	// The gh shim goes first on PATH once the hook PATH is final, on every exit.
	prependGHShimToPATH(env, envKeysCaseInsensitive)
	return env, err
}

func (m *Manager) runtimeEnvBase(id domain.SessionID, project domain.ProjectID, issue domain.IssueID, projectEnv map[string]string, accountID string, scratch bool, harness domain.AgentHarness) (map[string]string, error) {
	caseInsensitive := envKeysCaseInsensitive
	env := spawnEnvForOS(id, project, issue, m.dataDir, projectEnv, caseInsensitive)
	if scratch {
		m.applyScratchEnv(env, id, project, projectEnv, caseInsensitive)
	}
	// Project configuration must never redirect Tallpine-owned hook callbacks to a
	// different daemon. New receives the resolved absolute path in production;
	// the environment fallback keeps focused embedders and tests compatible.
	runFilePath := m.agentRunFilePath()
	deleteProtectedEnv(env, EnvRunFile, caseInsensitive)
	if runFilePath != "" {
		env[EnvRunFile] = runFilePath
	}
	setProtectedEnv(env, EnvBrowserCapability, "", caseInsensitive)
	setProtectedEnv(env, EnvBrowserRuntimeToken, "", caseInsensitive)
	setProtectedEnv(env, EnvBrowserRuntimeTokenStdin, "", caseInsensitive)
	// Only launchRuntimeEnv mints a session token, bound to the launch it
	// rotates; project env can never supply one.
	deleteProtectedEnv(env, EnvSessionToken, caseInsensitive)
	deleteProtectedEnv(env, EnvSessionTokenRunFile, caseInsensitive)
	deleteProtectedEnv(env, EnvSessionAuth, caseInsensitive)
	deleteProtectedEnv(env, EnvSessionAuthRunFile, caseInsensitive)
	accountErr := m.applyAgentAccountEnv(id, accountID, env, caseInsensitive)
	if scratch {
		m.applyGitHubCredentials(id, project, env, projectEnv, caseInsensitive)
		m.applyEnvFilter(id, project, harness, env, caseInsensitive)
		m.applyFenceGitConfigFor(id, harness, env)
	}
	path, err := hookPATHForOS(m.executable, os.Getenv, projectEnv, m.dataDir, caseInsensitive)
	if err != nil {
		m.logger.Warn("session PATH not pinned to the daemon binary; `tallpine hooks` callbacks may resolve to a different tallpine and activity tracking will stall",
			"session", id, "error", err)
		return env, accountErr
	}
	setProtectedEnv(env, "PATH", path, caseInsensitive)
	return env, accountErr
}

// applyAgentAccountEnv runs each provider's CLI under the session's account.
// It wins over project env, because a project value would silently launch the
// agent under a different login than the one the session shows. A nonempty
// accountID replaces the session's binding. An error means the env may lack an
// account's variables.
func (m *Manager) applyAgentAccountEnv(id domain.SessionID, accountID string, env map[string]string, caseInsensitive bool) error {
	if m.agentAccounts == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var accountEnv map[string]string
	var err error
	if accountID != "" {
		accountEnv, err = m.agentAccounts.SessionLaunchEnvWith(ctx, id, accountID)
	} else {
		accountEnv, err = m.agentAccounts.SessionLaunchEnv(ctx, id)
	}
	var unset []string
	for key, value := range accountEnv {
		if value == "" && (key == "CLAUDE_CONFIG_DIR" || key == "CLAUDE_SECURESTORAGE_CONFIG_DIR") {
			// Claude reads an empty config dir as the working directory and writes
			// its history and transcripts into the worktree. Remove the variable.
			deleteProtectedEnv(env, key, caseInsensitive)
			unset = append(unset, key)
			continue
		}
		setProtectedEnv(env, key, value, caseInsensitive)
	}
	if len(unset) > 0 {
		agentlaunch.SetUnset(env, append(agentlaunch.UnsetNames(env), unset...))
	}
	return err
}

func deleteProtectedEnv(env map[string]string, key string, caseInsensitive bool) {
	if !caseInsensitive {
		delete(env, key)
		return
	}
	for existing := range env {
		if strings.EqualFold(existing, key) {
			delete(env, existing)
		}
	}
}

func setProtectedEnv(env map[string]string, key, value string, caseInsensitive bool) {
	deleteProtectedEnv(env, key, caseInsensitive)
	env[key] = value
}

func hookPATHForOS(executable func() (string, error), getenv func(string) string, projectEnv map[string]string, dataDir string, caseInsensitive bool) (string, error) {
	if !caseInsensitive {
		return HookPATH(executable, getenv, projectEnv, dataDir)
	}
	if _, ok := projectEnv["PATH"]; ok {
		return HookPATH(executable, getenv, projectEnv, dataDir)
	}
	for key, value := range projectEnv {
		if strings.EqualFold(key, "PATH") {
			next := make(map[string]string, len(projectEnv)+1)
			for k, v := range projectEnv {
				next[k] = v
			}
			next["PATH"] = value
			return HookPATH(executable, getenv, next, dataDir)
		}
	}
	return HookPATH(executable, getenv, projectEnv, dataDir)
}

// pinRuntimePermissionEnv exposes the session's Tallpine approval policy to hook
// commands so adapters like Cursor can decide whether a tool attempt needs
// user approval before returning a native permission response.
func pinRuntimePermissionEnv(env map[string]string, mode domain.PermissionMode) {
	if env == nil {
		return
	}
	env[EnvPermissionMode] = string(ports.NormalizePermissionMode(mode))
}

// pinPromptCacheTTL sets CLAUDE_CODE_PROMPT_CACHE_TTL for claude-code sessions
// from the project's per-role setting (workers and orchestrators; the reviewer
// role applies only to native Tallpine review runs). A value already set in the
// project or daemon environment is kept.
func pinPromptCacheTTL(env map[string]string, harness domain.AgentHarness, kind domain.SessionKind, cfg domain.ProjectConfig) {
	if env == nil || harness != domain.HarnessClaudeCode {
		return
	}
	role := "worker"
	if kind == domain.KindOrchestrator {
		role = "orchestrator"
	}
	if _, set := cfg.Env[domain.EnvPromptCacheTTL]; set || os.Getenv(domain.EnvPromptCacheTTL) != "" {
		return
	}
	if ttl := cfg.PromptCacheTTLFor(role); ttl != "" {
		env[domain.EnvPromptCacheTTL] = ttl
	}
}

// pinClaudeRoleSettings hands a claude-code Chat session its role's plugin and
// auto-memory overrides (claudePlugins, claudeAutoMemory); the chat driver
// merges them into the session's SDK settings. A role with none sets nothing.
func pinClaudeRoleSettings(env map[string]string, harness domain.AgentHarness, kind domain.SessionKind, cfg domain.ProjectConfig) {
	delete(env, domain.EnvClaudeRoleSettings)
	if env == nil || harness != domain.HarnessClaudeCode {
		return
	}
	if settings := cfg.ClaudeRoleSettingsJSON(domain.SessionRoleFor(kind)); settings != "" {
		env[domain.EnvClaudeRoleSettings] = settings
	}
}

func (m *Manager) launchRuntimeEnv(id domain.SessionID, project domain.ProjectID, issue domain.IssueID, projectEnv map[string]string) (map[string]string, string, error) {
	return m.launchRuntimeEnvAs(id, project, issue, projectEnv, "")
}

// launchRuntimeEnvAs is launchRuntimeEnv for a launch of harness, which can differ
// from the stored session's while a provider switch is in flight.
func (m *Manager) launchRuntimeEnvAs(id domain.SessionID, project domain.ProjectID, issue domain.IssueID, projectEnv map[string]string, harness domain.AgentHarness) (map[string]string, string, error) {
	env, accErr := m.runtimeEnvChecked(id, project, issue, projectEnv, m.accountOverride(id), harness)
	if accErr != nil {
		m.logger.Warn("session agent account env incomplete", "session", id, "error", accErr)
	}
	if m.browserCapabilities == nil {
		return env, "", nil
	}
	token, verifier, err := m.browserCapabilities.Issue(id)
	if err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(token) == "" || strings.TrimSpace(verifier) == "" {
		return nil, "", errors.New("browser capability issuer returned an empty credential")
	}
	env[EnvBrowserCapability] = token
	if m.sessionTokens != nil {
		// Valid once the caller persists verifier, before the agent starts.
		if sessionToken := m.sessionTokens.IssueSession(id, verifier); sessionToken != "" && env[EnvRunFile] != "" {
			env[EnvSessionToken] = sessionToken
			env[EnvSessionTokenRunFile] = env[EnvRunFile]
			env[EnvSessionAuth] = sessionToken
			env[EnvSessionAuthRunFile] = env[EnvRunFile]
		}
	}
	return env, verifier, nil
}

// prepareWorkerLaunchEnv couples capability issuance with durable verifier
// persistence. Callers receive the bearer only after authorization can validate
// it, so an eager worker cannot race its own launch bookkeeping.
func (m *Manager) prepareWorkerLaunchEnv(
	ctx context.Context,
	rec domain.SessionRecord,
	projectEnv map[string]string,
) (domain.SessionRecord, map[string]string, error) {
	env, verifier, err := m.launchRuntimeEnv(rec.ID, rec.ProjectID, rec.IssueID, projectEnv)
	if err != nil {
		return rec, nil, err
	}
	rec, err = m.persistBrowserCapabilityVerifier(ctx, rec, rec.ControllerOwner(), verifier)
	if err != nil {
		return rec, nil, fmt.Errorf("persist browser capability verifier: %w", err)
	}
	return rec, env, nil
}

// prepareChatControllerEnv is the Chat launch variant: the service supplies the
// exact durable owner selected under its controller gate, while Session Manager
// retains responsibility for issuing and persisting the split credential.
func (m *Manager) prepareChatControllerEnv(
	ctx context.Context,
	rec domain.SessionRecord,
	project domain.ProjectConfig,
	expected domain.SessionControllerOwner,
	harness domain.AgentHarness,
) (domain.SessionRecord, map[string]string, error) {
	env, verifier, err := m.launchRuntimeEnvAs(rec.ID, rec.ProjectID, rec.IssueID, project.Env, harness)
	if err != nil {
		return rec, nil, err
	}
	pinClaudeRoleSettings(env, harness, rec.Kind, project)
	rec, err = m.persistBrowserCapabilityVerifier(ctx, rec, expected, verifier)
	if err != nil {
		return rec, nil, fmt.Errorf("persist browser capability verifier: %w", err)
	}
	return rec, env, nil
}

// persistBrowserCapabilityVerifier runs before the worker controller starts.
// This closes the launch race where an eager worker could present its freshly
// injected token before the daemon had stored the verifier needed to validate
// it. The bearer token remains only in the runtime environment.
func (m *Manager) persistBrowserCapabilityVerifier(
	ctx context.Context,
	rec domain.SessionRecord,
	expected domain.SessionControllerOwner,
	verifier string,
) (domain.SessionRecord, error) {
	if verifier == "" {
		return rec, nil
	}
	applied, err := m.store.UpdateBrowserCapabilityVerifier(ctx, rec.ID, expected, verifier)
	if err != nil {
		return rec, err
	}
	if !applied {
		return rec, errors.New("session controller ownership changed before browser capability rotation")
	}
	rec.Metadata.BrowserCapabilityVerifier = verifier
	return rec, nil
}

func chatControllerOwner(
	rec domain.SessionRecord,
	harness domain.AgentHarness,
	providerConversationID string,
	controllerGeneration string,
) domain.SessionControllerOwner {
	owner := rec.ControllerOwner()
	owner.Harness = harness
	owner.Mode = domain.SessionModeChat
	owner.IsTerminated = false
	owner.RuntimeLaunchID = ""
	owner.ProviderConversationID = providerConversationID
	owner.ControllerGeneration = controllerGeneration
	return owner
}

// HookPATH builds the PATH value pinned into a spawned session: the daemon
// executable's directory prepended to the base PATH (the project's PATH
// override when set, else the daemon's inherited PATH — matching what the
// runtime would have exported anyway). An error means the pin cannot be
// applied: the executable is unresolvable, or is not named "tallpine", in which case
// prepending its directory would not change what `tallpine` resolves to. Exported so
// the reviewer launcher can pin its pane's PATH the same way.
func HookPATH(executable func() (string, error), getenv func(string) string, projectEnv map[string]string, dataDir string) (string, error) {
	return agentlaunch.PinnedPATH(executable, getenv, projectEnv, dataDir)
}

// agentRunFilePath is the run file an agent's environment names: the manager's
// own, else the daemon process's (focused embedders and tests).
func (m *Manager) agentRunFilePath() string {
	if m.runFilePath != "" {
		return m.runFilePath
	}
	return strings.TrimSpace(os.Getenv(EnvRunFile))
}
