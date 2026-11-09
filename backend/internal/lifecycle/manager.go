// Package lifecycle implements the synchronous reducer that writes durable
// session lifecycle facts. It deliberately keeps the session model small:
// activity_state plus an is_terminated bit are the only persisted status-like
// facts on the session row.
package lifecycle

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/sessionguard"
)

type sessionStore interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	UpdateSession(ctx context.Context, rec domain.SessionRecord) error
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
	// UpdateSessionFromActivitySignal is a narrow, owner-generation-fenced
	// write. It returns false when a concurrent lifecycle/controller boundary
	// made the reducer's previously read session stale.
	UpdateSessionFromActivitySignal(ctx context.Context, rec domain.SessionRecord, expectedRevision int64) (bool, error)
	// UpdateSessionArtifactOutput is a narrow write touching only
	// artifact_dir and session_output_type. ReconcileSessionOutputType uses
	// it instead of a read-modify-write UpdateSession so a stale in-memory
	// read can never replay is_terminated, activity, runtime identity, or
	// preview state backwards over a newer concurrent write.
	UpdateSessionArtifactOutput(ctx context.Context, id domain.SessionID, artifactDir string, outputType domain.SessionOutputType) (bool, error)
	// ListSessions returns every session in a project. The dispatcher reads it
	// to resolve the current orchestrator at delivery time.
	ListSessions(ctx context.Context, project domain.ProjectID) ([]domain.SessionRecord, error)
	// ListPRsBySession returns every PR row tracked for the session. The
	// reducer reads it to apply the multi-PR completion rule (terminate only
	// when no open PR remains and at least one merged) and to suppress
	// merge-conflict nudges on PRs stacked behind an open parent.
	ListPRsBySession(ctx context.Context, id domain.SessionID) ([]domain.PullRequest, error)
	GetPR(ctx context.Context, prURL string) (domain.PullRequest, bool, error)
	// ListPRReviews and ListPRComments return the effective rows committed by
	// the SCM observer, including each item's preserved injection decision.
	ListPRReviews(ctx context.Context, prURL string) ([]domain.PullRequestReview, error)
	ListPRComments(ctx context.Context, prURL string) ([]domain.PullRequestComment, error)
	// GetPRLastNudgeSignature / UpdatePRLastNudgeSignature persist the
	// reaction-dedup map so nudges survive a daemon restart.
	GetPRLastNudgeSignature(ctx context.Context, prURL string) (string, error)
	UpdatePRLastNudgeSignature(ctx context.Context, prURL, payload string) error
}

// controllerEpochStore is the atomic persistence primitive used by
// CommitControllerEpoch. It stays optional on the broad lifecycle store so
// focused reducer fakes do not need controller-transition methods; production
// SQLite implements it.
type controllerEpochStore interface {
	CommitSessionControllerEpoch(
		context.Context,
		domain.SessionID,
		domain.SessionMode,
		domain.SessionMode,
		string,
		time.Time,
	) (bool, error)
	RestoreSessionControllerEpoch(
		context.Context,
		domain.SessionID,
		domain.SessionMode,
		domain.SessionMode,
		string,
		time.Time,
	) (bool, error)
}

// interfaceTransitionReader lets the activity reducer reject ownerless TUI
// callbacks while an interface handoff is restoring that controller. It stays
// optional so focused reducer fakes do not need the transition saga surface.
type interfaceTransitionReader interface {
	GetActiveSessionInterfaceTransition(
		context.Context,
		domain.SessionID,
	) (domain.SessionInterfaceTransition, bool, error)
}

// chatSpawnStore commits the lifecycle facts and the provider boundary in one
// transaction. A fresh provider must never become the durable session owner
// while the conversation head still names the provider it replaced.
type chatSpawnStore interface {
	CommitChatSpawn(context.Context, domain.SessionRecord, domain.ConversationBranch) error
}

