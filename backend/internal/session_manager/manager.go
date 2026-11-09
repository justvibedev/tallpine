// Package sessionmanager drives internal session command operations over runtime,
// agent, workspace, storage, messenger, and lifecycle dependencies.
package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/attachmentstore"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/sessionguard"
)

// Sentinel errors returned by the Session Manager; callers match them with
// errors.Is.
var (
	ErrNotFound                      = errors.New("session: not found")
	ErrSemanticAcceptanceUnsupported = errors.New("session: semantic message acceptance unsupported")
	// ErrSemanticAcceptancePending means the message is typed but the agent is
	// still mid-turn, so it has not accepted it yet. This is not a failure: it
	// clears by itself when the turn ends.
	ErrSemanticAcceptancePending = errors.New("session: semantic message not yet accepted by the running turn")
	ErrNotRestorable             = errors.New("session: not restorable (not terminal)")
	ErrTerminated                = errors.New("session: terminated")
	ErrAgentExited               = errors.New("session: agent exited")
	// ErrAgentStoppedByUser refuses a send to a Chat whose agent was stopped
	// on purpose; only an explicit Resume brings it back.
	ErrAgentStoppedByUser  = errors.New("session: agent was stopped; resume it to deliver messages")
	ErrAgentNotExited      = errors.New("session: agent has not exited")
	ErrAgentExitInProgress = errors.New("session: agent exit is already in progress")
	ErrIncompleteHandle    = errors.New("session: incomplete teardown handle")
	// ErrProjectNotResolvable means the spawn's project has no usable repo
	// (unregistered, archived, or missing a path). The API maps it to a 400.
	ErrProjectNotResolvable = errors.New("session: project repo not resolvable")
	// ErrUnknownHarness means the requested agent harness has no registered
	// adapter. The API maps it to a 400 so a typo'd `--harness` is a validation
	// error, not an opaque 500.
	ErrUnknownHarness = errors.New("session: unknown agent harness")
	// ErrUnsupportedHarness means a stored session names a harness this build
	// no longer ships. Such a session stays readable and can be terminated, but
	// every operation that would run its agent is refused.
	ErrUnsupportedHarness = errors.New("session: harness is no longer supported")
	// ErrMissingHarness means neither the spawn request nor the project's role
	// config selected an agent. Worker/orchestrator spawns must be explicit.
	ErrMissingHarness = errors.New("session: agent harness required")
	// ErrArtifactOutputUnavailable rejects an artifact-output spawn when the
	// daemon has no data dir to hold the session's artifact directory.
	ErrArtifactOutputUnavailable = errors.New("session: artifact output requires an artifact directory")
	// ErrHarnessInstallActive prevents launch while the harness executable is replaced.
	ErrHarnessInstallActive = errors.New("session: harness install active")
	// ErrUnsupportedModel means the requested model is not supported by the
	// selected harness's catalog and the harness does not accept arbitrary model
	// ids. The API maps it to a 400.
	ErrUnsupportedModel = errors.New("session: model is not supported by the selected harness")
	// ErrScratchBranchUnsupported means a caller tried to force git branch
	// semantics onto a scratch project.
	ErrScratchBranchUnsupported = errors.New("session: scratch projects do not support branches")
	// ErrNotResumable means a terminated session cannot be relaunched: its adapter
	// cannot natively resume it AND it has no prompt to fresh-launch from, and it is
	// not an orchestrator (orchestrators are promptless by design and relaunch fresh
	// with the system prompt only). Workers without a task and without a native
	// session id have nothing meaningful to restore.
	ErrNotResumable        = errors.New("session: nothing to resume from")
	ErrOperationInProgress = errors.New("session: another session operation is in progress")
	// ErrTargetAgentUnauthorized is returned only when the target adapter's
	// local auth probe conclusively reports missing or invalid credentials.
	// Unknown/probe failures remain advisory and are allowed to reach launch.
	ErrTargetAgentUnauthorized = errors.New("session: target agent is not authenticated")
	// ErrSwitchSourceStopUnconfirmed means runtime teardown returned an error and
	// Tallpine could not prove whether the source still owns the session. No target is
	// launched in this case.
	ErrSwitchSourceStopUnconfirmed = errors.New("session: source agent stop could not be confirmed")
	// ErrInterfaceHandoffUnsupported means the harness has not proven that its
	// TUI resume identity and Chat protocol identity name the same conversation.
	ErrInterfaceHandoffUnsupported = errors.New("session: interface handoff unsupported")
	// ErrNativeConversationMissing means a supported harness has not yet exposed
	// the native id required to resume it through the other controller.
	ErrNativeConversationMissing = errors.New("session: native conversation id unavailable")
	// ErrNativeConversationUnverified means the stored native id belongs to an
	// older terminal generation and the currently visible TUI has not confirmed
	// that it resumed the same provider conversation yet.
	ErrNativeConversationUnverified = errors.New("session: native conversation id is not confirmed for the current terminal launch")
	// ErrInterfaceWorkspaceUnavailable means the session's start failed before
	// its workspace existed, so no controller can run there until it restarts.
	ErrInterfaceWorkspaceUnavailable = errors.New("session: workspace was never set up")
	// ErrInterfaceAlreadySelected makes a stale/double switch request an explicit
	// conflict instead of leaking a generic 500 after the first switch commits.
	ErrInterfaceAlreadySelected = errors.New("session: requested interface is already selected")
	// ErrInterfaceTransitionInProgress distinguishes TUI/Chat controller handoff
	// from a provider agent switch so the API can report the correct operation.
	ErrInterfaceTransitionInProgress = errors.New("session: interface transition already in progress")
	// ErrInterfaceTransitionNotFound distinguishes a missing handoff from a
	// missing session when DELETE is retried after the transition settled.
	ErrInterfaceTransitionNotFound = errors.New("session: interface transition not found")
	// ErrInterfaceTransitionNotCancellable protects the no-overlap invariant once
	// the source controller is already stopping or stopped.
	ErrInterfaceTransitionNotCancellable = errors.New("session: interface transition can no longer be cancelled")
	// ErrInterfaceTransitionNoticeNotAcknowledgeable rejects acknowledgements for
	// active/successful rows that have no failure or recovery notice to dismiss.
	ErrInterfaceTransitionNoticeNotAcknowledgeable = errors.New("session: interface transition has no acknowledgeable notice")
	// ErrInterfaceProviderHistoryRecoveryUnavailable rejects broad or stale
	// recovery requests. Tallpine permits provider authority only for the latest exact
	// TUI-to-Chat saga after it proved every mismatch was legacy text, including
	// the same explicit recovery saga after startup reconciliation interrupted it.
	ErrInterfaceProviderHistoryRecoveryUnavailable = errors.New("session: provider-history recovery is unavailable")
	// ErrResumeInProgress prevents concurrent resume requests from replacing the
	// same runtime twice.
	ErrResumeInProgress = errors.New("session: agent resume already in progress")
	// ErrAwaitingDecision means the session is paused on a pending
	// permission/approval dialog. Send refuses to paste into it: the runtime
	// appends Enter after every paste, and an Enter into a decision dialog
	// would answer it on the user's behalf. The API maps it to a 409; the
	// caller retries once the user has answered in the terminal.
	ErrAwaitingDecision = errors.New("session: awaiting a user decision")
	// ErrStartupPending means a TUI session has not yet received its first
	// agent hook callback, so the native startup sequence (including pre-session
	// approval dialogs) may still be consuming pane input.
	ErrStartupPending = errors.New("session: agent startup not ready for input")
	// ErrComposerBusy means a TUI session's composer holds an unsent human draft,
	// so delivering now would concatenate with the operator's half-typed text and
	// submit both as one prompt (#5711). The API maps it to a retryable 409; the
	// caller retries once the operator submits or discards their draft.
	ErrComposerBusy = errors.New("session: composer has an unsent draft")
	// ErrDeliveryUnconfirmed means the prompt was written to the pane, but the
	// harness never confirmed that the agent became active before the
	// confirmation budget was exhausted (e.g. trailing Enter absorbed or dropped).
	// The API maps it to a 409; the caller retries or inspects the terminal.
	ErrDeliveryUnconfirmed = errors.New("session: message delivery was not acknowledged")

	// Spawn-stage sentinels. Each is the existing log word after "spawn" /
	// "spawn <id>:" so wrapping them does not change daemon-log wording, while
	// errors.Is can tell the service which stage failed. More specific wrapped
	// sentinels (branch, agent binary, chat preflight) still match first.
	ErrSpawnPrompt             = errors.New("prompt")
	ErrSpawnCreate             = errors.New("create")
	ErrSpawnSystemPrompt       = errors.New("system prompt file")
	ErrSpawnArtifactDir        = errors.New("artifact dir")
	ErrSpawnAgentAccount       = errors.New("agent account")
	ErrWorkspaceCreate         = errors.New("workspace")
	ErrWorkspaceProvision      = errors.New("provision")
	ErrSpawnAttachments        = errors.New("attachments")
	ErrSpawnBrowser            = errors.New("browser capability")
	ErrSpawnPrepare            = errors.New("prepare")
	ErrSpawnPromptDelivery     = errors.New("prompt delivery")
	ErrSpawnLaunchCommand      = errors.New("launch command")
	ErrSpawnSupervisor         = errors.New("supervisor")
	ErrSpawnPrepareLaunch      = errors.New("prepare launch")
	ErrRuntimeCreate           = errors.New("runtime")
	ErrSpawnCommit             = errors.New("completed")
	ErrSpawnDeliverPrompt      = errors.New("deliver prompt")
	ErrClientRequestConflict   = errors.New("client request id belongs to a different task")
	ErrClientRequestIncomplete = errors.New("client request has an incomplete prior spawn")
	ErrChatController          = errors.New("chat controller")
)

