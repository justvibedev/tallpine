package session

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
)

const maxDisplayNameLen = 100

// Store is the read-only persistence surface needed to assemble controller-facing session read models.
type Store interface {
	GetSessionCleanupFacts(ctx context.Context, id domain.SessionID) (domain.SessionCleanupRecord, bool, error)
	ListSessionCleanupFactsForSessions(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID]domain.SessionCleanupRecord, error)
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	GetSessionByClientRequestID(ctx context.Context, id string) (domain.SessionRecord, bool, error)
	ListSessions(ctx context.Context, project domain.ProjectID) ([]domain.SessionRecord, error)
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
	ListLiveSessions(ctx context.Context) ([]domain.SessionRecord, error)
	CountArchivedSessions(ctx context.Context) ([]domain.ArchivedSessionCount, error)
	RenameSession(ctx context.Context, id domain.SessionID, displayName string, updatedAt time.Time) (bool, error)
	RenameSessionIfDisplayName(ctx context.Context, id domain.SessionID, currentDisplayName, displayName string, updatedAt time.Time) (bool, error)
	SetSessionPreviewURL(ctx context.Context, id domain.SessionID, previewURL string, updatedAt time.Time) (bool, error)
	SetSessionTerminateOnPRMerge(ctx context.Context, id domain.SessionID, terminate bool, updatedAt time.Time) (bool, error)
	SetSessionAutoInjectReview(ctx context.Context, id domain.SessionID, autoInject bool, updatedAt time.Time) (bool, error)
	SetSessionAutoInjectCI(ctx context.Context, id domain.SessionID, autoInject bool, updatedAt time.Time) (bool, error)
	SetSessionPinned(ctx context.Context, id domain.SessionID, isPinned bool, pinnedAt *time.Time, updatedAt time.Time) (bool, error)
	SetSessionReviewerConfig(ctx context.Context, id domain.SessionID, harness domain.ReviewerHarness, config domain.AgentConfig, updatedAt time.Time) (bool, error)
	SetSessionAutoReview(ctx context.Context, id domain.SessionID, enabled bool, updatedAt time.Time) (bool, error)
	GetDisplayPRFactsForSession(ctx context.Context, id domain.SessionID) (domain.PRFacts, bool, error)
	ListPRFactsForSession(ctx context.Context, id domain.SessionID) ([]domain.PRFacts, error)
	ListPRFactsForSessions(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID][]domain.PRFacts, error)
	ListCurrentHeadReviewRunsForSession(ctx context.Context, id domain.SessionID) ([]domain.CurrentHeadReviewRun, error)
	ListCurrentHeadReviewRunsForSessions(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID][]domain.CurrentHeadReviewRun, error)
	ListInterruptedSessions(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID]bool, error)
	ListPRsBySession(ctx context.Context, sessionID domain.SessionID) ([]domain.PullRequest, error)
	ListReportedPRURLs(ctx context.Context, id domain.SessionID) ([]string, error)
	ListSessionWorktrees(ctx context.Context, id domain.SessionID) ([]domain.SessionWorktreeRecord, error)
	ListChecks(ctx context.Context, prURL string) ([]domain.PullRequestCheck, error)
	ListPRReviews(ctx context.Context, prURL string) ([]domain.PullRequestReview, error)
	ListPRReviewThreads(ctx context.Context, prURL string) ([]domain.PullRequestReviewThread, error)
	ListPRComments(ctx context.Context, prURL string) ([]domain.PullRequestComment, error)
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
	ListWorkspaceRepos(ctx context.Context, projectID string) ([]domain.WorkspaceRepoRecord, error)
}

// ListFilter captures API-facing session list query filters.
type ListFilter struct {
	ProjectID        domain.ProjectID
	Active           *bool
	OrchestratorOnly bool
	Fresh            bool
	// Standalone keeps only sessions that belong to no project.
	Standalone bool
	// Kind keeps one kind of session. KindWorker matches every session that is
	// not an orchestrator, so rows written before kinds existed still list.
	Kind domain.SessionKind
	// Limit pages the list: at most Limit sessions, pinned first and then most
	// recently updated, resuming after Cursor. Zero lists every match in store
	// order, as before paging.
	Limit  int
	Cursor string
}