// preparedChatSpawnStore extends the atomic Chat publication boundary with
// native-history projection. The callback runs on the same SQLite transaction
// after the provider boundary and generation are staged, but before the live
// session record is published. It remains optional for focused lifecycle fakes.
type preparedChatSpawnStore interface {
	CommitChatSpawnPrepared(
		context.Context,
		domain.SessionRecord,
		domain.ConversationBranch,
		*domain.ChatProviderHandoff,
		func(context.Context) error,
	) error
}

// notificationSink is the optional lifecycle-to-notification-producer boundary.
type notificationSink interface {
	Notify(ctx context.Context, intent ports.NotificationIntent) error
	// Resolve closes notifications whose underlying issue went away. It is the
	// only way a notification leaves the unresolved list: there is no manual
	// user-facing resolve action.
	Resolve(ctx context.Context, res ports.NotificationResolution) error
}

// projectConfigLoader resolves a project's config so MarkTerminated can check
// the ContainerReap opt-out before reaping. A load failure must not fall
// through to reaping - see ports.ContainerReaper below.
type projectConfigLoader interface {
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
}

type sessionTerminator interface {
	Kill(ctx context.Context, id domain.SessionID) (bool, error)
}

type sessionUsageFinalizer interface {
	FinalizeSession(
		ctx context.Context,
		id domain.SessionID,
		expectedRuntimeLaunchID string,
		expectedSessionRevision int64,
	) error
}

type sessionUsageReactivator interface {
	ReactivateSession(ctx context.Context, id domain.SessionID, expectedRuntimeLaunchID string) error
}

type sessionOperationGate interface {
	SessionMutationInProgress(id domain.SessionID) bool
}

type pendingLaunch struct {
	launchID string
	ready    chan struct{}
}

// Option customizes a Manager.
type Option func(*Manager)

// WithNotificationSink wires lifecycle notification intents to a write-side producer.
func WithNotificationSink(sink notificationSink) Option {
	return func(m *Manager) { m.notifications = sink }
}

// WithDataDir supplies Tallpine's data directory so ReconcileSessionOutputType can
// derive a session's artifact directory when its stored value is empty (a
// row created before that column existed).
func WithDataDir(dir string) Option {
	return func(m *Manager) { m.dataDir = dir }
}

// WithProcessReaper makes every terminal path stop the processes the session
// left running (see sessionreap), including detached ones.
func WithProcessReaper(reaper sessionProcessReaper) Option {
	return func(m *Manager) { m.processes = reaper }
}

// WithContainerReaper wires the container leg of #2652: MarkTerminated will
// force-remove the terminated session's tallpine.session-labeled Docker containers,
// unless the project opts out via ProjectConfig.ContainerReap.Disabled.
func WithContainerReaper(reaper ports.ContainerReaper, projects projectConfigLoader) Option {
	return func(m *Manager) {
		m.containers = reaper
		m.projects = projects
	}
}

// WithActiveSteering supplies the adapter-provided active-turn steering
// capability (see ports.ActiveTurnSteerer). Without it the reducer assumes no
// harness can be steered mid-turn.
func WithActiveSteering(pred func(domain.AgentHarness) bool) Option {
	return func(m *Manager) {
		if pred != nil {
			m.steerActive = pred
		}
	}
}

// WithStartupSignalGate supplies the adapter capability predicate used to
// suppress reaction writes until a TUI's first startup-ready hook arrives.
func WithStartupSignalGate(pred func(domain.AgentHarness) bool) Option {
	return func(m *Manager) {
		if pred != nil {
			m.startupSignalGatesInput = pred
		}
	}
}

// WithUrgentNudgeGate supplies the adapter capability predicate that decides
// whether an urgent (merge-conflict) nudge may reach a session at a
// waiting_input prompt: true only for harnesses that report a permission dialog
// as blocked rather than as waiting_input. Without it the reducer treats every
// waiting_input prompt as a possible masked decision and withholds the urgent
// nudge until the session is active or idle.
func WithUrgentNudgeGate(pred func(domain.AgentHarness) bool) Option {
	return func(m *Manager) {
		if pred != nil {
			m.urgentNudgeWaitingInputSafe = pred
		}
	}
}