// wrapSpawnStage annotates a spawn failure with a stage sentinel. The original
// error stays in the chain so errors.Is still matches inner sentinels.
func wrapSpawnStage(id domain.SessionID, stage, err error) error {
	if err == nil {
		return fmt.Errorf("spawn %s: %w", id, stage)
	}
	return fmt.Errorf("spawn %s: %w: %w", id, stage, err)
}

func wrapSpawnStageEarly(stage, err error) error {
	if err == nil {
		return fmt.Errorf("spawn: %w", stage)
	}
	return fmt.Errorf("spawn: %w: %w", stage, err)
}

// PreviewLifecycle is the narrow teardown hook consumed by Session Manager.
// Keeping it here follows the consumer-owned interface boundary.
type PreviewLifecycle interface {
	StopSession(ctx context.Context, id domain.SessionID) error
}

// BrowserLifecycle is the narrow Electron-target teardown hook consumed by
// Session Manager. It must work even when no renderer panel mounted.
type BrowserLifecycle interface {
	DestroySession(ctx context.Context, id domain.SessionID) error
}

// BrowserCapabilityIssuer mints the split capability injected into a worker
// and persisted as a one-way verifier on its session row.
type BrowserCapabilityIssuer interface {
	Issue(id domain.SessionID) (token, verifier string, err error)
}

