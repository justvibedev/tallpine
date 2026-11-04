package ports

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// MetadataKeyAgentSessionID is the SessionRef.Metadata key that carries an
// agent's native session id. It matches the json tag on
// domain.SessionMetadata.AgentSessionID and the key the adapters read, so the
// Session Manager can bridge its typed metadata onto a SessionRef without
// either side hard-coding the other's vocabulary.
const MetadataKeyAgentSessionID = "agentSessionId"

// MetadataKeySessionMode is the SessionRef.Metadata key that carries the
// current session mode (e.g. "chat" or "tui").
const MetadataKeySessionMode = "sessionMode"

// MetadataKeyTitle and MetadataKeySummary are the SessionRef.Metadata keys
// carrying a session's human title and one-line summary. They are the shared
// vocabulary every adapter reports under, so the dashboard renders agents
// uniformly.
const (
	MetadataKeyTitle   = "title"
	MetadataKeySummary = "summary"
)

// AgentConfig is the typed per-project agent config handed to adapters at
// launch. It aliases domain.AgentConfig so storage, services, and adapters
// share one definition without a translation layer.
type AgentConfig = domain.AgentConfig

// ConfigSpec describes the agent-specific config keys Tallpine can expose to users.
type ConfigSpec struct {
	Fields []ConfigField
}

// ConfigField describes one user-facing agent config key.
type ConfigField struct {
	Key         string
	Type        ConfigFieldType
	Description string
	Required    bool
	Default     any
	Enum        []string
}

// ConfigFieldType is the primitive value kind Tallpine expects for a field.
type ConfigFieldType string

// The primitive value kinds a ConfigField can declare.
const (
	ConfigFieldString     ConfigFieldType = "string"
	ConfigFieldBool       ConfigFieldType = "bool"
	ConfigFieldNumber     ConfigFieldType = "number"
	ConfigFieldStringList ConfigFieldType = "string_list"
	ConfigFieldEnum       ConfigFieldType = "enum"
)

// LaunchConfig carries inputs needed to build a new agent launch command.
type LaunchConfig struct {
	// Policy is the agent policy decision for this launch, computed once by the
	// session manager. The zero value restricts nothing.
	Policy      agentpolicy.Decision
	Config      AgentConfig
	DataDir     string
	IssueID     string
	Kind        domain.SessionKind
	Permissions PermissionMode
	Prompt      string
	SessionID   string
	// NativeSessionID optionally asks an adapter that supports caller-assigned
	// native identities to use this id for a fresh provider conversation. It is
	// deliberately separate from SessionID: one stable Tallpine session may create
	// several provider-native conversations over its lifetime. Adapters whose
	// CLI assigns native ids ignore this field and report the id through hooks.
	NativeSessionID string
	// AllowedTools and DisallowedTools scope the agent to a tool allowlist when
	// it runs in a non-bypass permission mode (allow rules auto-approve, deny
	// rules auto-reject). They are the enforced read-only guarantee the reviewer
	// relies on: bypassPermissions ignores both lists, so a restricted launch
	// must leave Permissions off bypass. Empty means no restriction, so worker
	// sessions are unaffected.
	AllowedTools     []string
	DisallowedTools  []string
	SystemPrompt     string
	SystemPromptFile string
	WorkspacePath    string
	// Env is the environment the runtime layers over the daemon's own for this
	// launch (the project env plus Tallpine's variables). The session manager sets it
	// for PreLaunch, so an adapter prepares the provider config the launched
	// process will actually read (for example a project CLAUDE_CONFIG_DIR).
	Env map[string]string
	// SkipRepoAgentConfig keeps the agent from loading agent config committed to
	// the repository it runs in (project settings, hooks, MCP servers). Reviewer
	// launches always set it; for workers the repoAgentConfig policy decides.
	SkipRepoAgentConfig bool
}

// WorkspaceHookConfig carries inputs needed to install workspace-local agent hooks.
type WorkspaceHookConfig struct {
	Config           AgentConfig
	DataDir          string
	Env              map[string]string
	SessionID        string
	SystemPrompt     string
	SystemPromptFile string
	WorkspacePath    string
}