// Manager reduces runtime, activity, spawn, and termination observations into durable session facts.
// It also owns agent nudges caused by PR observations, including merge-conflict, CI-failure, and review-feedback prompts.
type Manager struct {
	store sessionStore
	// reconcileMu serializes ReconcileSessionOutputType calls so an older scan
	// can never persist over a newer one. It is separate from mu, so a slow
	// artifact walk never blocks lifecycle mutations.
	reconcileMu sync.Mutex
	// guard is the shared pane-write primitive every reaction nudge goes
	// through (see sessionguard). Nil when no messenger was wired: reaction
	// nudges become no-ops but the reducer still runs.
	guard         *sessionguard.Guard
	notifications notificationSink
	// completionTerminator is late-bound because Session Manager itself depends
	// on this lifecycle reducer. It is required before the SCM observer starts.
	completionTerminator sessionTerminator
	// mergeNotifier announces a merged PR to the orchestrators it concerns; optional.
	mergeNotifier mergeNotifier
	// autoClose gates and announces the end of a finished worker; optional.
	autoClose autoCloseHooks
	// autoCloseRetry re-arms a close held by a running job (B494).
	autoCloseRetry autoCloseRetry
	// usageFinalizer is late-bound because the usage pipeline is optional. It
	// receives terminal intent before is_terminated makes the session ineligible
	// for normal source discovery.
	usageFinalizer   sessionUsageFinalizer
	usageReactivator sessionUsageReactivator
	containers       ports.ContainerReaper
	processes        sessionProcessReaper
	projects         projectConfigLoader
	operationGateMu  sync.RWMutex
	orchestratorIdle func(domain.SessionRecord) // guarded by operationGateMu
	operationGate    sessionOperationGate
	// dataDir backs ReconcileSessionOutputType's artifact-dir fallback for
	// sessions rows created before session_output_type/artifact_dir existed.
	dataDir string

	// sessionLocks serializes each session's read-reduce-write sequences
	// (activity signals, runtime observations, spawn and controller commits)
	// across their store round trips.
	sessionLocks sessionLocks
	// mu guards the in-memory per-session maps below and the late-bound
	// collaborators. It is held only around memory access, never across a
	// store call, and is always taken after a session lock, never before.
	mu     sync.Mutex
	window time.Duration
	clock  func() time.Time
	react  reactionState
	// flights tracks, per session, the in-flight tool executions and the
	// pending permission dialog's identity (see toolFlight). Guarded by mu.
	flights map[domain.SessionID]*toolFlight
	// steps is each session's recent tool calls for the memory window, from
	// tool-use hooks (see steps.go). Guarded by mu.
	steps map[domain.SessionID][]domain.SessionStep
	// pendingLaunches closes the small ordering gap between starting a supervised
	// process and durably recording its generation in MarkSpawned. A hook from
	// that exact generation waits on ready instead of being discarded as stale.
	// This coordination is intentionally memory-only: a daemon crash leaves the
	// durable session exited, so the user can safely retry the resume.
	pendingLaunches map[domain.SessionID]pendingLaunch
	// steerActive reports whether a harness can safely receive a write during an
	// active turn (input steers the run) rather than only while idle. Supplied by
	// the agent adapter via WithActiveSteering; the default answers false, so an
	// unknown harness is only written to while idle.
	steerActive             func(domain.AgentHarness) bool
	startupSignalGatesInput func(domain.AgentHarness) bool
	// urgentNudgeWaitingInputSafe reports whether a harness surfaces a permission
	// dialog AS blocked (rather than as waiting_input), so an urgent merge-conflict
	// nudge is safe to paste at a waiting_input prompt. Supplied by the agent
	// adapter via WithUrgentNudgeGate; the default answers false, so an unknown
	// harness never takes an urgent write while waiting_input.
	urgentNudgeWaitingInputSafe func(domain.AgentHarness) bool
	// threadReader re-reads review threads before a review comment is
	// delivered (see review_comment_delivery.go). Late-bound; guarded by mu.
	threadReader ReviewThreadReader
	// deferredDeliveries tracks turn-end review-comment deliveries in flight.
	deferredDeliveries sync.WaitGroup
	// deferMu guards deferredSessions, the sessions holding a deferred review
	// comment. It is a leaf lock so the activity-hook path can read it without
	// ever waiting on react.mu (see deliverDeferredAfterTurn).
	deferMu          sync.Mutex
	deferredSessions map[domain.SessionID]bool
	// turnClaims admits review-comment writes one turn at a time per session,
	// across every delivery path (see turnClaim). Guarded by deferMu.
	turnClaims map[domain.SessionID]turnClaim
	claimSeq   uint64
}

