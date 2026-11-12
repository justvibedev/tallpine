package acp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/persistenthost"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

const (
	// eventBuffer is how many events the consumer may fall behind by before a
	// journaled emitter blocks or an unjournaled delta is dropped. Only
	// eventChannelBuffer of it is allocated up front (see event_queue.go).
	eventBuffer  = 4096
	approvalWait = 30 * time.Minute
	// sessionCloseWait bounds the session/close sent before a provider is
	// terminated (quickSessionCloseWait for a QuickStop provider).
	sessionCloseWait      = 2 * time.Second
	quickSessionCloseWait = 500 * time.Millisecond
)

var (
	errConversationClosed = errors.New("ACP conversation closed")
	errClientCapability   = errors.New("ACP client capability not advertised")

	// ErrACPSetterUnsupported is returned by applyTurnSettings when the agent
	// does not implement session/set_mode or session/set_config_option
	// (JSON-RPC -32601). Start() and Resume() tolerate it because the initial
	// model and permission mode may have been applied via launch-time flags;
	// SendTurn propagates it so a runtime change that cannot be applied is
	// surfaced to the user instead of silently running with the wrong mode.
	ErrACPSetterUnsupported = errors.New("ACP agent does not support runtime session configuration")
)

type preparedTurn struct {
	id              string
	clientMessageID string
	prompt          []acpsdk.ContentBlock
}

type interruptAttempt struct {
	turnID string
	done   chan struct{}
	err    error
}

type parkedPermission struct {
	options map[string]json.RawMessage
	result  chan string
	ready   chan struct{}
}

type parkedInput struct {
	request ports.ChatInputRequest
	result  chan ports.ChatInputResponse
	ready   chan struct{}
}

type toolState struct {
	id             string
	title          string
	kind           acpsdk.ToolKind
	status         acpsdk.ToolCallStatus
	locations      []acpsdk.ToolCallLocation
	content        []acpsdk.ToolCallContent
	rawInput       any
	rawOutput      any
	meta           map[string]any
	terminalOutput string
}

type nestedMessageState struct {
	text     string
	parentID string
}

