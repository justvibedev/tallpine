// Package acp implements Tallpine's provider-neutral Chat driver over the Agent
// Client Protocol. Provider packages supply only discovery, launch, metadata,
// and capability policy; this package owns the ACP lifecycle and translates ACP
// updates into Tallpine's durable conversation vocabulary.
package acp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/persistenthost"
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// handshakeTimeout is a var only so e2e builds can shorten it (handshake_e2e.go).
var handshakeTimeout = 60 * time.Second

// Launch describes one ACP agent process. Command may be either Tallpine's packaged
// protocol bridge or the exact user-installed provider executable resolved by
// the existing agent plugin.
type Launch struct {
	Command string
	Args    []string
	Env     map[string]string
}

// LaunchConfig is the resolved Tallpine session context a provider binding may use to
// construct its process. It intentionally contains no install mechanism: binary
// ownership stays with the existing agent plugin.
type LaunchConfig struct {
	// Policy is the conversation's frozen agent policy decision.
	Policy          agentpolicy.Decision
	ReconnectOnly   bool
	SessionID       domain.SessionID
	DataDir         string
	WorkspacePath   string
	Env             map[string]string
	Model           string
	Permissions     ports.PermissionMode
	SystemPrompt    string
	DisableTools    bool
	ProviderScopeID string
	// ProviderConversationID carries the stored native conversation on resume so
	// a binding can migrate provider-side history into a fresh process home.
	// Empty on first start.
	ProviderConversationID string
}