// New builds a Lifecycle Manager over the session store it writes and the messenger it uses for agent nudges.
func New(store sessionStore, messenger ports.AgentMessenger, opts ...Option) *Manager {
	// UTC so activity-driven LastActivityAt/UpdatedAt match spawn-stamped
	// timestamps (the session manager clock is UTC too); a local clock here left
	// `tallpine session get` showing created in UTC but updated in local time. A
	// WithClock option may still override this in tests.
	clock := func() time.Time { return time.Now().UTC() }
	m := &Manager{
		store:                       store,
		window:                      defaultRecentActivityWindow,
		clock:                       clock,
		react:                       newReactionState(),
		flights:                     map[domain.SessionID]*toolFlight{},
		steps:                       map[domain.SessionID][]domain.SessionStep{},
		pendingLaunches:             map[domain.SessionID]pendingLaunch{},
		steerActive:                 func(domain.AgentHarness) bool { return false },
		startupSignalGatesInput:     func(domain.AgentHarness) bool { return false },
		urgentNudgeWaitingInputSafe: func(domain.AgentHarness) bool { return false },
	}
	if messenger != nil {
		m.guard = sessionguard.New(store, messenger, nil)
	}
	for _, opt := range opts {
		opt(m)
	}
	if m.guard != nil {
		m.guard.SetStartupSignalGate(m.startupSignalGatesInput)
	}
	return m
}

// SetCompletionTerminator wires merge completion to the same teardown path as
// an explicit user kill.
func (m *Manager) SetCompletionTerminator(terminator sessionTerminator) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.completionTerminator = terminator
}

// mergeNotifier tells orchestrators about a merged PR (B376).
type mergeNotifier interface {
	NotifyMerge(ctx context.Context, project domain.ProjectID, prURL string)
}

// SetMergeNotifier wires merge notices to lane and main orchestrators.
func (m *Manager) SetMergeNotifier(n mergeNotifier) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mergeNotifier = n
}

// SetUsageFinalizer wires termination and relaunches to usage collection.
// Telemetry failures never block the lifecycle transition.
func (m *Manager) SetUsageFinalizer(finalizer sessionUsageFinalizer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.usageFinalizer = finalizer
	m.usageReactivator, _ = finalizer.(sessionUsageReactivator)
}

// SetSessionInputLease late-binds Session Manager's pane-input authority into
// lifecycle's guarded reaction writes. Lifecycle is constructed first during
// daemon boot, so constructor injection would create a dependency cycle.
func (m *Manager) SetSessionInputLease(lease sessionguard.InputLease) {
	if m.guard != nil {
		m.guard.SetInputLease(lease)
	}
}