type conversation struct {
	conn                 *acpsdk.ClientSideConnection
	legacyWire           *legacyACPTransport
	proc                 *process
	log                  *slog.Logger
	providerScopeID      string
	decodeConversationID func(string) (string, error)
	canDeleteFork        bool
	unadoptedForkID      string

	mu                sync.Mutex
	sessionID         string
	reportedSessionID string
	capabilities      ports.ChatCapabilities
	prepared          *preparedTurn
	activeTurn        string
	settlingTurn      string
	turnCancel        context.CancelFunc
	interrupt         *interruptAttempt
	pending           map[string]*parkedPermission
	pendingInputs     map[string]*parkedInput
	accepted          map[string]persistentInteractionCommand
	messages          map[string]string
	thoughts          map[string]string
	nestedMessages    map[string]nestedMessageState
	tools             map[string]*toolState
	subagents         map[string]*subagentTask // SDK subagent tasks by task id
	subagentSeq       uint64
	compactionSent    map[string]ports.ChatCompaction // last report per compact tool call
	turnDiffs         *turnDiffAccumulator
	turnDiffTurnID    string
	providerFailure   *ports.ChatEvent
	mcpReported       map[string]bool
	configOptions     []ports.ChatConfigOption
	skills            []ports.ChatSkill
	skillsKnown       bool
	closed            bool
	modeFor           func(ports.PermissionMode) string
	optionsFor        func(ports.ChatTurnSettings) []SessionOption
	// requireAdvertised refuses a config option the session did not advertise
	// instead of sending it. See Config.RequireAdvertisedOptions.
	requireAdvertised bool
	// optionCatalogWait: see Config.OptionCatalogWait.
	optionCatalogWait time.Duration
	permissionMode    ports.PermissionMode
	permissionFor     boundPermissionPolicy
	initialPermission ports.PermissionMode
	validateSettings  TurnSettingsValidator
	extensionFor      ClientExtensionHandler
	extensionMethods  map[string]string
	legacyModel       bool
	// The agent lists model among its own config options, as well as the older model list.
	modelConfigOption  bool
	legacyMode         bool
	liveState          *persistenthost.ACPState
	hasDurableTurns    bool
	detaching          bool
	terminalEventID    string
	ignorePromptResult bool
	// onAuthRejected corrects cached auth state when the provider rejects the
	// credential mid-turn. Nil when the binding does not supply one.
	onAuthRejected func()
	// authRejected records a credential refusal on this connection, so only the
	// next successful turn reports recovery. Durable recovery after a restart is
	// established by the controller from turn completion, not from this signal.
	authRejected          bool
	promptResponseFailure func(acpsdk.PromptResponse, string) error
	// turnUsage: see Config.TurnUsage.
	turnUsage func(map[string]any, string) []domain.ModelUsage
	// infoContextUsage: see Config.InfoContextUsage.
	infoContextUsage func(map[string]any) (int64, int64, bool)
	// usageRateLimits: see Config.UsageRateLimits.
	usageRateLimits func(map[string]any) *ports.ChatRateLimits
	// sessionPromptResponseFailure is promptResponseFailure given the session.
	sessionPromptResponseFailure func(string, acpsdk.PromptResponse, bool) error
	// sessionUsage is the provider's own cumulative accounting, read after a prompt.
	sessionUsage func(string) *ports.ChatUsage
	// lastSessionUsageTotal is the cumulative total last emitted from sessionUsage.
	lastSessionUsageTotal int64
	hibernationCheck      func(context.Context, *acpsdk.ClientSideConnection, acpsdk.SessionId) (bool, error)
	historyNotice         func(string) bool
	// historyUserChunkIsMessage: see Config.HistoryUserChunkIsMessage.
	historyUserChunkIsMessage bool
	// plansSpanTurns: see Config.PlansSpanTurns.
	plansSpanTurns bool
	// preamble is standing context attached to the first prompt that actually
	// reaches the provider. It is the delivery channel for agents that accept no
	// system-prompt launch flag and ignore session/new _meta but do execute
	// prompt content blocks. It is consumed in StartDeferredTurn, not SendTurn,
	// so a rejected or discarded turn leaves it armed for the next one.
	preamble      []acpsdk.ContentBlock
	preambleSpent bool
	// servedModel reports the model that answered a completed turn; nil when the
	// binding cannot tell. lastServedModel suppresses repeating the same report.
	servedModel     func(ctx context.Context, sessionID, selectedModel string, since time.Time) (string, string)
	lastServedModel string
	// validateModel rejects a model the binding knows the agent cannot use;
	// nil when the binding has no such knowledge.
	validateModel func(ctx context.Context, model string) error
	// turnModel is the model selected when turn turnModelID started. The
	// selection can change while a turn runs, so turn-scoped facts (a version
	// rejection, the model that answered) are attributed to this value instead.
	turnModel   string
	turnModelID string
	// turnStart is when turn turnModelID began; the served-model lookup ignores
	// transcript messages older than it.
	turnStart time.Time

	contextTokens     int64
	contextWindow     int64
	compactingTurnID  string
	compactionBefore  int64
	compactionSummary string
	compactedTurn     string

	eventMu      sync.RWMutex
	events       chan ports.ChatEvent
	eventsClosed bool
	backlogMu    sync.Mutex
	backlog      eventBacklog
	closeOnce    sync.Once
	closing      chan struct{}

	historyMu     sync.Mutex
	history       *historyCapture
	historyEvents []ports.ChatEvent
	historyErr    error
	historyLoaded bool
	// replayMu protects only the replay inbox and its transition to live
	// delivery; normalization of replay batches does not hold it.
	replayMu      sync.Mutex
	replaying     bool
	replayUpdates []acpsdk.SessionNotification
}

var _ ports.ChatConversation = (*conversation)(nil)

var _ ports.ChatHistoryReader = (*conversation)(nil)

var _ ports.ChatDeferredTurnStarter = (*conversation)(nil)

var _ ports.ChatConfigOptionController = (*conversation)(nil)

var _ ports.ChatSkillLister = (*conversation)(nil)

var _ ports.ChatSteerer = (*conversation)(nil)

var _ ports.ChatInputResponder = (*conversation)(nil)

var _ ports.ChatProviderPreserver = (*conversation)(nil)