// SessionTokenIssuer mints a session's scoped daemon credential for the
// launch identified by its browser-capability verifier, so the credential
// rotates with every launch (see internal/sessiontoken).
type SessionTokenIssuer interface {
	IssueSession(id domain.SessionID, launchVerifier string) string
}

// sendConfirmConfig bounds the best-effort activity-confirmation loop run after
// Send. Tallpine has no delivery ack: tallpine send returns 200 the moment tmux send-keys
// exits 0, and for a large multiline paste the single Enter may not submit the
// prompt — so UserPromptSubmit never fires and the orchestrator cannot tell the
// worker started. confirmActive observes the durable Activity.State (written by
// the user-prompt-submit hook) and re-sends Enter until the session is active or
// the budget is exhausted. If the budget is exhausted without the session going
// active and no delivery-specific acceptance marker is found, it fails the send
// with ErrDeliveryUnconfirmed rather than silently reporting a delivery it could
// not confirm.
type sendConfirmConfig struct {
	// pollInterval is the gap between activity reads.
	pollInterval time.Duration
	// semanticAcceptance overrides semanticAcceptanceTimeout when positive.
	semanticAcceptance time.Duration
	// attemptDeadline is how long to wait for active after each Enter.
	attemptDeadline time.Duration
	// maxAttempts bounds how many times Enter is (re)sent, counting the initial
	// Enter from Send itself.
	maxAttempts int
}

// interfaceTransitionConfig keeps reported human-paced work unbounded while
// making the contradictory stale-idle proof window short and testable. Only an
// idle row older than accepted PTY input consumes staleIdleLimit.
type interfaceTransitionConfig struct {
	pollInterval   time.Duration
	idleSettle     time.Duration
	staleIdleLimit time.Duration
}