// Nudge writes a Tallpine-initiated message to a live agent under the same guard as
// reaction nudges: it is refused while the session waits on its user, has not
// signalled startup, or is gone. It reports whether the message was sent.
func (m *Manager) Nudge(ctx context.Context, id domain.SessionID, message string) (bool, error) {
	if m.guard == nil {
		return false, nil
	}
	outcome, err := m.guard.Nudge(ctx, id, message)
	return outcome == sessionguard.Sent, err
}

// SetSessionOperationGate prevents observation-driven terminal facts from
// racing Tallpine's deliberate provider replacement/relaunch operations.
func (m *Manager) SetSessionOperationGate(gate sessionOperationGate) {
	m.operationGateMu.Lock()
	m.operationGate = gate
	m.operationGateMu.Unlock()
}

func (m *Manager) sessionMutationInProgress(id domain.SessionID) bool {
	m.operationGateMu.RLock()
	gate := m.operationGate
	m.operationGateMu.RUnlock()
	return gate != nil && gate.SessionMutationInProgress(id)
}

// PrepareLaunch registers a supervised generation before the runtime starts.
// Hooks from that exact generation wait until MarkSpawned commits the generation
// instead of racing the old durable generation and being discarded as stale.
func (m *Manager) PrepareLaunch(id domain.SessionID, launchID string) error {
	launchID = strings.TrimSpace(launchID)
	if launchID == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if pending, ok := m.pendingLaunches[id]; ok {
		if pending.launchID == launchID {
			return nil
		}
		return fmt.Errorf("lifecycle: session %q already has launch %q in progress", id, pending.launchID)
	}
	m.pendingLaunches[id] = pendingLaunch{launchID: launchID, ready: make(chan struct{})}
	return nil
}

// CancelLaunch releases hooks waiting on a generation whose runtime failed to
// start. Once released, normal generation fencing discards those signals.
func (m *Manager) CancelLaunch(id domain.SessionID, launchID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finishLaunchLocked(id, strings.TrimSpace(launchID))
}

// ReleaseLaunch publishes that the caller has durably committed the prepared
// generation. Hooks waiting behind PrepareLaunch may now re-read the session
// row and pass normal generation fencing. Unlike MarkSpawned, this does not
// rewrite session ownership.
func (m *Manager) ReleaseLaunch(id domain.SessionID, launchID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finishLaunchLocked(id, strings.TrimSpace(launchID))
}

func (m *Manager) finishLaunchLocked(id domain.SessionID, launchID string) {
	if launchID == "" {
		return
	}
	pending, ok := m.pendingLaunches[id]
	if !ok || pending.launchID != launchID {
		return
	}
	delete(m.pendingLaunches, id)
	close(pending.ready)
}

func (m *Manager) finishLaunch(id domain.SessionID, launchID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finishLaunchLocked(id, launchID)
}

// dropSessionState forgets all of a session's in-memory reducer state.
func (m *Manager) dropSessionState(id domain.SessionID) {
	m.mu.Lock()
	delete(m.flights, id)
	delete(m.steps, id)
	m.mu.Unlock()
	m.forgetTurnClaim(id)
}

func (m *Manager) currentUsageFinalizer() sessionUsageFinalizer {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.usageFinalizer
}

func (m *Manager) currentUsageReactivator() sessionUsageReactivator {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.usageReactivator
}

func (m *Manager) mutate(ctx context.Context, id domain.SessionID, fn func(domain.SessionRecord, time.Time) (domain.SessionRecord, bool)) error {
	unlock := m.sessionLocks.lock(id)
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		unlock()
		return err
	}
	now := m.clock()
	next, changed := fn(rec, now)
	if !changed {
		unlock()
		return nil
	}
	next.UpdatedAt = now
	if err := m.store.UpdateSession(ctx, next); err != nil {
		unlock()
		return err
	}
	unlock()
	// Notification side effects run outside the session lock, like the activity
	// path does: a slow notification store must never stall lifecycle writes.
	m.resolveNotifications(ctx, needsInputResolutions(rec, next, now)...)
	return nil
}
