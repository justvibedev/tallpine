package sessionmanager

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/attachmentstore"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/sessionguard"
)

// Env vars a spawned process reads to learn who it is. A worker that starts
// its own Docker containers (a database, a queue, any ad-hoc service) should
// label them `--label tallpine.session=$TALLPINE_SESSION_ID` so Tallpine's container reaper
// (dockerreap) removes them on session kill/terminal state — see #2652. Add
// `--label tallpine.spare=true` to a deliberately shared container that must
// survive past this session.
const (
	EnvSessionID = "TALLPINE_SESSION_ID"
	EnvProjectID = "TALLPINE_PROJECT_ID"
	EnvIssueID   = "TALLPINE_ISSUE_ID"
	// EnvRuntimeLaunchID identifies the current supervised agent generation.
	EnvRuntimeLaunchID = "TALLPINE_RUNTIME_LAUNCH_ID"
	// EnvSupervisedProcess tells terminal runtimes that the Tallpine supervisor owns
	// this launch. When it exits, tmux must park on a non-interpreting input sink
	// instead of exposing its historical interactive-shell fallback.
	EnvSupervisedProcess = "TALLPINE_SUPERVISED_PROCESS"
	// EnvDataDir tells a spawned agent's Tallpine hook commands where the store lives.
	EnvDataDir = "TALLPINE_DATA_DIR"
	// EnvPermissionMode tells hook commands which Tallpine approval policy applies.
	EnvPermissionMode = "TALLPINE_PERMISSION_MODE"
	// EnvRunFile tells spawned Tallpine hook commands which live daemon owns the
	// session. TALLPINE_DATA_DIR is durable storage, not daemon discovery; custom and
	// isolated daemons therefore need this coordinate explicitly.
	EnvRunFile = "TALLPINE_RUN_FILE"
	// EnvBrowserCapability proves ownership of the session's browser target.
	EnvBrowserCapability = "TALLPINE_BROWSER_CAPABILITY"
	// EnvSessionToken is the agent's scoped daemon credential. The `tallpine` CLI
	// sends it instead of the install token, and the daemon authorizes it
	// against the agent capability policy (docs/adr/0008).
	EnvSessionToken = "TALLPINE_SESSION_TOKEN" //nolint:gosec // Environment variable name, not a credential.
	// EnvSessionTokenRunFile names the run file of the daemon that issued
	// EnvSessionToken, so the CLI never sends the token to another daemon.
	EnvSessionTokenRunFile = "TALLPINE_SESSION_TOKEN_RUN_FILE" //nolint:gosec // Environment variable name, not a credential.
	// EnvSessionAuth and EnvSessionAuthRunFile repeat the two values above under
	// names a harness's credential scrub (KEY/TOKEN/SECRET/PASSWORD) leaves alone.
	EnvSessionAuth        = "TALLPINE_SESSION_AUTH"
	EnvSessionAuthRunFile = "TALLPINE_SESSION_AUTH_RUN_FILE"
	// EnvBrowserRuntimeToken must never be inherited by a worker. It authenticates
	// the privileged Electron runtime, not session-scoped browser callers.
	EnvBrowserRuntimeToken = "TALLPINE_BROWSER_RUNTIME_TOKEN" // #nosec G101 -- Environment variable name, not a credential.
	// EnvBrowserRuntimeTokenStdin is the daemon-only token handoff marker and
	// must be cleared before a worker process is spawned.
	EnvBrowserRuntimeTokenStdin = "TALLPINE_BROWSER_RUNTIME_TOKEN_STDIN" // #nosec G101 -- Environment variable name, not a credential.
)