// Config binds one harness to an ACP agent implementation.
type Config struct {
	Harness      domain.AgentHarness
	Capabilities ports.ChatCapabilities
	Probe        func(context.Context) error
	// InstallCheck optionally reports a missing chat adapter without starting
	// anything. See ports.ChatInstallChecker.
	InstallCheck func(context.Context) error
	// FencesState optionally reports that launches on goos run inside the
	// Security mode file fence. See ports.StateFencer.
	FencesState func(goos string) bool
	// FencesLaunch optionally makes the fence depend on the launch's post-cap
	// permission mode, taking precedence over FencesState. See
	// ports.LaunchStateFencer.
	FencesLaunch func(goos string, mode ports.PermissionMode) bool
	// RetryStartupExit starts the agent once more if it exits during its first handshake.
	RetryStartupExit bool
	Launch           func(context.Context, LaunchConfig) (Launch, error)
	// ValidateInitialize optionally admits only the tested ACP distribution and
	// version after the protocol handshake identifies it. This is preferable to
	// invoking an adapter-specific version flag, which many stdio agents do not
	// implement and which can emit protocol bytes instead of a version string.
	ValidateInitialize func(acpsdk.InitializeResponse) error
	// AfterInitialize runs once on a freshly initialized provider connection,
	// before any session is created. It is for bindings whose agent needs a
	// one-time ACP call on the connection, such as authenticating with a method
	// the user has already completed elsewhere. It does not run when Tallpine
	// reattaches to a persistent host, whose provider is already set up.
	AfterInitialize func(context.Context, *acpsdk.ClientSideConnection, acpsdk.InitializeResponse, LaunchConfig) error
	// SessionMeta carries adapter-defined ACP extensions whenever Tallpine creates the
	// provider-side session object: session/new, session/load, or
	// session/resume. Standing context such as a system prompt is process input,
	// not transcript history, so a resumed native conversation must receive it
	// again even though the provider recovers the messages itself.
	//
	// Only use this for extensions the provider's session handlers actually read.
	// An agent that ignores _meta on session/new needs SessionPreamble instead.
	SessionMeta func(LaunchConfig) map[string]any
	// SessionPreamble returns standing context to attach to the first prompt of a
	// session as an embedded ACP resource. It is for agents that accept no
	// system-prompt launch flag and ignore session/new _meta, but do execute
	// prompt content blocks. It leads the first prompt of a new session. A
	// resumed conversation that already has turns received it then, so it is not
	// repeated (see conversation.setSessionPreamble).
	SessionPreamble func(LaunchConfig) []acpsdk.ContentBlock
	// SessionMode maps Tallpine's approval vocabulary onto this ACP agent's mode ids.
	// Empty means "leave the provider/user default unchanged".
	SessionMode func(agentpolicy.Decision, ports.PermissionMode) string
	// SessionOptions maps Tallpine's per-turn choices onto ACP config option ids.
	SessionOptions func(ports.ChatTurnSettings) []SessionOption
	// LaunchSessionOptions returns config options whose value is fixed for the
	// whole session and known only from the launch, such as a persona the agent
	// lets Tallpine select by id but not by launch flag. They are set once after
	// session/new or session/load, before the launch's turn settings.
	LaunchSessionOptions func(LaunchConfig) []SessionOption
	// RequireAdvertisedOptions refuses to send a config option, or a value, the
	// session did not advertise. Set it for agents that acknowledge an unknown
	// option without applying it, so the choice fails visibly instead.
	RequireAdvertisedOptions bool
	// OptionCatalogWait is how long to wait for a config option the session has
	// not advertised yet before RequireAdvertisedOptions refuses it, for an agent
	// that loads part of its catalog (Kiro's models) after session/new answers.
	// Zero refuses immediately.
	OptionCatalogWait time.Duration
	// CloseSessionBeforeReload closes the session before a history refresh
	// reloads it on the same connection. Set it for agents that answer a second
	// session/load with "already loaded". It applies only when the agent
	// advertises session/close.
	CloseSessionBeforeReload bool
	// PermissionPolicy lets a provider binding resolve permission requests that
	// have an exact Tallpine policy mapping before the generic client parks them for a
	// human. Returning handled=false preserves the ordinary approval flow.
	PermissionPolicy PermissionPolicy
	// ClientExtension handles provider-defined agent-to-client JSON-RPC methods.
	ClientExtension ClientExtensionHandler
	// ClientExtensionAliases maps explicitly supported legacy wire methods onto
	// ACP-compliant underscore extension names handled by the pinned SDK.
	ClientExtensionAliases map[string]string
	// ValidateTurnSettings rejects provider settings that cannot be applied to a
	// live process. The initial permission mode is the launch-time value.
	ValidateTurnSettings TurnSettingsValidator
	// TurnUsage reads provider-specific spend from a prompt response's _meta, in
	// addition to the generic quota rows. model is the session's current model
	// ("" when unknown).
	TurnUsage func(meta map[string]any, model string) []domain.ModelUsage
	// UsageRateLimits reads provider rate-limit state from a usage_update's
	// _meta. Nil means the binding reports none; the generic driver knows no
	// vendor's format.
	UsageRateLimits func(meta map[string]any) *ports.ChatRateLimits
	// PromptResponseFailure lets a provider binding interpret its own structured
	// terminal metadata after a nominally successful ACP prompt response. model
	// is the session's selected model when the turn ran, or "" when unknown.
	PromptResponseFailure func(response acpsdk.PromptResponse, model string) error
	// ServedModel optionally reports the model that actually answered the turn
	// that just completed, when it differs from selectedModel in a way worth
	// showing (for example an alias resolving to a concrete model). It returns
	// "" when there is nothing to report. Optional.
	ServedModel func(ctx context.Context, cfg LaunchConfig, sessionID, selectedModel string, since time.Time) (served, reason string)
	// ValidateModel optionally rejects a model the binding knows the running
	// agent cannot use (for example one its CLI version predates) before Tallpine
	// asks the agent to switch to it. It returns nil when the model may be tried.
	ValidateModel func(ctx context.Context, cfg LaunchConfig, model string) error
	// SessionPromptResponseFailure is PromptResponseFailure given the provider
	// session, for a binding that reads the failure from the agent's own record,
	// and whether the turn brought any reply text or tool call.
	SessionPromptResponseFailure func(sessionID string, response acpsdk.PromptResponse, answered bool) error
	// InfoContextUsage reads context occupancy from a session_info_update's
	// _meta, for an agent that sends no usage_update. It returns used and window
	// in whatever unit the agent reports (the UI shows the ratio); false means
	// the update carries no context figure.
	InfoContextUsage func(meta map[string]any) (used, window int64, ok bool)
	// SessionUsage reads the session's cumulative usage from the agent's own
	// record after each completed prompt, for an agent whose ACP stream carries
	// context occupancy but no token counts. It emits as a usage event; nil means
	// nothing to report. It does not run when the prompt response itself carried
	// usage.
	SessionUsage func(sessionID string) *ports.ChatUsage
	// HistoryNotice reports a replayed user message chunk that the agent wrote
	// into its own transcript. Replay starts no turn for it, so the agent's
	// output after it stays in the turn before, as it did live.
	HistoryNotice func(string) bool
	// HistoryUserChunkIsMessage makes each id-less replayed user chunk its own
	// prompt, for an agent that replays a user message as one chunk (pi-acp).
	HistoryUserChunkIsMessage bool
	// PlansSpanTurns marks the agent's ACP plan as one list kept for the whole
	// session (claude-agent-acp's task list), re-sent into later turns, rather
	// than a plan for the turn it arrives in.
	PlansSpanTurns bool
	// QuickStop marks a provider whose stop gains nothing from waiting (it never
	// reacts to stdin closing): a short session/close wait, then a quick signal.
	QuickStop bool
	// CanHibernate checks provider-owned work that outlives an ACP prompt.
	CanHibernate func(context.Context, *acpsdk.ClientSideConnection, acpsdk.SessionId) (bool, error)
	// EncodeProviderConversationID and DecodeProviderConversationID let a binding
	// persist ownership metadata around an opaque provider ID while keeping the
	// raw value on the ACP wire.
	EncodeProviderConversationID func(string) string
	DecodeProviderConversationID func(string) (string, error)
	// OnAuthRejected is called when the provider rejects the credential during
	// a live turn. It is how a cached "this credential works" verdict is
	// corrected the moment the provider says otherwise, and it is the only
	// correction that covers every credential source — including the Bedrock
	// and Vertex chains Tallpine cannot inspect at all. It receives the refused
	// connection's launch config so a driver can scope the correction to that
	// launch's credential. Optional.
	OnAuthRejected func(LaunchConfig)
}

