package ports

import (
	"context"
	"errors"

	"tallpine.eu/tallpine/backend/internal/domain"
)

var (
	// ErrUnsupportedEffort reports a value the selected model did not advertise.
	ErrUnsupportedEffort = errors.New("unsupported model effort")
	// ErrModelCapabilitiesUnavailable reports tuning that cannot be validated safely.
	ErrModelCapabilitiesUnavailable = errors.New("model capabilities unavailable")
	// ErrAgentAuthRequired means a launch-fresh provider check rejected the
	// credential selected by the session's cwd and environment.
	ErrAgentAuthRequired = errors.New("agent requires authentication")
	// ErrNativeHistoryUnavailable means an interactive native restorer has no
	// saved conversation yet; ordinary restore may launch fresh in its workspace.
	ErrNativeHistoryUnavailable = errors.New("agent native history unavailable")
)

// ErrAgentBinaryNotFound is returned by agent adapters when neither PATH nor
// any well-known install location holds the agent's binary. The session
// manager surfaces this BEFORE creating the runtime so a missing CLI doesn't
// silently launch into an empty tmux pane that the reaper later mistakes
// for a live session.
var ErrAgentBinaryNotFound = errors.New("agent: binary not found on PATH")

// ErrAgentBinaryIdentityUnknown is returned by a startup-only presence check
// when a name-matching executable exists but the adapter's identity probe has
// not confirmed it. It is deliberately distinct from ErrAgentBinaryNotFound:
// callers must not present an unverified name-only match as installed.
var ErrAgentBinaryIdentityUnknown = errors.New("agent: binary identity unknown")

// ErrAgentModelDiscoverySignInRequired is returned by model discovery when the
// agent's model-list command would start an interactive sign-in because the
// CLI is not confirmed as signed in. It is not a discovery failure: callers
// keep the last catalog and retry once the agent reports a login.
var ErrAgentModelDiscoverySignInRequired = errors.New("agent: sign-in required to list models")

// ErrAgentModelDiscoveryCredentialRejected marks a discovery failure caused by
// the provider definitively rejecting the agent's credential. The catalog
// service reports it as an auth problem so clients can offer a login instead
// of a raw provider error.
var ErrAgentModelDiscoveryCredentialRejected = errors.New("agent: credential rejected while listing models")

// ErrAgentModelDiscoveryCredentialExpired marks a discovery failure caused by
// a stored login whose access token expired while a refresh token remains.
// The agent CLI renews such a login on its next run; it is not a sign-out.
var ErrAgentModelDiscoveryCredentialExpired = errors.New("agent: stored login expired while listing models")

// AgentAuthStatus describes the result of a short local auth probe for an
// installed agent. It is advisory only: credentials, quota, selected model
// availability, or CLI state can still fail at session spawn/model-call time.
type AgentAuthStatus string

const (
	// AgentAuthStatusAuthorized means the local auth probe recently passed.
	// It does not guarantee that a later spawn or model call will succeed.
	AgentAuthStatusAuthorized AgentAuthStatus = "authorized"
	// AgentAuthStatusUnauthorized means the agent is installed but its local
	// auth probe reported missing or invalid authentication.
	AgentAuthStatusUnauthorized AgentAuthStatus = "unauthorized"
	// AgentAuthStatusUnknown means the daemon could not determine auth status.
	AgentAuthStatusUnknown AgentAuthStatus = "unknown"
	// AgentAuthStatusConfigured means a credential is present locally but no
	// check has proven it valid. Presence is not validity: a key can be
	// revoked, downgraded, or rate-limited with no change on disk, so only a
	// provider round-trip may report AgentAuthStatusAuthorized. Local evidence
	// (a config file, an env var, or a CLI that reports loggedIn) reports
	// configured instead, and must never render as a ready state.
	AgentAuthStatusConfigured AgentAuthStatus = "configured"
)