// RestoreConfig carries inputs needed to continue an existing native agent session.
type RestoreConfig struct {
	// Policy is the agent policy decision for this restore; see LaunchConfig.
	Policy  agentpolicy.Decision
	Config  AgentConfig
	DataDir string
	// RunFilePath is the daemon's run file, from which an adapter derives the
	// install token path to deny the agent reading. Empty means unknown.
	RunFilePath string
	// Env is the launch env the restored process runs with, when the caller
	// has it (nil otherwise). Fencing adapters read harness homes from it, such
	// as an agent account's CODEX_HOME.
	Env             map[string]string
	Kind            domain.SessionKind
	Permissions     PermissionMode
	AllowedTools    []string
	DisallowedTools []string
	Session         SessionRef
	// Prompt is an optional new user turn to submit while resuming the native
	// conversation. Adapters whose CLI accepts a resume-time positional prompt
	// should append it to the restore command; after-start adapters leave it
	// empty and receive the turn through the interactive terminal instead.
	Prompt string
	// SystemPrompt carries the session's standing instructions (e.g. the
	// orchestrator role). Agent CLIs rebuild their system prompt from flags on
	// resume — it is not part of the transcript — so adapters whose CLI has a
	// system-prompt flag should re-apply this in their resume command.
	SystemPrompt     string
	SystemPromptFile string
	// SkipRepoAgentConfig keeps the agent from loading agent config committed to
	// the repository it runs in (project settings, hooks, MCP servers). Reviewer
	// launches always set it; for workers the repoAgentConfig policy decides.
	SkipRepoAgentConfig bool
}

// SessionRef identifies a Tallpine session whose agent-owned metadata may be read.
type SessionRef struct {
	ID            string
	Metadata      map[string]string
	WorkspacePath string
	// DataDir is Tallpine's isolated state root. Native lifecycle commands must use it
	// as their stable working/configuration root, never the session worktree.
	DataDir string
}

// SessionInfo contains agent-owned session metadata.
type SessionInfo struct {
	AgentSessionID string
	Metadata       map[string]string
	Title          string
	Summary        string
}

// PermissionMode controls how much review an agent requires before acting. It
// is a type alias for domain.PermissionMode so adapters keep using
// ports.PermissionMode while the typed AgentConfig (in domain) reuses the same
// type.
type PermissionMode = domain.PermissionMode

// The permission modes adapters map onto their agent's native approval flags.
// These re-export the domain constants so existing adapter code is unchanged.
const (
	PermissionModeDefault           = domain.PermissionModeDefault
	PermissionModeAcceptEdits       = domain.PermissionModeAcceptEdits
	PermissionModeAuto              = domain.PermissionModeAuto
	PermissionModeBypassPermissions = domain.PermissionModeBypassPermissions
)

// NormalizePermissionMode collapses an empty or unrecognized mode to
// PermissionModeDefault, leaving the four known modes unchanged. Adapters call
// it so a stored value they don't recognize defers to the agent's own config
// (usually by emitting no flag) rather than mapping onto a bogus one.
func NormalizePermissionMode(mode PermissionMode) PermissionMode {
	switch mode {
	case PermissionModeDefault,
		PermissionModeAcceptEdits,
		PermissionModeAuto,
		PermissionModeBypassPermissions:
		return mode
	default:
		return PermissionModeDefault
	}
}

// PromptDeliveryStrategy describes how Tallpine should deliver the initial prompt.
type PromptDeliveryStrategy string

// How the orchestrator hands the initial prompt to a freshly launched agent.
const (
	PromptDeliveryInCommand   PromptDeliveryStrategy = "in_command"
	PromptDeliveryAfterStart  PromptDeliveryStrategy = "after_start"
	PromptDeliveryCustomAgent PromptDeliveryStrategy = "custom_agent"
)

// AgentInteractiveNativeRestorer requires a strict, taskless initialization after
// runtime launch. It never exposes fresh argv as a successful ordinary resume.
type AgentInteractiveNativeRestorer interface {
	PrepareNativeRestore(context.Context, RestoreConfig) (NativeRestoreInitialization, error)
}

// NativeRestoreInitialization is private to one restore attempt. The manager
// requires StyledTerminalOutputReader current viewport evidence, never raw history.
// The adapter
// validates native history and interprets current terminal/witness evidence;
// the manager alone owns runtime I/O, deadlines, publication and cleanup.
type NativeRestoreInitialization interface {
	Argv() []string
	LaunchEnv() map[string]string
	ResumeInput() string
	TargetID() string
	Ready(context.Context, string, string) (bool, error)
	Restored(context.Context, string, string) (bool, error)
}