// Production sendConfirm bounds: 3 Enters total (1 from Send + 2 re-sends),
// each given 2s to flip the session active, polled every 300ms.
const (
	sendConfirmPollInterval       = 300 * time.Millisecond
	sendConfirmAttemptDeadline    = 2 * time.Second
	sendConfirmMaxAttempts        = 3
	defaultBranchRefreshTimeout   = 5 * time.Second
	promptDeliveryDeadlineReserve = 5 * time.Second
)

// Deps are the collaborators a Session Manager needs; New wires them together.
type Deps struct {
	Runtime   runtimeController
	Agents    ports.AgentResolver
	Workspace ports.Workspace
	Store     Store
	Messenger ports.AgentMessenger
	// Defaults supplies the daemon-owned default session interface for spawns that
	// name no mode. Nil means always use the compatibility default.
	Defaults SessionModeDefaults
	// Chat launches the structured controller for a chat-mode session. Nil means
	// chat mode is unavailable. Explicit Chat requests are refused; an inherited
	// Chat preference falls back to TUI.
	Chat                ChatLauncher
	Lifecycle           lifecycleRecorder
	Preview             PreviewLifecycle
	Browser             BrowserLifecycle
	BrowserCapabilities BrowserCapabilityIssuer
	// SessionTokens mints the scoped daemon credential (TALLPINE_SESSION_TOKEN)
	// each agent launch gets instead of the install token. Nil launches
	// agents without one.
	SessionTokens SessionTokenIssuer
	// DataDir owns durable attachment storage and is exported to spawned agents
	// as TALLPINE_DATA_DIR so their hook commands can open the same store.
	DataDir string
	// RunFilePath is exported to spawned agents as TALLPINE_RUN_FILE so their hook
	// callbacks reach this daemon even when another Tallpine daemon is also running.
	RunFilePath string
	// SessionGradleHome points each session's GRADLE_USER_HOME at a per-project
	// home under DataDir (TALLPINE_SESSION_GRADLE_HOME=off disables it).
	SessionGradleHome bool
	Clock             func() time.Time
	// LookPath overrides exec.LookPath for the pre-launch agent-binary check.
	// Production wiring leaves this nil and the manager defaults to
	// exec.LookPath; tests inject a stub so they need not seed real binaries.
	LookPath func(string) (string, error)
	// Executable overrides os.Executable for the session PATH pin (see
	// hookPATH). Production wiring leaves this nil; tests inject a stub so they
	// control what the test binary appears to be.
	Executable func() (string, error)
	// NewLaunchID overrides supervised-process generation for deterministic tests.
	NewLaunchID func() string
	// CodexOperationGate is shared with account clients and reviewer launches.
	// Nil preserves focused-test compatibility by disabling device-global gating.
	CodexOperationGate ports.CodexOperationGate
	// ReconcileWorkers bounds concurrent live-session recovery during daemon
	// startup. Values below one preserve the serial default for embedders/tests;
	// production explicitly opts into a small worker pool.
	ReconcileWorkers int
	// BackgroundContext owns work admitted by request-scoped methods. Nil keeps
	// focused tests and embedders compatible by defaulting to Background.
	BackgroundContext context.Context
	// Logger receives spawn-time diagnostics (e.g. when the session PATH
	// cannot be pinned to the daemon binary). Nil defaults to slog.Default().
	Logger *slog.Logger
}

func (m *Manager) beginAgentSwitchAttempt() error {
	m.agentSwitchWorkerMu.Lock()
	defer m.agentSwitchWorkerMu.Unlock()
	if m.agentSwitchWorkersClosed {
		return errors.New("session: interface recovery is shutting down")
	}
	m.agentSwitchWorkers.Add(1)
	return nil
}

// WaitAgentSwitchWorkers closes admission and waits for deferred TUI/Chat
// interface recovery work.
func (m *Manager) WaitAgentSwitchWorkers(ctx context.Context) error {
	m.agentSwitchWorkerMu.Lock()
	m.agentSwitchWorkersClosed = true
	m.agentSwitchWorkerMu.Unlock()
	return waitForWorkers(ctx, &m.agentSwitchWorkers)
}