// commander is the command-side surface Service delegates to: the
// *sessionmanager.Manager in production, a fake in tests.
type commander interface {
	Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.SessionRecord, int, int, error)
	RunBackgroundTask(ctx context.Context, id domain.SessionID, systemPrompt, prompt string) (string, error)
	RestoreWithMode(ctx context.Context, id domain.SessionID) (sessionmanager.RestoreResult, error)
	ResumeAgentWithMode(ctx context.Context, id domain.SessionID) (sessionmanager.RestoreResult, error)
	Kill(ctx context.Context, id domain.SessionID) (bool, error)
	RequestKill(ctx context.Context, id domain.SessionID) (sessionmanager.KillResult, error)
	RetireForReplacement(ctx context.Context, id domain.SessionID) error
	WaitForMessageDeliveryReady(ctx context.Context, id domain.SessionID) error
	Send(ctx context.Context, id domain.SessionID, message string, attachment *ports.SpawnAttachment) error
	SendWithOptions(ctx context.Context, id domain.SessionID, message string, attachment *ports.SpawnAttachment, options ports.MessageDeliveryOptions) error
	Cleanup(ctx context.Context, project domain.ProjectID) (sessionmanager.CleanupResult, error)
	RollbackSpawn(ctx context.Context, id domain.SessionID) (deleted, killed bool, err error)
	StageAttachments(ctx context.Context, id domain.SessionID, attachments []ports.SpawnAttachment) ([]string, error)
	PrepareTaskWorkspace(context.Context, domain.ProjectRecord) (domain.TaskPreparationToken, error)
	CancelTaskPreparation(context.Context, domain.TaskPreparationToken) error
}

// interfaceTransitionCommander is an optional command capability. Keeping it
// separate avoids widening every focused session-service fake while production
// can expose the feature through the concrete Session Manager.
type interfaceTransitionCommander interface {
	InterfaceTransitionStatus(context.Context, domain.SessionID) (sessionmanager.InterfaceTransitionStatus, error)
	StartInterfaceTransition(context.Context, domain.SessionID, domain.SessionMode, domain.SessionInterfaceTransitionPolicy, domain.SessionInterfaceTransitionHistoryPolicy) (domain.SessionInterfaceTransition, error)
	CancelInterfaceTransition(context.Context, domain.SessionID) error
	AcknowledgeInterfaceTransitionNotice(context.Context, domain.SessionID, string) (domain.SessionInterfaceTransition, error)
}

// exitAgentCommander keeps the process-only lifecycle optional for focused
// service fakes while production delegates to Session Manager.
type exitAgentCommander interface {
	ExitAgent(context.Context, domain.SessionID) (domain.SessionRecord, error)
}

// agentAccountCommander keeps account moves optional for focused service fakes.
type agentAccountCommander interface {
	StartAccountTransition(context.Context, domain.SessionID, string, domain.SessionInterfaceTransitionPolicy) (domain.SessionInterfaceTransition, error)
}

// RollbackOutcome reports what happened in a rollback: either the seed row was
// deleted, or the partially-spawned session was killed (runtime+workspace torn
// down, row marked terminated).
type RollbackOutcome struct {
	Deleted bool `json:"deleted"`
	Killed  bool `json:"killed"`
}

// CleanupOutcome reports what session cleanup reclaimed and what it preserved.
type CleanupOutcome struct {
	Cleaned     []domain.SessionID `json:"cleaned"`
	AlreadyGone []domain.SessionID `json:"alreadyGone"`
	Skipped     []CleanupSkipped   `json:"skipped"`
}

// CleanupSkipped is one terminal session whose workspace was preserved by
// cleanup (never force-deleted), with the user-facing reason.
type CleanupSkipped struct {
	SessionID domain.SessionID `json:"sessionId"`
	Reason    string           `json:"reason"`
}

// RestoreModeView is the API-facing restore-mode enum, defined in domain so
// the shared wire types can use it.
type RestoreModeView = domain.RestoreModeView

// Restore modes, re-exported from domain.
const (
	RestoreModeViewNative      = domain.RestoreModeViewNative
	RestoreModeViewSavedPrompt = domain.RestoreModeViewSavedPrompt
	RestoreModeViewFresh       = domain.RestoreModeViewFresh
)

// RestoreOutcome reports the restored read model and how Tallpine relaunched it.
type RestoreOutcome struct {
	Session domain.Session  `json:"session"`
	Mode    RestoreModeView `json:"restoreMode"`
}

// ResumeAgentOutcome reports the resumed read model and how Tallpine relaunched it.
type ResumeAgentOutcome struct {
	Session domain.Session  `json:"session"`
	Mode    RestoreModeView `json:"resumeMode"`
}

