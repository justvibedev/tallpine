package chat

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
)

// ErrNoController reports a command for a session with no live Chat controller.
// It is distinct from "unknown session": the session may exist and be terminated,
// or its controller may have stopped, and the client needs to tell those apart.
var ErrNoController = errors.New("no live chat controller for session")

// ErrNotChatMode reports a Chat command against a session whose committed
// controller is currently TUI. An explicit interface transition may change that
// fact later, but callers must route from the persisted mode they read now.
var ErrNotChatMode = errors.New("session is not in chat mode")

var (
	// ErrExcerptInvalid reports malformed or unauthorized excerpt references.
	ErrExcerptInvalid = errors.New("chat excerpt is invalid")
	// ErrExcerptStale reports excerpt references whose source changed or disappeared.
	ErrExcerptStale = errors.New("chat excerpt is stale")
)

const (
	maxExcerptReferences = 8
	maxExcerptTextBytes  = 24 * 1024
	// Each excerpt carries its source turn so a short selection keeps its
	// meaning. Long turns are truncated so eight excerpts cannot flood the
	// provider prompt or the durable delivery row.
	maxExcerptPairedTextBytes = 16 * 1024
)

// SessionReader is the session-fact surface the service needs. It reads the
// persisted mode rather than trusting the caller, so a client cannot talk its way
// into the wrong dispatch path.
type SessionReader interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// ProjectReader reads a project, for its model policy.
type ProjectReader interface {
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
}

// ErrModelPolicyUnavailable means the project's config could not be read, so a
// model choice cannot be checked. The choice is refused (fail closed).
var ErrModelPolicyUnavailable = errors.New("project model policy could not be read; the model change was refused")

// Service owns the live Chat controllers.
type Service struct {
	store                  Store
	reader                 SnapshotReader
	pageReader             SnapshotPageReader
	sessions               SessionReader
	projects               ProjectReader
	drivers                ports.ChatDriverRegistry
	activity               ActivityRecorder
	log                    *slog.Logger
	newID                  IDFactory
	now                    Clock
	onAccountChanged       func(domain.SessionID, string, domain.AgentHarness)
	recheckAccount         func(domain.SessionID, domain.AgentHarness)
	onAsyncTask            func(domain.SessionID, ports.ChatAsyncTask)
	onAsyncTasksGone       func(domain.SessionID)
	onCodexCapacityChanged func(domain.SessionID, string, ports.CodexCapacityObservation)
	// onModelChanged syncs ChatUI's model override (including clearing it) to
	// session metadata before the next prompt routes or a later TUI rebuild.
	onModelChanged   func(domain.SessionID, string)
	stopProviderHost func(context.Context, domain.SessionID) error
	reports          atomic.Pointer[reportsvc.Coordinator]
	renders          RenderFiles
	dataDir          string
	reconcileOutput  func(context.Context, domain.SessionID) error
	renderCheck      RenderCheck
	renderMeasure    RenderMeasure
	// renderMeasures tracks background measures, so tests can wait for them.
	renderMeasures     sync.WaitGroup
	sides              *sideManager
	wakeChat           func(context.Context, domain.SessionID) error
	hibernationEnabled func() bool
	modelCatalog       ModelCatalogReader
	autoCompactDefault func(ctx context.Context, harness domain.AgentHarness) string
	// dynamicContext reports the global "Dynamic orchestrator context" toggle
	// (B413); nil counts as on.
	dynamicContext func(ctx context.Context) bool
	// announce replaces the research mode chat line; tests set it.
	announce func(c *Controller, summary string)
	// researchCarried keeps research mode across the restart that applies it;
	// the rest are test seams for that restart (B638).
	researchCarried      researchCarry
	researchPoll         time.Duration
	researchWait         time.Duration
	researchHibernatedFn func(context.Context, domain.SessionID) bool
	researchRestartFn    func(context.Context, domain.SessionID) error
	researchSendFn       func(context.Context, domain.SessionID, string) error
	hibernationPolicy    func(context.Context, domain.SessionRecord) (time.Duration, bool)
	backgroundWork       func(context.Context, domain.SessionID) (bool, error)
	// versionRefresh reports whether open sessions move to a newly installed
	// harness release, and how long they must be quiet first.
	versionRefresh func() (time.Duration, bool)
	// versionSwitches holds the release each stopped-for-refresh session is
	// moving to, until its wake succeeds or fails.
	versionSwitchMu sync.Mutex
	versionSwitches map[domain.SessionID]string
	viewMu          sync.Mutex
	viewLeases      map[domain.SessionID]map[string]time.Time
	viewClosedAt    map[domain.SessionID]time.Time
	wakeMu          sync.Mutex
	waking          map[domain.SessionID]int
	wakeRuns        map[domain.SessionID]*wakeRun
	backgroundWakes map[domain.SessionID]bool
	// wakeRetries holds sessions whose wake failed with messages still queued.
	wakeRetries map[domain.SessionID]wakeRetry
	// parking is set once the daemon begins stopping every agent on a
	// deliberate quit; nothing starts an agent after that.
	parking            atomic.Bool
	autoResumeMu       sync.Mutex
	autoResumeRetryAt  map[string]time.Time
	autoResumeInFlight map[string]bool
	autoResumeScanning bool
	usageLimitAccounts usageLimitAccountReader
	usageLimitFailover UsageLimitFailover
	// failoverMoves maps an armed turn to the account move started for it.
	failoverMoves   map[string]failoverMove // guarded by autoResumeMu
	failoverRetryAt map[string]time.Time    // guarded by autoResumeMu
	// pausedFailovers holds the sessions with a usage-limit failover in flight.
	pausedFailovers sync.Map
	// failoverMovesLoaded is set once recorded moves were read back after a restart.
	failoverMovesLoaded bool // guarded by autoResumeMu
	// updateStops are sessions stopped so an update could replace their
	// executable; the update starts them again, so their queue is kept.
	updateStops map[domain.SessionID]bool

	runPromptSuggestion   func(context.Context, domain.SessionID, string, string, string) (string, error)
	promptSuggestionSlots chan struct{}

	mu               sync.RWMutex
	controllers      map[domain.SessionID]*Controller
	ownerControllers map[domain.ConversationOwner]*Controller
	startConfigs     map[domain.ConversationOwner]StartConfig
	awakeInterrupted map[domain.ConversationOwner]domain.AwakeAgentTarget
	startIntake      startIntake // guarded by mu
	// handoffInterrupted is the provider turn each session's pending handoff
	// stopped, kept until the account move that stopped it commits.
	handoffInterrupted map[domain.SessionID]string
	gateMu             sync.Mutex
	gates              map[domain.ConversationOwner]controllerGate
	probeMu            sync.Mutex
	probed             map[domain.AgentHarness]ports.ChatCapabilities
	prewarming         sync.Map // domain.AgentHarness -> struct{}
	providerSwitches   sync.Map // domain.SessionID -> chan struct{}, one switch at a time
	terminalSwitcher   TerminalProviderSwitcher
}