// New builds a Session Manager from its dependencies, defaulting the clock to
// time.Now when Deps.Clock is nil.
func New(d Deps) *Manager {
	m := &Manager{
		runtime:                        d.Runtime,
		agents:                         d.Agents,
		workspace:                      d.Workspace,
		store:                          d.Store,
		defaults:                       d.Defaults,
		chat:                           d.Chat,
		lcm:                            d.Lifecycle,
		preview:                        d.Preview,
		browser:                        d.Browser,
		browserCapabilities:            d.BrowserCapabilities,
		sessionTokens:                  d.SessionTokens,
		attachments:                    attachmentstore.New(d.DataDir),
		attachmentSuffix:               randomSuffix,
		dataDir:                        d.DataDir,
		runFilePath:                    strings.TrimSpace(d.RunFilePath),
		sessionGradleHome:              d.SessionGradleHome,
		clock:                          d.Clock,
		reconcileWorkers:               d.ReconcileWorkers,
		defaultBranchRefreshTimeout:    defaultBranchRefreshTimeout,
		taskPreparations:               make(map[domain.TaskPreparationToken]*taskPreparation),
		taskNamingSlots:                make(chan struct{}, 4),
		asyncChatSpawns:                make(map[domain.SessionID]*asyncChatSpawnRun),
		taskPreparationTTL:             defaultTaskPreparationTTL,
		openTranscriptFile:             os.Open,
		lookPath:                       d.LookPath,
		executable:                     d.Executable,
		newLaunchID:                    d.NewLaunchID,
		codexOperationGate:             defaultCodexOperationGate(d.CodexOperationGate),
		backgroundContext:              d.BackgroundContext,
		startupBackgroundReconcileDone: make(chan struct{}),
		agentOperations:                make(map[domain.SessionID]agentOperationKind),
		launchStartWait:                3 * time.Second,
		inputLeases:                    make(map[domain.SessionID]int),
		inputDrained:                   make(map[domain.SessionID]chan struct{}),
		agentOperationSince:            make(map[domain.SessionID]time.Time),
		inputLeaseSince:                make(map[domain.SessionID]time.Time),
		transitions:                    make(map[domain.SessionID]*interfaceTransitionRun),
		transitionDeliveryWake:         make(chan struct{}, 1),
		sendConfirm: sendConfirmConfig{
			pollInterval:    sendConfirmPollInterval,
			attemptDeadline: sendConfirmAttemptDeadline,
			maxAttempts:     sendConfirmMaxAttempts,
		},
		interfaceTransition: interfaceTransitionConfig{
			pollInterval:   interfaceTransitionPoll,
			idleSettle:     interfaceTransitionIdleSettle,
			staleIdleLimit: interfaceTransitionStaleIdleLimit,
		},
		logger:         d.Logger,
		workspaceGates: make(map[domain.ProjectID]*sync.RWMutex),
	}
	if m.clock == nil {
		// UTC so spawn-stamped CreatedAt/UpdatedAt match every other session
		// write (rename, activity) — all of which use time.Now().UTC(). A local
		// default produced mixed-timezone timestamps in `tallpine session get`.
		m.clock = func() time.Time { return time.Now().UTC() }
	}
	m.statusRecoveries = make(map[domain.SessionID]statusRecovery)
	m.statusVerificationLimit = statusVerificationLimit
	m.startupChatRetryDelays = defaultStartupChatRetryDelays
	m.startupChatResumeDelays = defaultStartupChatResumeDelays
	m.startupChatResumeSem = make(chan struct{}, defaultStartupChatResumeWorkers)
	m.startupChatResume = m.resumeStartupChatOnce
	m.startupChatResumePending = make(map[domain.SessionID]struct{})
	if m.reconcileWorkers < 1 {
		m.reconcileWorkers = 1
	}
	if m.backgroundContext == nil {
		m.backgroundContext = context.Background()
	}
	if m.lookPath == nil {
		m.lookPath = exec.LookPath
	}
	if m.executable == nil {
		m.executable = os.Executable
	}
	if m.newLaunchID == nil {
		m.newLaunchID = uuid.NewString
	}
	if m.logger == nil {
		m.logger = slog.Default()
	}
	// messenger is the raw d.Messenger wrapped in a Guard (needs m.logger, so it
	// is built after the logger default).
	m.messenger = sessionguard.New(d.Store, d.Messenger, m.logger)
	m.messenger.SetInputLease(m)
	m.messenger.SetStartupSignalGate(m.harnessStartupSignalGatesInput)
	return m
}