type lifecycleRecorder interface {
	PrepareLaunch(id domain.SessionID, launchID string) error
	CancelLaunch(id domain.SessionID, launchID string)
	ReleaseLaunch(id domain.SessionID, launchID string)
	MarkSpawned(ctx context.Context, id domain.SessionID, metadata domain.SessionMetadata) error
	MarkChatReconnected(ctx context.Context, id domain.SessionID, metadata domain.SessionMetadata) error
	MarkChatSpawned(ctx context.Context, id domain.SessionID, metadata domain.SessionMetadata, boundary domain.ConversationBranch) error
	CommitControllerEpoch(ctx context.Context, id domain.SessionID, source, target domain.SessionMode, nativeConversationID string, startFresh bool) (bool, error)
	RestoreControllerEpoch(ctx context.Context, id domain.SessionID, source, target domain.SessionMode, nativeConversationID string, startFresh bool) (bool, error)
	ApplyActivitySignal(ctx context.Context, id domain.SessionID, signal ports.ActivitySignal) error
	MarkTerminated(ctx context.Context, id domain.SessionID) error
}

// ShellTerminalCloser gates a session's scoped shell terminals around every
// path that releases its worktree (Kill, Cleanup, RetireForReplacement, the
// explicit shutdown save-and-teardown path), so none of them removes a
// worktree out from under a shell whose cwd still points into it — on Windows
// an open handle on that directory can even make the removal itself fail.
//
// BeginSessionTeardown drains the session's open shells and, on success,
// blocks any new OpenShellTerminal for that session until the returned
// release function is called — the caller MUST call it exactly once
// (typically via defer) once its own worktree work finishes, whatever the
// outcome. That release is tied to this specific acquisition (a fresh
// closure), not looked up by session id, so it can never be confused with — or
// release — a different, unrelated Begin for the same session. An error from
// BeginSessionTeardown means some scoped runtime could not be confirmed dead;
// the caller MUST NOT touch the worktree in that case, and release is nil (the
// gate already released itself on that error path).
//
// Late-bound via SetShellTerminalCloser: shellterm.Service is built after
// Session Manager during boot (see daemon.startShellTerminals), mirroring why
// lifecycle.Manager takes its completion terminator the same way.
type ShellTerminalCloser interface {
	BeginSessionTeardown(ctx context.Context, id domain.SessionID) (release func(), err error)
}

// HarnessUseGate coordinates session lifecycle operations with harness binary
// replacement so a launch cannot observe a partially installed executable.
type HarnessUseGate interface {
	TryBeginHarnessUse(domain.AgentHarness) (release func(), ok bool)
}

// TerminalInputGate closes the raw terminal input path while an interface
// transition drains and stops a TUI controller. It is separate from Messenger:
// xterm keystrokes travel over the terminal mux and never pass through Send.
type TerminalInputGate interface {
	// BeginInputDrain atomically blocks later writes and returns the time of the
	// newest write that was accepted before the block. Session Manager uses that
	// barrier to avoid trusting an idle hook which predates already-buffered PTY
	// input.
	BeginInputDrain(terminalID string) (lastInputAt time.Time, release func())
}

// TerminalInputObserver is an optional TerminalInputGate capability: it reports
// the newest accepted keystroke without closing input. Keystrokes arriving while
// a drain is held are dropped, not buffered, so a read must never open a drain.
type TerminalInputObserver interface {
	LastInputAt(terminalID string) time.Time
}

// TerminalInputFencer is an optional TerminalInputGate capability: it holds one
// terminal's write fence so a keystroke cannot interleave with a Tallpine write,
// without dropping it (the keystroke blocks until release, unlike a drain). The
// composer guard holds it across the final draft proof and the paste so a human
// keystroke cannot land between them (#5711). lastInputAt is the newest
// keystroke accepted before the fence closed, read under it.
type TerminalInputFencer interface {
	HoldInput(terminalID string) (lastInputAt time.Time, release func())
}

// ReviewerTerminator tears down a worker's reviewer pane when the worker leaves
// its live lifecycle. It is late-bound like ShellTerminalCloser because review
// services are assembled after the session manager in daemon wiring.
type ReviewerTerminator interface {
	TerminateReviewer(ctx context.Context, workerID domain.SessionID, body string) error
	TeardownReviewerTerminal(ctx context.Context, workerID domain.SessionID) error
	RestoreReviewer(ctx context.Context, workerID domain.SessionID) error
}