// ExitAgentOutcome reports the still-live Tallpine session after only its agent
// controller has exited.
type ExitAgentOutcome struct {
	Session domain.Session `json:"session"`
}

// InterfaceTransitionStatus describes whether this session can cross between
// its TUI and Chat controllers and includes the latest durable handoff attempt.
type InterfaceTransitionStatus struct {
	Supported  bool
	TargetMode domain.SessionMode
	ReasonCode string
	Reason     string
	Transition *domain.SessionInterfaceTransition
}

type scmProvider interface {
	ParseRepository(remote string) (ports.SCMRepo, bool)
	FetchPullRequests(ctx context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error)
	FetchReviewThreads(ctx context.Context, ref ports.SCMPRRef) (ports.SCMReviewObservation, error)
}

// outputTypeReconciler recomputes and persists a session's durable OutputType
// column. Production wiring supplies *lifecycle.Manager; declaring the
// capability locally (rather than importing lifecycle) keeps this package
// from depending on the reducer that already depends on it transitively.
type outputTypeReconciler interface {
	ReconcileSessionOutputType(ctx context.Context, id domain.SessionID) error
}

// Service is the controller-facing session service. It delegates command-side
// session operations to the internal sessionmanager.Manager and owns read-model
// assembly, including user-facing display status derivation.
type Service struct {
	manager             commander
	store               Store
	prClaimer           ports.PRClaimer
	scm                 scmProvider
	tracker             ports.Tracker
	clock               func() time.Time
	dataDir             string
	logger              *slog.Logger
	backgroundContext   context.Context
	agentReadiness      ports.AgentReadinessProvider
	runBackground       func(func())
	orchestratorLocksMu sync.Mutex
	orchestratorLocks   map[domain.ProjectID]*sync.Mutex
	// handoffMu serializes orchestrator handoffs; handoffSuccessors remembers
	// which session replaced a handed-off one so a repeated call answers with
	// the same new id.
	handoffMu          sync.Mutex
	handoffSuccessors  map[domain.SessionID]domain.SessionID
	workspaceCache     *workspaceCache
	workspaceManifests *workspaceManifestIndex
	// commitSelections caches resolved commit selections. Their keys are
	// immutable SHAs, so entries never go stale.
	commitSelections *commitSelectionCache
	workspaceEditsMu sync.Mutex
	// workspaceGroup coalesces concurrent cache-miss compare/status lookups
	// for the same (session, root): "Expand All" on many files fires that
	// many GetWorkspaceFile calls at once, and without this each one would
	// independently spawn its own git subprocesses for identical work.
	workspaceGroup singleflight.Group
	manifestGroup  singleflight.Group
	// branchStateMu serializes branch-state reconciles and guards branchTips,
	// each session's last reconciled branch, remote, and base tips.
	branchStateMu sync.Mutex
	branchTips    map[domain.SessionID]string
	// signalCapable reports whether a harness has a hook pipeline that can
	// deliver activity signals at all. Only capable harnesses are eligible for
	// the no_signal downgrade: a hook-less harness staying silent forever is
	// normal, not a broken pipeline. nil means "unknown": never downgrade.
	signalCapable          func(domain.AgentHarness) bool
	chatProviderPreserved  func(domain.SessionID) bool
	titleRefinementSlots   chan struct{}
	titleRefinementMu      sync.Mutex
	titleRefinementCancels map[domain.SessionID]context.CancelFunc
	// outputTypeReconciler lets ClaimPR persist the pr OutputType immediately
	// instead of waiting for the next artifact-output poll tick, so raising a
	// PR on a session that already produced artifacts flips its Kanban
	// placement with the same latency claiming a PR always had. Nil in
	// service tests that construct Service directly; ClaimPR degrades to
	// relying on the poller in that case.
	outputTypeReconciler outputTypeReconciler
	// artifactDeleter deletes a session's artifact files and version rows. Nil
	// makes DeleteArtifacts fail rather than delete outside the capture lock.
	artifactDeleter artifactDeleter
	// runsOnComputerLogin reports whether a new session would launch on the
	// computer's own CLI login rather than a saved account. Nil means always.
	runsOnComputerLogin func(context.Context, domain.AgentHarness, string) bool
	// laneEnded runs after EndLane removed a lane, so the owner asks of that
	// lane's chat are withdrawn. Nil means nothing to tell.
	laneEnded func(ctx context.Context, project domain.ProjectID, lane string)
}