var _ ports.ChatProviderTerminator = (*conversation)(nil)

var _ ports.ChatProviderHibernator = (*conversation)(nil)

var _ ports.ChatLiveReconnector = (*conversation)(nil)

var _ ports.ChatLiveReconnectActivator = (*conversation)(nil)

var _ ports.ChatProviderEventAcknowledger = (*conversation)(nil)

var _ ports.ChatCompactor = (*conversation)(nil)

var _ acpsdk.Client = (*conversation)(nil)

var _ acpsdk.ClientExperimental = (*conversation)(nil)

var _ acpsdk.ExtensionMethodHandler = (*conversation)(nil)

func newConversation(
	proc *process,
	log *slog.Logger,
	providerScopeID string,
	extensionFor ClientExtensionHandler,
	extensionAliases map[string]string,
) *conversation {
	reverseAliases := make(map[string]string, len(extensionAliases))
	for method, alias := range extensionAliases {
		reverseAliases[alias] = method
	}
	c := &conversation{
		proc:             proc,
		log:              log,
		providerScopeID:  providerScopeID,
		pending:          make(map[string]*parkedPermission),
		pendingInputs:    make(map[string]*parkedInput),
		accepted:         make(map[string]persistentInteractionCommand),
		capabilities:     make(ports.ChatCapabilities),
		messages:         make(map[string]string),
		thoughts:         make(map[string]string),
		nestedMessages:   make(map[string]nestedMessageState),
		tools:            make(map[string]*toolState),
		events:           make(chan ports.ChatEvent, eventChannelBuffer),
		closing:          make(chan struct{}),
		extensionFor:     extensionFor,
		extensionMethods: reverseAliases,
	}
	legacyWire, sdkWriter, sdkReader := newLegacyACPTransport(proc.stdin)
	c.legacyWire = legacyWire
	c.conn = acpsdk.NewClientSideConnection(
		c, sdkWriter, newExtensionMethodReader(sdkReader, extensionAliases),
	)
	c.conn.SetLogger(log)
	go legacyWire.forward(proc.stdout, c.handleNotification)
	go c.watchConnection()
	return c
}

// providerItemID makes ACP's session-scoped opaque item ids safe to use in
// Tallpine's conversation-wide indexes. ACP only promises ids such as toolCallId are
// unique inside one provider session, while reconstructed branches deliberately
// create new sessions that may reuse those values.
func (c *conversation) providerItemID(id string) string {
	if id == "" || c.providerScopeID == "" {
		return id
	}
	return "acp:" + lengthPrefixedTuple(c.providerScopeID, id)
}

// lengthPrefixedTuple encodes opaque strings injectively. ACP identifiers are
// allowed to contain delimiters (including NUL), and Tallpine provider scopes contain
// colons, so delimiter-joining cannot safely define a durable identity.
func lengthPrefixedTuple(parts ...string) string {
	var encoded strings.Builder
	for _, part := range parts {
		encoded.WriteString(strconv.Itoa(len(part)))
		encoded.WriteByte(':')
		encoded.WriteString(part)
	}
	return encoded.String()
}

func decodeLengthPrefixedTuple(encoded string, count int) ([]string, bool) {
	parts := make([]string, 0, count)
	for range count {
		separator := strings.IndexByte(encoded, ':')
		if separator <= 0 {
			return nil, false
		}
		lengthText := encoded[:separator]
		length, err := strconv.Atoi(lengthText)
		if err != nil || length < 0 || strconv.Itoa(length) != lengthText {
			return nil, false
		}
		encoded = encoded[separator+1:]
		if len(encoded) < length {
			return nil, false
		}
		parts = append(parts, encoded[:length])
		encoded = encoded[length:]
	}
	return parts, encoded == ""
}