type runtimeController interface {
	Create(ctx context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error)
	Destroy(ctx context.Context, handle ports.RuntimeHandle) error
	GetOutput(ctx context.Context, handle ports.RuntimeHandle, lines int) (string, error)
	// IsAlive reports whether the handle's runtime session still exists. Used by
	// Reconcile on boot to adopt crash-surviving sessions and reap leaked ones.
	IsAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error)
	ProbeFencedRuntime(ctx context.Context, ref ports.FencedRuntimeRef) ports.FencedProbeResult
}

// RestoreMode reports whether a restore continued an agent-native transcript or
// relaunched from Tallpine's saved task prompt.
type RestoreMode string

const (
	// RestoreModeNative means Tallpine relaunched through the agent's native transcript resume command.
	RestoreModeNative RestoreMode = "native"
	// RestoreModeSavedPrompt means Tallpine relaunched a new conversation from the saved task prompt.
	RestoreModeSavedPrompt RestoreMode = "saved_prompt"
	// RestoreModeFresh means Tallpine relaunched without a saved task prompt.
	RestoreModeFresh RestoreMode = "fresh"
)

// RestoreResult is the command result for a restored session.
type RestoreResult struct {
	Session domain.SessionRecord
	Mode    RestoreMode
}

// Store is the persistence surface needed by the internal session Manager.
type Store interface {
	// GetProject loads a project row so spawn can resolve its per-project agent
	// config into the launch command. ok=false means the project is unknown.
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
	ListWorkspaceRepos(ctx context.Context, projectID string) ([]domain.WorkspaceRepoRecord, error)
	CreateSession(ctx context.Context, rec domain.SessionRecord) (domain.SessionRecord, error)
	CreateAutomationSession(ctx context.Context, rec domain.SessionRecord) (domain.SessionRecord, bool, error)
	CreateClientRequestSession(ctx context.Context, rec domain.SessionRecord) (domain.SessionRecord, bool, error)
	GetSessionByClientRequestID(ctx context.Context, id string) (domain.SessionRecord, bool, error)
	CommitClientRequestSession(ctx context.Context, id domain.SessionID) error
	UpdateSession(ctx context.Context, rec domain.SessionRecord) error
	UpdateSessionModel(ctx context.Context, id domain.SessionID, model string) (bool, error)
	RenameSessionIfDisplayName(ctx context.Context, id domain.SessionID, currentDisplayName, displayName string, updatedAt time.Time) (bool, error)
	// UpdateSessionArtifactOutput is the only write that changes artifact_dir
	// and session_output_type on an existing row; UpdateSession leaves them
	// untouched so a stale full-row write cannot revert a reconcile.
	UpdateSessionArtifactOutput(ctx context.Context, id domain.SessionID, artifactDir string, outputType domain.SessionOutputType) (bool, error)
	UpdateBrowserCapabilityVerifier(ctx context.Context, id domain.SessionID, expected domain.SessionControllerOwner, verifier string) (bool, error)
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	UpsertSessionCleanupFacts(ctx context.Context, rec domain.SessionCleanupRecord) error
	GetSessionCleanupFacts(ctx context.Context, id domain.SessionID) (domain.SessionCleanupRecord, bool, error)
	ListSessions(ctx context.Context, project domain.ProjectID) ([]domain.SessionRecord, error)
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
	ListLiveSessions(ctx context.Context) ([]domain.SessionRecord, error)
	ListChatHibernationCandidates(ctx context.Context) ([]domain.SessionID, error)
	// DeleteSession removes a session row only if it is still in seed state
	// (no workspace, runtime handle, agent session id, or prompt; not
	// terminated). Returns deleted=true when removal happened; deleted=false
	// when the row had already progressed past seed state — preserving the
	// no-resurrection guarantee for live sessions.
	DeleteSession(ctx context.Context, id domain.SessionID) (bool, error)
	// UpsertSessionWorktree records or updates the worktree row for a session.
	// SaveAndTeardownAll writes the preserved_ref here (even when empty) as the
	// "shutdown-saved" marker before ForceDestroying the worktree.
	UpsertSessionWorktree(ctx context.Context, row domain.SessionWorktreeRecord) error
	// ListSessionWorktrees returns every worktree row for a session. RestoreAll
	// uses this to identify sessions saved by the last SaveAndTeardownAll: the
	// presence of any row is the marker; preserved_ref may be empty for clean
	// worktrees.
	ListSessionWorktrees(ctx context.Context, id domain.SessionID) ([]domain.SessionWorktreeRecord, error)
	// DeleteSessionWorktrees consumes stale shutdown-restore markers. Explicit
	// Kill and successful RestoreAll must remove these rows to prevent
	// resurrecting sessions the user intentionally terminated.
	DeleteSessionWorktrees(ctx context.Context, id domain.SessionID) error
	SetSessionProvisionState(ctx context.Context, id domain.SessionID, state domain.SessionProvisionState, message string, now time.Time) (bool, error)
	SetSessionProvisionSteps(ctx context.Context, id domain.SessionID, steps []domain.SessionProvisionStep, now time.Time) error
	SetSessionProvisionedWorkspace(ctx context.Context, id domain.SessionID, branch, workspacePath, workspaceRepoPath string, now time.Time) (bool, error)
	SetSessionTaskBranch(ctx context.Context, id domain.SessionID, expectedBranch, branch string, repoBranches map[string]string, now time.Time) (bool, error)
	SetSessionBranchNaming(ctx context.Context, id domain.SessionID, naming bool) error
	ClearSessionBranchNaming(ctx context.Context) error
	SetTaskPreparationBase(ctx context.Context, id domain.SessionID, baseSHA, baseRef string) (bool, error)
	PromoteTaskPreparation(ctx context.Context, id domain.SessionID, rec domain.SessionRecord) (bool, error)
	DeleteTaskPreparation(ctx context.Context, id domain.SessionID) (bool, error)
}