// TurnSettingsValidator validates live turn settings against launch-time state.
type TurnSettingsValidator func(ports.PermissionMode, ports.ChatTurnSettings) error

// PermissionPolicy maps the active Tallpine permission mode and the provider's exact
// offered choices to an automatic response when the mapping is unambiguous.
type PermissionPolicy func(
	agentpolicy.Decision,
	ports.PermissionMode,
	acpsdk.RequestPermissionRequest,
) (acpsdk.PermissionOptionId, bool)

// SessionOption is one ACP session configuration selection.
type SessionOption struct {
	ID    string
	Value string
	// Optional settings are skipped when the session rejects them.
	Optional bool
	// Name finds the choice when the agent lists it under another value, such as
	// Claude Code offering claude-opus-5-5 as "opus", named "Opus 5.5".
	Name string
}

// Driver opens ACP conversations for a single harness.
type Driver struct {
	cfg         Config
	log         *slog.Logger
	spawn       spawnFunc // short-lived catalog discovery only
	openProcess func(context.Context, LaunchConfig, func(context.Context) (map[string]string, error)) (*process, error)
	connectHost func(context.Context, persistenthost.Config) (*persistenthost.Transport, error)
}

var _ ports.ChatDriver = (*Driver)(nil)

// New returns an ACP Chat driver from a provider binding.
func New(cfg Config, log *slog.Logger) *Driver {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	driver := &Driver{cfg: cfg, log: log, spawn: spawnAgent, connectHost: persistenthost.ConnectOrStart}
	driver.openProcess = driver.connectProcess
	return driver
}

// DiscoverConfigOptions opens a short-lived ACP session and returns the
// provider-owned configuration catalog advertised by session/new. It sends no
// prompt and closes the process immediately after the handshake.
func DiscoverConfigOptions(
	ctx context.Context,
	launch Launch,
	workingDir string,
	log *slog.Logger,
) ([]ports.ChatConfigOption, error) {
	driver := New(Config{
		Launch: func(context.Context, LaunchConfig) (Launch, error) { return launch, nil },
	}, log)
	return driver.discoverConfigOptions(ctx, workingDir)
}

func (d *Driver) discoverConfigOptions(ctx context.Context, workingDir string) ([]ports.ChatConfigOption, error) {
	if !filepath.IsAbs(workingDir) {
		return nil, fmt.Errorf("workspace path must be absolute, got %q", workingDir)
	}
	cfg := LaunchConfig{WorkspacePath: workingDir}
	launch, err := d.cfg.Launch(ctx, cfg)
	if err != nil {
		return nil, err
	}
	proc, err := d.spawn(launch, workingDir)
	if err != nil {
		return nil, err
	}
	conv, _, _, err := d.initialize(ctx, cfg, proc)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conv.Close() }()

	openCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	resp, err := conv.conn.NewSession(openCtx, acpsdk.NewSessionRequest{
		Cwd:        workingDir,
		McpServers: []acpsdk.McpServer{},
	})
	if err != nil {
		return nil, normalizeACPError("ACP session/new", err)
	}
	return normalizeConfigOptions(resp.ConfigOptions), nil
}

// Harness identifies the Tallpine harness this ACP transport adapts.
func (d *Driver) Harness() domain.AgentHarness { return d.cfg.Harness }

// Probe checks the provider binding without creating an ACP session or worktree.
func (d *Driver) Probe(ctx context.Context) (ports.ChatCapabilities, error) {
	if d.cfg.Probe == nil || d.cfg.Launch == nil {
		return nil, fmt.Errorf("%w: incomplete ACP binding", ports.ErrChatDriverUnavailable)
	}
	if err := d.cfg.Probe(ctx); err != nil {
		return nil, err
	}
	return cloneCapabilities(d.cfg.Capabilities), nil
}

// CheckChatInstall reports a missing chat adapter; nil without a cheap check.
func (d *Driver) CheckChatInstall(ctx context.Context) error {
	if d.cfg.InstallCheck == nil {
		return nil
	}
	return d.cfg.InstallCheck(ctx)
}

// FencesState implements ports.StateFencer; false without a Config.FencesState.
func (d *Driver) FencesState(goos string) bool {
	return d.cfg.FencesState != nil && d.cfg.FencesState(goos)
}

// FencesLaunch implements ports.LaunchStateFencer: Config.FencesLaunch when
// set, otherwise FencesState.
func (d *Driver) FencesLaunch(goos string, mode ports.PermissionMode) bool {
	if d.cfg.FencesLaunch != nil {
		return d.cfg.FencesLaunch(goos, mode)
	}
	return d.FencesState(goos)
}

// SupportsPermissionMode reports whether the agent can start under mode.
func (d *Driver) SupportsPermissionMode(mode ports.PermissionMode) bool {
	if d.cfg.ValidateTurnSettings == nil {
		return true
	}
	err := d.cfg.ValidateTurnSettings(mode, ports.ChatTurnSettings{Approval: mode})
	return !errors.Is(err, ports.ErrChatPermissionModeUnsupported)
}