// HasCredential reports whether a check found a credential, verified or only
// configured.
func (s AgentAuthStatus) HasCredential() bool {
	return s == AgentAuthStatusAuthorized || s == AgentAuthStatusConfigured
}

// Agent is the contract every CLI coding agent adapter (claude-code, codex, …)
// must satisfy. It supplies the argv and process configuration the Session
// Manager needs to launch, restore, and read back a native agent session.
type Agent interface {
	// GetConfigSpec describes the agent-specific config keys Tallpine can
	// expose to users in the Tallpine config.
	GetConfigSpec(ctx context.Context) (ConfigSpec, error)

	// GetLaunchCommand builds the argv Tallpine should run to start this agent.
	GetLaunchCommand(ctx context.Context, cfg LaunchConfig) (cmd []string, err error)

	// GetPromptDeliveryStrategy tells Tallpine whether the prompt is included in
	// the launch command or must be sent after the agent process starts.
	GetPromptDeliveryStrategy(ctx context.Context, cfg LaunchConfig) (PromptDeliveryStrategy, error)

	// GetAgentHooks installs or merges Tallpine hooks into the agent's
	// native workspace-local hook config. It must preserve user-defined hooks.
	GetAgentHooks(ctx context.Context, cfg WorkspaceHookConfig) error

	// GetRestoreCommand builds an argv that continues an existing native agent
	// session. ok=false means no existing native session can be continued.
	GetRestoreCommand(ctx context.Context, cfg RestoreConfig) (cmd []string, ok bool, err error)

	// SessionInfo reads agent-owned session metadata such as native session id,
	// display title, or summary. ok=false means no info is available.
	SessionInfo(ctx context.Context, session SessionRef) (info SessionInfo, ok bool, err error)
}

// AgentAuthChecker is the optional capability for adapters whose native CLI has
// a cheap local authentication status probe.
type AgentAuthChecker interface {
	AuthStatus(ctx context.Context) (AgentAuthStatus, error)
}

// AgentLaunchAuthValidator is the optional launch gate for adapters whose
// credential source depends on the project cwd or environment. Implementations
// must perform a fresh provider check instead of trusting display/readiness
// caches; unknown or uncheckable credentials remain advisory.
type AgentLaunchAuthValidator interface {
	ValidateLaunchAuth(ctx context.Context, workingDir string, env map[string]string) (AgentAuthStatus, error)
}

// AgentBinaryResolver is the optional capability adapters expose when their
// binary can be checked without constructing a real session launch command.
type AgentBinaryResolver interface {
	ResolveBinary(ctx context.Context) (path string, err error)
}

// StateFencer is implemented by agent and chat adapters that can run a
// session inside the protectState fence on goos. An adapter without it cannot.
type StateFencer interface {
	FencesState(goos string) bool
}

// LaunchStateFencer is implemented by adapters whose fence depends on the
// launch's permission mode (Codex can fence only a launch with no inner
// sandbox, since macOS refuses nested sandboxes). It takes precedence over
// StateFencer.
type LaunchStateFencer interface {
	FencesLaunch(goos string, permissions PermissionMode) bool
}

// AgentRuntimeLaunchEnv augments a terminal launch after its generation has
// been assigned. Native reporters can then carry that generation in their
// own protocol identity. Implementations only modify the supplied environment.
type AgentRuntimeLaunchEnv interface {
	AugmentRuntimeLaunchEnv(env map[string]string, dataDir string, sessionID domain.SessionID, launchID string)
}

// AgentTerminalVersionRefresher opts a terminal adapter into unattended upgrades.
// GetRestoreCommand must name the exact captured conversation, preserve its
// model/provider, instructions and permission options, and never silently fall
// back to a fresh conversation when that identity cannot be resumed. This is
// independent of TUI/Chat handoff support. Absence means not audited for refresh.
type AgentTerminalVersionRefresher interface {
	SupportsTerminalVersionRefresh() bool
}