// SetReportCoordinator installs the report piggyback hook after daemon wiring
// has constructed both services.
func (s *Service) SetReportCoordinator(coordinator *reportsvc.Coordinator) {
	s.reports.Store(coordinator)
}

// controllerGate serializes start/stop for one session without making provider
// I/O for that session block lookups or commands for every other Chat session.
type controllerGate chan struct{}

func (g controllerGate) lock(ctx context.Context) error {
	select {
	case g <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g controllerGate) tryLock() bool {
	select {
	case g <- struct{}{}:
		return true
	default:
		return false
	}
}

func (g controllerGate) unlock() { <-g }

// Options configures a Service. The id factory and clock are injected so tests
// are deterministic.
type Options struct {
	AppRunID   string
	Store      Store
	Reader     SnapshotReader
	PageReader SnapshotPageReader
	Sessions   SessionReader
	// Projects supplies the project model policy that gates the in-chat model
	// and agent switch. Production always wires it; nil skips the policy check
	// (unit constructions only).
	Projects ProjectReader
	Drivers  ports.ChatDriverRegistry
	// Activity feeds derived session status from turn events. Nil leaves a chat
	// session reading as idle while it works, so production always wires it.
	Activity ActivityRecorder
	Log      *slog.Logger
	NewID    IDFactory
	Now      Clock
	// OnAccountChanged invalidates daemon-owned account readiness for the
	// harness that emitted an account/updated notification.
	OnAccountChanged func(domain.SessionID, string, domain.AgentHarness)
	// RecheckAccount reads the session's account again when a turn starts while
	// a refused login is pending, so a login that works clears the notice. It
	// is not a refusal and leaves readiness alone.
	RecheckAccount func(domain.SessionID, domain.AgentHarness)
	// OnAsyncTask receives each harness background task report.
	OnAsyncTask func(domain.SessionID, ports.ChatAsyncTask)
	// OnAsyncTasksGone fires when a session's provider process is gone (its
	// tasks died with it), so no terminal frame will follow.
	OnAsyncTasksGone func(domain.SessionID)
	// OnCodexCapacityChanged attributes a structured provider update to the one
	// globally active Tallpine Codex account. The callback owns profile-independent
	// account state; conversation rows are not the authority for Codex capacity.
	OnCodexCapacityChanged func(domain.SessionID, string, ports.CodexCapacityObservation)
	// OnModelChanged syncs ChatUI's model override to session metadata before
	// the next prompt routes. Nil leaves session metadata unchanged.
	OnModelChanged func(domain.SessionID, string)
	// StopProviderHost destroys current ownership on explicit teardown or failed hibernation,
	// even if its daemon attachment already failed. Never used by StopAll.
	StopProviderHost func(context.Context, domain.SessionID) error
	// Renders stores agent HTML renders. Nil refuses PublishRender.
	Renders RenderFiles
	// DataDir locates a session's artifact directory when its record names
	// none, for a render kept as an artifact.
	DataDir string
	// ReconcileOutputType updates a session's output type at once after a
	// render is kept as an artifact. Nil leaves it to the artifact observer.
	ReconcileOutputType func(context.Context, domain.SessionID) error
	// AutoCompactDefault reads the per-harness compaction default from Settings.
	// A conversation with no choice of its own starts with it. Nil means none.
	AutoCompactDefault func(ctx context.Context, harness domain.AgentHarness) string
	// DynamicOrchestratorContext reads the global toggle for orchestrator
	// research mode (B413); nil counts as on.
	DynamicOrchestratorContext func(ctx context.Context) bool
	// HibernationEnabled reads the daemon-owned feature gate. Nil is disabled.
	HibernationEnabled func() bool
	// HibernationPolicy decides whether one session may sleep when idle and
	// how long it must be quiet first (its kind's threshold, its project's
	// opt-out). Nil allows every session after the short close grace.
	HibernationPolicy func(context.Context, domain.SessionRecord) (time.Duration, bool)
	// BackgroundProcesses reports whether a session's agent still runs work
	// of its own (a background shell, test or build) that stopping the agent
	// would kill. An error counts as busy. Nil skips the process check.
	BackgroundProcesses func(context.Context, domain.SessionID) (bool, error)
	// VersionRefresh reports whether an idle session still on a replaced
	// harness release moves to the installed one on its next message, and the
	// quiet time required first. Nil never moves a session.
	VersionRefresh func() (time.Duration, bool)
}

// New builds a Chat service.
func New(opts Options) *Service {
	log := opts.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	svc := &Service{
		store:                  opts.Store,
		reader:                 opts.Reader,
		pageReader:             opts.PageReader,
		sessions:               opts.Sessions,
		projects:               opts.Projects,
		drivers:                opts.Drivers,
		activity:               opts.Activity,
		log:                    log,
		newID:                  opts.NewID,
		now:                    now,
		onAccountChanged:       opts.OnAccountChanged,
		recheckAccount:         opts.RecheckAccount,
		onAsyncTask:            opts.OnAsyncTask,
		onAsyncTasksGone:       opts.OnAsyncTasksGone,
		onCodexCapacityChanged: opts.OnCodexCapacityChanged,
		onModelChanged:         opts.OnModelChanged,
		stopProviderHost:       opts.StopProviderHost,
		renders:                opts.Renders,
		dataDir:                opts.DataDir,
		reconcileOutput:        opts.ReconcileOutputType,
		hibernationEnabled:     opts.HibernationEnabled,
		autoCompactDefault:     opts.AutoCompactDefault,
		dynamicContext:         opts.DynamicOrchestratorContext,
		hibernationPolicy:      opts.HibernationPolicy,
		backgroundWork:         opts.BackgroundProcesses,
		versionRefresh:         opts.VersionRefresh,
		controllers:            make(map[domain.SessionID]*Controller),
		handoffInterrupted:     make(map[domain.SessionID]string),
		ownerControllers:       make(map[domain.ConversationOwner]*Controller),
		startConfigs:           make(map[domain.ConversationOwner]StartConfig),
		awakeInterrupted:       make(map[domain.ConversationOwner]domain.AwakeAgentTarget),
		gates:                  make(map[domain.ConversationOwner]controllerGate),
		probed:                 make(map[domain.AgentHarness]ports.ChatCapabilities),
		waking:                 make(map[domain.SessionID]int),
		wakeRuns:               make(map[domain.SessionID]*wakeRun),
		backgroundWakes:        make(map[domain.SessionID]bool),
		promptSuggestionSlots:  make(chan struct{}, 2),
	}
	svc.sides = newSideManager(svc, newMemorySideStore(), opts.AppRunID)
	return svc
}

func (s *Service) controllerGate(owner domain.ConversationOwner) controllerGate {
	s.gateMu.Lock()
	defer s.gateMu.Unlock()
	gate := s.gates[owner]
	if gate == nil {
		gate = make(controllerGate, 1)
		s.gates[owner] = gate
	}
	return gate
}

func conversationOwner(cfg StartConfig) domain.ConversationOwner {
	if cfg.Owner.Kind != "" && cfg.Owner.ID != "" {
		return cfg.Owner
	}
	return domain.SessionConversationOwner(cfg.SessionID)
}

func providerHostID(cfg StartConfig) domain.SessionID {
	if owner := conversationOwner(cfg); owner.Kind == domain.ConversationOwnerReview {
		return domain.ReviewChatHostID(owner.ID)
	}
	return cfg.SessionID
}

// StartConfig opens a controller for a session.
type StartConfig = ports.ChatControllerStart

// liveControllerClaimer claims a generation for a Chat that reconnected to its
// still-running provider and, in the same write, keeps the harness release and
// stale-release mark the replaced generation recorded for that process.
type liveControllerClaimer interface {
	ClaimLiveChatControllerGeneration(ctx context.Context, session domain.SessionID, generation, reclaimedTurnID string) error
}

type reviewerConversationStore interface {
	ConversationForReview(context.Context, string) (domain.ConversationRecord, error)
	GetReviewByID(context.Context, string) (domain.Review, bool, error)
}

// claimChatController claims generation for the session. A live reconnect
// keeps the old provider process, so it carries what that process runs in the
// same write as the claim.
func (s *Service) claimChatController(ctx context.Context, id domain.SessionID, generation string, liveReconnect bool, reclaimedTurnID string) error {
	if claimer, ok := s.store.(liveControllerClaimer); ok && liveReconnect {
		return claimer.ClaimLiveChatControllerGeneration(ctx, id, generation, reclaimedTurnID)
	}
	return s.store.ClaimChatControllerGeneration(ctx, id, generation)
}

// ControllerCommit is the conversation state committed by ControllerReady.
// Carrying it back across the callback avoids a fallible database read after an
// irreversible ownership transfer.
type ControllerCommit = ports.ChatControllerCommit

func conversationReconnectedLive(conv ports.ChatConversation) bool {
	reconnected, ok := conv.(ports.ChatLiveReconnector)
	return ok && reconnected.ReconnectedLive()
}

func controllerStartResult(
	controller *Controller,
	providerBoundary *domain.ConversationBranch,
	commitProviderHistory func(context.Context) error,
) StartResult {
	return StartResult{
		LiveReconnect:          conversationReconnectedLive(controller.conv),
		ProviderConversationID: controller.ProviderConversationID(),
		ControllerGeneration:   controller.Generation(),
		Conversation:           controller.conversation,
		ProviderBoundary:       providerBoundary,
		CommitProviderHistory:  commitProviderHistory,
	}
}

func notifyControllerReady(
	cfg StartConfig,
	controller *Controller,
	providerBoundary *domain.ConversationBranch,
	commitProviderHistory func(context.Context) error,
) (ControllerCommit, error) {
	if cfg.ControllerReady == nil {
		return ControllerCommit{}, nil
	}
	commit, err := cfg.ControllerReady(controllerStartResult(
		controller, providerBoundary, commitProviderHistory,
	))
	if err != nil {
		return ControllerCommit{}, fmt.Errorf("commit chat controller: %w", err)
	}
	return commit, nil
}

func cloneStartConfig(cfg StartConfig) StartConfig {
	cloned := cfg
	if cfg.AwakeReconnectTarget != nil {
		target := *cfg.AwakeReconnectTarget
		cloned.AwakeReconnectTarget = &target
	}
	cloned.Env = make(map[string]string, len(cfg.Env))
	for key, value := range cfg.Env {
		cloned.Env[key] = value
	}
	cloned.AdditionalDirectories = append([]string(nil), cfg.AdditionalDirectories...)
	cloned.MCPServers = make([]ports.ChatMCPServerConfig, len(cfg.MCPServers))
	for index, server := range cfg.MCPServers {
		server.Args = append([]string(nil), server.Args...)
		server.Env = make(map[string]string, len(cfg.MCPServers[index].Env))
		for key, value := range cfg.MCPServers[index].Env {
			server.Env[key] = value
		}
		server.Headers = make(map[string]string, len(cfg.MCPServers[index].Headers))
		for key, value := range cfg.MCPServers[index].Headers {
			server.Headers[key] = value
		}
		cloned.MCPServers[index] = server
	}
	return cloned
}