// conversationSettingsStore is the narrow optional read boundary for deriving
// a chat orchestrator's current approval mode during a worker spawn. Older
// embedders without chat persistence retain project-config-only behavior.
type conversationSettingsStore interface {
	ConversationForSession(ctx context.Context, session domain.SessionID) (domain.ConversationRecord, error)
}

// Manager coordinates internal session spawn, restore, kill, and cleanup over
// the outbound ports. User-facing read-model assembly lives in the service package.
type Manager struct {
	// beforeHibernate runs before idle hibernation stops a Chat; true keeps
	// it awake this pass (SetBeforeHibernate).
	beforeHibernate func(context.Context, domain.SessionID) (bool, error)
	// startFailed hears about a background chat start that gave up, so the
	// worker's orchestrator learns it is not running. Nil is a no-op.
	startFailed func(domain.SessionID, string)
	runtime     runtimeController
	agents      ports.AgentResolver
	workspace   ports.Workspace
	store       Store
	// agentReadiness is late-bound by SetAgentReadiness.
	agentReadiness ports.AgentReadinessProvider
	// messenger is a sessionguard.Guard wrapping the raw messenger, so every
	// pane write is guarded (re-read state, refuse a blocked session) without
	// each call site re-deriving the check. Send/confirmActive use Deliver for
	// its Outcome; mutation-owned handoff/startup prompts use the explicit
	// admitted path while ordinary input remains fenced out.
	messenger *sessionguard.Guard
	// chat launches the structured controller for a chat-mode session. Nil means
	// this build cannot run chat sessions. Explicit Chat requests are refused;
	// an inherited Chat preference falls back to TUI.
	// defaults resolves the daemon-owned default session interface for a spawn
	// that names no mode. Nil falls back to the compatibility default, so a build
	// without it behaves exactly as before.
	defaults     SessionModeDefaults
	chat         ChatLauncher
	modelCatalog interface {
		Models(context.Context, string, string, bool) (ports.AgentModelCatalog, error)
	}
	lcm                 lifecycleRecorder
	preview             PreviewLifecycle
	browser             BrowserLifecycle
	browserCapabilities BrowserCapabilityIssuer
	sessionTokens       SessionTokenIssuer
	agentAccounts       ports.AgentAccountLaunchEnv
	githubAccounts      GitHubAccountTokens
	policy              PolicyEngine
	// goos overrides runtime.GOOS for decisions that depend on it (tests only).
	goos              string
	accountMovedMu    sync.RWMutex
	accountMoved      func(domain.AgentHarness)
	accountOverrideMu sync.Mutex
	accountOverrides  map[domain.SessionID]string
	attachments       *attachmentstore.Store
	attachmentSuffix  func() (string, error)
	dataDir           string
	runFilePath       string
	sessionGradleHome bool
	// cleanupCheck is the check Cleanup and a CleanupSession without one run
	// under the teardown guard (see SetCleanupCheck).
	cleanupCheck CleanupCheck
	// plannerEnabled adds the plan-before-build section to orchestrator prompts.
	plannerEnabled              bool
	clock                       func() time.Time
	reconcileWorkers            int
	defaultBranchRefreshTimeout time.Duration
	taskPreparationsMu          sync.Mutex
	taskPreparations            map[domain.TaskPreparationToken]*taskPreparation
	taskNamingSlots             chan struct{}
	taskPreparationTTL          time.Duration
	// Snapshot before the listener binds; background cleanup must never reclaim
	// a preparation opened in this daemon after the listener is live.
	startupTaskPreparations []domain.SessionRecord
	// Only pre-listener provisioning writes that failed may be retried after
	// bind; scanning then would also sweep tasks created by this daemon.
	startupProvisioningRetries []domain.SessionRecord
	// runBackground runs an asynchronous spawn's remaining work. Nil means a
	// plain goroutine; tests substitute a synchronous runner.
	runBackground     func(func())
	backgroundWorkers sync.WaitGroup
	asyncChatSpawnsMu sync.Mutex
	asyncChatSpawns   map[domain.SessionID]*asyncChatSpawnRun
	// killTeardown bounds Kill's detached teardown. Zero means
	// killTeardownBudget; tests shrink it to prove what survives its expiry.
	killTeardown time.Duration
	// openTranscriptFile is os.Open in production. The narrow seam lets tests
	// deterministically prove that a post-stop transcript read failure falls
	// back without advertising the provider path.
	openTranscriptFile func(string) (*os.File, error)
	harnessUseGate     HarnessUseGate
	// lookPath is exec.LookPath in production; tests substitute a stub so
	// they don't need real binaries on PATH. Returns ports.ErrAgentBinaryNotFound
	// when the binary is missing so the sentinel propagates through toAPIError.
	lookPath func(string) (string, error)
	// executable resolves the daemon's own binary (os.Executable in
	// production); its directory is prepended to spawned sessions' PATH so the
	// workspace hook commands resolve back to this daemon. Tests inject a stub.
	executable                     func() (string, error)
	newLaunchID                    func() string
	codexOperationGate             ports.CodexOperationGate
	startupBackgroundReconcileDone chan struct{}
	startupBackgroundReconcileOnce sync.Once
	// A cold Chat resume must not launch a new persistent host while startup's
	// orphan-host sweep is using its earlier snapshot of sleeping sessions.
	persistentHostReconcileDone <-chan struct{}
	statusRecoveryMu            sync.RWMutex
	statusRecoveryFailed        bool
	statusRecoveryRevision      uint64
	statusRecoveries            map[domain.SessionID]statusRecovery
	statusReadinessSubsMu       sync.Mutex
	statusReadinessSubs         map[int]func()
	statusReadinessNextSub      int
	statusVerificationLimit     time.Duration
	startupChatRetryDelays      []time.Duration
	startupChatResumeDelays     []time.Duration
	startupChatResumeSem        chan struct{}
	startupChatResume           func(context.Context, domain.SessionID) (bool, error)
	startupChatResumeMu         sync.Mutex
	startupChatResumePending    map[domain.SessionID]struct{}
	agentOpMu                   sync.Mutex
	agentOperations             map[domain.SessionID]agentOperationKind
	agentOperationSince         map[domain.SessionID]time.Time
	inputLeaseSince             map[domain.SessionID]time.Time
	// autoResumes counts automatic (wake, startup, version-move) resumes per
	// session, guarded by agentOpMu, so a user Resume can tell whose lock it hit.
	autoResumes map[domain.SessionID]int
	// reconcileQueued marks sessions a live reconcile pass has reserved but no
	// worker has started; Kill takes such a slot over at once (B541).
	reconcileQueued map[domain.SessionID]struct{}
	// launchStartWait bounds the wait to see a new supervised launch alive.
	launchStartWait           time.Duration
	interfaceRecoveryMu       sync.Mutex
	deferredInterfaceRecovery map[domain.SessionID]string
	inputLeases               map[domain.SessionID]int
	inputDrained              map[domain.SessionID]chan struct{}
	// backgroundContext owns asynchronous agent-switch execution independently
	// of the admitting request. The daemon cancels it before waiting for workers.
	backgroundContext        context.Context
	agentSwitchWorkers       sync.WaitGroup
	agentSwitchWorkerMu      sync.Mutex
	agentSwitchWorkersClosed bool

	transitionMu sync.Mutex
	transitions  map[domain.SessionID]*interfaceTransitionRun
	// transitionDeliveryWake drives the durable transition-message outbox. A
	// daemon-lifetime worker is started by Reconcile; terminal transition paths
	// also make one immediate delivery attempt so tests and in-process callers do
	// not depend on the boot worker.
	transitionDeliveryMu        sync.Mutex
	transitionDeliveryRunning   bool
	transitionDeliveryWake      chan struct{}
	transitionDeliveryAttemptMu sync.Mutex
	// sendConfirm bounds the best-effort post-send confirmation that the session
	// actually became active (the agent accepted the prompt). New fills in the
	// sendConfirm* defaults; tests in this package shrink the timings directly.
	sendConfirm sendConfirmConfig
	// interfaceTransition bounds only contradictory stale-idle proof. Turns and
	// user-paced waits reported through the activity boundary remain unbounded.
	interfaceTransition interfaceTransitionConfig
	logger              *slog.Logger

	// shellTerminalsMu guards shellTerminals: it is late-bound (see
	// ShellTerminalCloser) after Manager already exists, so a setter mutates it
	// under lock rather than through the constructor.
	shellTerminalsMu sync.Mutex
	shellTerminals   ShellTerminalCloser

	terminalInputGateMu sync.Mutex
	terminalInputGate   TerminalInputGate
	// updateStops holds input gates of terminals stopped for an update.
	updateStops updateStops
	// versionRetries are sessions a version move could not start again.
	versionRetries versionRetries
	// versionMoves bounds the version moves restarting sessions at once.
	versionMoves versionMoveSlots
	// versionPolicy says whether and when sessions move to a new release;
	// nil means they do not (see SetVersionRefreshPolicy).
	versionPolicyMu sync.Mutex
	versionPolicy   func() VersionRefreshPolicy

	reviewersMu sync.Mutex
	reviewers   ReviewerTerminator

	// workspaceGateMu guards workspaceGates: per-project gates that coordinate
	// workspace lifecycle operations (spawn, restore, cleanup) so live sessions
	// and shared worktrees are not torn down concurrently.
	workspaceGateMu sync.Mutex
	workspaceGates  map[domain.ProjectID]*sync.RWMutex
}