// AgentBinaryResolutionInvalidator is an optional capability for adapters that
// cache the executable path. Install and reinstall flows use it to make the
// next readiness, model-discovery, or launch operation resolve the current
// local installation again.
type AgentBinaryResolutionInvalidator interface {
	InvalidateBinaryResolution()
}

// AgentBinaryPresenceResolver is an optional startup-only refinement for an
// adapter whose normal binary resolution performs additional validation. It
// must only inspect local executable paths; it must not start the agent CLI.
// Tallpine uses it for the first-render prerequisite gate. Identity-sensitive
// adapters may return ErrAgentBinaryIdentityUnknown when existence alone is
// insufficient; that result remains unknown until a normal identity probe.
type AgentBinaryPresenceResolver interface {
	ResolveBinaryPresence(ctx context.Context) (path string, err error)
}

// AgentReadinessProvider is the daemon-owned coordination boundary used by
// launch and policy consumers. Implementations coalesce native checks and keep
// the resulting snapshots in memory.
type AgentReadinessProvider interface {
	EnsureAgentReadiness(ctx context.Context, agentID string, purpose domain.AgentReadinessPurpose) (domain.AgentReadinessSnapshot, error)
	InvalidateAgentInstallation(agentID string)
	InvalidateAgentAuthentication(agentID string)
	RecheckAgent(agentID string)
}

// AgentNativeSessionTerminator is an optional adapter capability used before
// Tallpine destroys a terminal runtime or worktree whose agent may keep running in a
// detached native process. Implementations must affect only the supplied
// session and leave its transcript resumable.
type AgentNativeSessionTerminator interface {
	TerminateNativeSession(ctx context.Context, session SessionRef) error
}

// AgentInterfaceHandoff is an OPTIONAL capability for a TUI adapter whose
// native resume identity is also understood by its structured Chat driver.
// Merely supporting GetRestoreCommand is not enough: some harnesses expose a
// different identifier through their TUI and protocol surfaces.
type AgentInterfaceHandoff interface {
	NativeConversationID(
		ctx context.Context,
		session SessionRef,
		currentMode domain.SessionMode,
		providerConversationID string,
	) (id string, ok bool, err error)
}

// AgentInterfaceHandoffHistoryProbe is an OPTIONAL refinement for adapters
// that reserve a native conversation id before the provider has persisted any
// history. A missing history record means an interface transition may safely
// start the target fresh: there is no provider context to carry. Without this
// capability, Session Manager conservatively treats every declared id as an
// existing conversation and requires a native resume.
type AgentInterfaceHandoffHistoryProbe interface {
	NativeConversationExists(
		ctx context.Context,
		session SessionRef,
		nativeConversationID string,
		env map[string]string,
	) (bool, error)
}

// AgentInterfaceHandoffStorageBridge is an OPTIONAL capability for adapters
// whose native conversation files live in interface-specific storage roots.
// Session Manager calls it only after the source controller is stopped.
type AgentInterfaceHandoffStorageBridge interface {
	PrepareInterfaceHandoffStorage(
		ctx context.Context,
		session SessionRef,
		from, to domain.SessionMode,
		nativeConversationID string,
		env map[string]string,
	) error
}

// FencesState reports whether adapter (an agent or chat adapter, possibly nil)
// can run a session inside the protectState fence on goos. An adapter that does
// not implement StateFencer cannot.
func FencesState(adapter any, goos string) bool {
	f, ok := adapter.(StateFencer)
	return ok && f.FencesState(goos)
}

// FencesLaunch reports whether adapter can run a launch in permissions (the
// post-cap mode the launch actually uses) inside the protectState fence on
// goos. An adapter without LaunchStateFencer answers FencesState.
func FencesLaunch(adapter any, goos string, permissions PermissionMode) bool {
	if f, ok := adapter.(LaunchStateFencer); ok {
		return f.FencesLaunch(goos, permissions)
	}
	return FencesState(adapter, goos)
}