func (c *conversation) legacyProviderItemAlias(id string) (string, bool) {
	if c.providerScopeID == "" || !strings.HasPrefix(id, "acp:") {
		return "", false
	}
	parts, ok := decodeLengthPrefixedTuple(strings.TrimPrefix(id, "acp:"), 2)
	if !ok || parts[0] != c.providerScopeID || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func (c *conversation) start(
	sessionID string,
	capabilities ports.ChatCapabilities,
	modeFor func(ports.PermissionMode) string,
	optionsFor func(ports.ChatTurnSettings) []SessionOption,
	permissionFor boundPermissionPolicy,
	initialPermission ports.PermissionMode,
	validateSettings TurnSettingsValidator,
	configOptions []acpsdk.SessionConfigOption,
	models *legacySessionModelState,
	modes *acpsdk.SessionModeState,
) {
	c.mu.Lock()
	c.sessionID = sessionID
	c.reportedSessionID = sessionID
	c.capabilities = capabilities
	// Preserve config options received via session/update during session/new.
	// An agent may send config_option_update before start() runs; only overwrite
	// the catalog when the response actually carries one, so an early update is
	// not lost to an empty response snapshot.
	if len(configOptions) > 0 || models != nil || modes != nil {
		c.configOptions = normalizeSessionOptions(configOptions, models, modes)
	}
	if len(c.configOptions) > 0 {
		c.capabilities[ports.ChatCapabilityConfigOptions] = true
	}
	if c.skillsKnown {
		c.capabilities[ports.ChatCapabilitySkills] = true
		c.capabilities[ports.ChatCapabilityCompaction] = hasCompactSkill(c.skills)
	}
	c.modeFor = modeFor
	c.optionsFor = optionsFor
	c.permissionFor = permissionFor
	c.initialPermission = ports.NormalizePermissionMode(initialPermission)
	c.permissionMode = c.initialPermission
	c.validateSettings = validateSettings
	c.legacyModel = models != nil
	c.modelConfigOption = slices.ContainsFunc(configOptions, func(option acpsdk.SessionConfigOption) bool {
		return option.Select != nil && option.Select.Id == "model"
	})
	c.legacyMode = modes != nil
	c.mu.Unlock()
	c.emit(ports.ChatEvent{Kind: ports.ChatEventControllerState, ControllerState: ports.ChatControllerReady})
}

func (c *conversation) ProviderConversationID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reportedSessionID
}

func (c *conversation) setReportedProviderConversationID(sessionID string) {
	c.mu.Lock()
	c.reportedSessionID = sessionID
	c.mu.Unlock()
}

// setSessionPreamble arms standing context for the next prompt that actually
// reaches the provider. Any earlier preamble is dropped: the newest launch
// context is the one that should be delivered, which is how a recomputed system
// prompt replaces the original. It is consumed in StartDeferredTurn so a turn
// that never starts leaves it armed.
func (c *conversation) setSessionPreamble(blocks []acpsdk.ContentBlock) {
	// Standing context is only intended to lead the first prompt of a session.
	// A conversation that already has user turns received it then; repeating it
	// mid-conversation would read as a new user message. Tallpine's durable history
	// covers a live reconnect, which replays nothing; session/load replay covers
	// turns made in the terminal.
	if c.hasPriorTurns() {
		blocks = nil
	}
	c.mu.Lock()
	c.preamble = blocks
	c.preambleSpent = len(blocks) == 0
	c.mu.Unlock()
}

func (c *conversation) hasPriorTurns() bool {
	c.mu.Lock()
	durable := c.hasDurableTurns
	c.mu.Unlock()
	return durable || c.nativeHistoryHasUserTurns()
}

func (c *conversation) nativeHistoryHasUserTurns() bool {
	c.historyMu.Lock()
	defer c.historyMu.Unlock()
	for _, event := range c.historyEvents {
		if event.Kind == ports.ChatEventUserMessageCompleted {
			return true
		}
	}
	return false
}

func (c *conversation) Capabilities() ports.ChatCapabilities {
	c.mu.Lock()
	defer c.mu.Unlock()
	return cloneCapabilities(c.capabilities)
}

func (c *conversation) Events() <-chan ports.ChatEvent { return c.events }

// PreservesProviderOnClose reports that Close detaches only when this
// conversation is backed by the daemon-independent host.
func (c *conversation) PreservesProviderOnClose() bool { return c.proc.terminate != nil }

// ReconnectedLive distinguishes the same initialized ACP connection from a
// replacement process recovered with session/load or session/resume.
func (c *conversation) ReconnectedLive() bool { return c.proc.reconnected }