// SetLaneEndedHook late-binds what runs after a lane has been ended (B698:
// the lane's open owner asks are withdrawn).
func (s *Service) SetLaneEndedHook(hook func(ctx context.Context, project domain.ProjectID, lane string)) {
	s.laneEnded = hook
}

// SetComputerLoginCheck late-binds the account check the launch gate needs: the
// computer's login state matters only to a session that will run on it.
func (s *Service) SetComputerLoginCheck(check func(ctx context.Context, harness domain.AgentHarness, accountID string) bool) {
	s.runsOnComputerLogin = check
}

// SetChatProviderPreserver wires the live Chat lifetime observation after both
// services have been constructed. It performs no provider or filesystem probes.
func (s *Service) SetChatProviderPreserver(preserves func(domain.SessionID) bool) {
	s.chatProviderPreserved = preserves
}

// New wires a controller-facing session service over an internal session Manager.
func New(manager *sessionmanager.Manager, store Store) *Service {
	return NewWithDeps(Deps{Manager: manager, Store: store})
}

// Deps are optional collaborators for the session service. The default New
// path keeps existing tests and callers small; daemon wiring uses NewWithDeps
// to supply SCM observation for PR claiming.
type Deps struct {
	Manager   commander
	Store     Store
	PRClaimer ports.PRClaimer
	SCM       scmProvider
	Tracker   ports.Tracker
	Clock     func() time.Time
	DataDir   string
	Logger    *slog.Logger
	// AgentReadiness coordinates advisory native harness checks before launch.
	AgentReadiness ports.AgentReadinessProvider
	// BackgroundContext owns best-effort work that must survive an HTTP request
	// returning but stop with the daemon. It defaults to context.Background for
	// focused service tests and non-daemon callers.
	BackgroundContext context.Context
	// SignalCapable gates the no_signal status downgrade per harness; daemon
	// wiring passes activitydispatch.SupportsHarness. Left nil, no session is
	// ever downgraded to no_signal.
	SignalCapable func(domain.AgentHarness) bool
	// OutputTypeReconciler persists the durable OutputType column; daemon
	// wiring passes the shared *lifecycle.Manager. Left nil, ClaimPR still
	// succeeds but the pr OutputType only lands on the next artifact-output
	// poll tick.
	OutputTypeReconciler outputTypeReconciler
	// ArtifactDeleter deletes a session's artifacts; daemon wiring passes the
	// shared *artifactversions.Capturer so a delete cannot race a capture.
	ArtifactDeleter artifactDeleter
}

// NewWithDeps wires a session service with optional PR-claim dependencies.
func NewWithDeps(d Deps) *Service {
	backgroundContext := d.BackgroundContext
	if backgroundContext == nil {
		backgroundContext = context.Background()
	}
	s := &Service{manager: d.Manager, store: d.Store, prClaimer: d.PRClaimer, scm: d.SCM, tracker: d.Tracker, clock: d.Clock, dataDir: d.DataDir, signalCapable: d.SignalCapable, logger: d.Logger, backgroundContext: backgroundContext, agentReadiness: d.AgentReadiness, titleRefinementSlots: make(chan struct{}, delegatedTaskTitleConcurrency), titleRefinementCancels: map[domain.SessionID]context.CancelFunc{}, outputTypeReconciler: d.OutputTypeReconciler, artifactDeleter: d.ArtifactDeleter}
	if s.prClaimer == nil {
		if w, ok := d.Store.(ports.PRClaimer); ok {
			s.prClaimer = w
		}
	}
	if s.clock == nil {
		s.clock = time.Now
	}
	s.workspaceCache = newWorkspaceCache(workspaceCacheTTL, s.clock)
	s.workspaceManifests = newWorkspaceManifestIndex()
	s.workspaceManifests.now = s.clock
	s.commitSelections = newCommitSelectionCache(commitSelectionCacheSize)
	return s
}

// now tolerates a zero-value Service (tests construct the struct literally
// without going through New, which is where clock gets its default).
func (s *Service) now() time.Time {
	if s.clock == nil {
		return time.Now().UTC()
	}
	return s.clock().UTC()
}

// harnessSignals tolerates a zero-value Service the same way now does. Without
// an injected capability predicate the service cannot tell a broken pipeline
// from a hook-less harness, so it never claims no_signal.
func (s *Service) harnessSignals(h domain.AgentHarness) bool {
	if s.signalCapable == nil {
		return false
	}
	return s.signalCapable(h)
}
