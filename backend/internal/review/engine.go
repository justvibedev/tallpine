// Package review holds the core code-review logic: triggering a reviewer over a
// worker's worktree, recording review runs, and accepting submitted results.
//
// It is independent of any transport. The daemon's HTTP service
// (internal/service/review) is a thin boundary over this engine today, and the
// same engine can back an in-process CLI trigger later without going through the
// API. Transport-specific concerns (DTOs, error→status mapping) stay in the
// service/controller layers; the orchestration and run-id generation live here.
package review

import (
	stdctx "context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// ErrInvalid and ErrNotFound let the transport layer map failures to 422/404.
// ErrConflict marks a trigger the caller asked to reject rather than reuse; the
// two specific conflicts wrap it so the transport can name which one occurred.
var (
	ErrInvalid  = errors.New("review: invalid input")
	ErrNotFound = errors.New("review: not found")
	ErrConflict = errors.New("review: conflict")

	ErrReviewAlreadyRunning = fmt.Errorf("%w: review already running", ErrConflict)
	ErrHeadAlreadyReviewed  = fmt.Errorf("%w: head already reviewed", ErrConflict)
	// ErrPROwnedElsewhere means a named PR belongs to another active session,
	// which a review trigger never takes over.
	ErrPROwnedElsewhere = fmt.Errorf("%w: pull request owned by another session", ErrConflict)
	// ErrHarnessInstallActive means the reviewer's harness is being installed,
	// updated or removed, so a reviewer cannot start on it now.
	ErrHarnessInstallActive = fmt.Errorf("%w: reviewer harness is being updated", ErrConflict)
)

// HarnessUseGate keeps reviewer launches from racing a harness update. The
// release must be called once the reviewer's handle is recorded, so an update
// that starts afterwards sees the reviewer as live.
type HarnessUseGate interface {
	TryBeginHarnessUse(harness domain.AgentHarness) (release func(), ok bool)
}

// Store is the persistence surface the engine needs. *sqlite.Store satisfies it
// in production; tests use a fake.
type Store interface {
	UpsertReview(ctx stdctx.Context, r domain.Review) error
	ArchiveReviewsBySession(ctx stdctx.Context, workerID domain.SessionID, now time.Time) error
	SetReviewInterfaceMode(ctx stdctx.Context, id string, mode domain.ReviewerInterfaceMode, updatedAt time.Time) (bool, error)
	RestoreReviewLaunchState(ctx stdctx.Context, review domain.Review, now time.Time) (bool, error)
	SettleReviewChatWork(ctx stdctx.Context, reviewID string, now time.Time) error
	SetSessionReviewerConfig(ctx stdctx.Context, id domain.SessionID, harness domain.ReviewerHarness, config domain.AgentConfig, updatedAt time.Time) (bool, error)
	GetReviewBySession(ctx stdctx.Context, id domain.SessionID) (domain.Review, bool, error)
	ClearReviewerHandle(ctx stdctx.Context, id domain.SessionID) error
	GetReviewBySessionAndHarness(ctx stdctx.Context, id domain.SessionID, harness domain.ReviewerHarness) (domain.Review, bool, error)
	ListReviewsBySession(ctx stdctx.Context, id domain.SessionID) ([]domain.Review, error)
	ClearReviewerHandleByHarness(ctx stdctx.Context, id domain.SessionID, harness domain.ReviewerHarness) error
	// ReviewBatchDispatched reports whether a Chat reviewer's prompt for a batch
	// was accepted by the provider (its turn is bound to a provider turn id).
	ReviewBatchDispatched(ctx stdctx.Context, reviewID, batchID string) (bool, error)
	InsertReviewRun(ctx stdctx.Context, r domain.ReviewRun) error
	UpdateReviewRunResult(ctx stdctx.Context, id string, status domain.ReviewRunStatus, verdict domain.ReviewVerdict, body, githubReviewID string, autoInjectReview bool) (bool, error)
	UpdateReviewAgentSessionID(ctx stdctx.Context, id, agentSessionID string) (bool, error)
	SupersedeStaleRunningReviewRuns(ctx stdctx.Context, sessionID domain.SessionID, prURL, targetSHA, body string) (int64, error)
	CancelRunningReviewRunsBySession(ctx stdctx.Context, sessionID domain.SessionID, body string) (int64, error)
	CancelRunningReviewRunsBySessionAndHarness(ctx stdctx.Context, sessionID domain.SessionID, harness domain.ReviewerHarness, body string) (int64, error)
	GetReviewRun(ctx stdctx.Context, id string) (domain.ReviewRun, bool, error)
	GetReviewRunBySessionPRAndSHA(ctx stdctx.Context, id domain.SessionID, prURL, targetSHA string) (domain.ReviewRun, bool, error)
	GetReviewRunBySessionPRSHAAndHarness(ctx stdctx.Context, id domain.SessionID, prURL, targetSHA string, harness domain.ReviewerHarness) (domain.ReviewRun, bool, error)
	ListReviewRunsBySession(ctx stdctx.Context, id domain.SessionID) ([]domain.ReviewRun, error)
	ListRunningReviewRunsBySession(ctx stdctx.Context, id domain.SessionID) ([]domain.ReviewRun, error)
	ListRecoverableChatReviews(ctx stdctx.Context) ([]domain.Review, error)
	RecordReviewChatControllerError(ctx stdctx.Context, id, message string, now time.Time) (bool, error)
}

// Sessions resolves the worker session under review.
type Sessions interface {
	GetSession(ctx stdctx.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// PRs resolves the PR a worker owns.
type PRs interface {
	ListPRsBySession(ctx stdctx.Context, id domain.SessionID) ([]domain.PullRequest, error)
}

// Projects resolves the per-project reviewer config.
type Projects interface {
	GetProject(ctx stdctx.Context, id string) (domain.ProjectRecord, bool, error)
}

// Deps wires the engine.
type Deps struct {
	// HarnessUse, when set, is held across each reviewer launch.
	HarnessUse HarnessUseGate
	Store      Store
	Sessions   Sessions
	PRs        PRs
	Projects   Projects
	Launcher   Launcher
	// Plans, when set, supplies the approved plan of the backlog item a worker
	// builds so the reviewer can check the code against it (B205). Nil: none.
	Plans PlanSource
	// ChatRecoveryDone gates operations that could mistake a recovering reviewer for an exited one.
	ChatRecoveryDone <-chan struct{}
	// PassDeadline fails a running pass that has not submitted a result this
	// long after it started, whatever the reviewer's activity reads. Zero
	// disables it.
	PassDeadline time.Duration
	// IdleGrace is how long a live terminal reviewer may sit idle at its
	// prompt with a pass running before that pass fails. Zero means
	// reviewerIdleGrace; negative disables the idle expiry.
	IdleGrace time.Duration
	// RetireGrace is how long a terminal reviewer pane may outlive its last pass
	// before the sweep destroys it. Zero means reviewerRetireGrace; negative disables.
	RetireGrace time.Duration
	// LaunchGrace is how long a live Claude terminal reviewer may run a pass
	// without its first activity hook before the pass fails as never started.
	// Zero means reviewerLaunchGrace; negative disables the check.
	LaunchGrace time.Duration

	// ChangedPaths lists the files a PR head changes against its base, for the
	// risk-based reviewer model. Nil uses git in the worker workspace.
	ChangedPaths func(ctx stdctx.Context, workspace string, pr domain.PullRequest) ([]string, error)

	// Clock and NewID are injectable for deterministic tests.
	Clock func() time.Time
	NewID func() string
}

// Engine is the core code-review engine.
type Engine struct {
	harnessUse HarnessUseGate
	store      Store
	sessions   Sessions
	prs        PRs
	projects   Projects
	launcher   Launcher
	plans      PlanSource
	clock      func() time.Time
	newID      func() string

	changedPaths func(ctx stdctx.Context, workspace string, pr domain.PullRequest) ([]string, error)
	patchID      func(ctx stdctx.Context, workspace, base, sha string) (string, error)

	chatRecoveryDone <-chan struct{}
	passDeadline     time.Duration
	idleGrace        time.Duration
	retireGrace      time.Duration
	launchGrace      time.Duration

	// triggerMu guards triggerLocks; triggerLocks holds one mutex per worker
	// session so concurrent Trigger calls for the same worker serialise (see
	// lockWorker). Distinct workers never contend.
	launchedMu sync.Mutex
	// launchedAt is when a reviewer pane finished launching, by handle. The
	// launch grace counts from it, not from the pass's creation: a slow boot
	// under load must not eat the time the CLI needs to report its first hook.
	launchedAt map[string]time.Time

	triggerMu    sync.Mutex
	triggerLocks map[domain.SessionID]*sync.Mutex
}

const autoReviewFailedRetryLimit = 3

// New wires an Engine from its dependencies, defaulting the clock and id source.
func New(d Deps) *Engine {
	clock := d.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	newID := d.NewID
	if newID == nil {
		newID = uuid.NewString
	}
	idleGrace := d.IdleGrace
	if idleGrace == 0 {
		idleGrace = reviewerIdleGrace
	}
	retireGrace := d.RetireGrace
	if retireGrace == 0 {
		retireGrace = reviewerRetireGrace
	}
	launchGrace := d.LaunchGrace
	if launchGrace == 0 {
		launchGrace = reviewerLaunchGrace
	}
	changedPaths := d.ChangedPaths
	if changedPaths == nil {
		changedPaths = gitChangedPaths
	}
	return &Engine{
		changedPaths: changedPaths,
		patchID:      gitPatchID,
		store:        d.Store,
		harnessUse:   d.HarnessUse,
		sessions:     d.Sessions,
		prs:          d.PRs,
		projects:     d.Projects,
		launcher:     d.Launcher,
		plans:        d.Plans,
		clock:        clock,
		newID:        newID,
		triggerLocks: make(map[domain.SessionID]*sync.Mutex),
		launchedAt:   make(map[string]time.Time),

		chatRecoveryDone: d.ChatRecoveryDone,
		passDeadline:     d.PassDeadline,
		idleGrace:        idleGrace,
		retireGrace:      retireGrace,
		launchGrace:      launchGrace,
	}
}

// lockWorker serialises Trigger calls for a single worker session and returns
// the unlock func. Without it, two concurrent triggers for the same worker can
// both pass the per-commit idempotency check and each spawn a reviewer against
// the same deterministic handle, leaving two running runs for one commit (#242).
//
// The per-worker mutex is created on first use and kept for the lifetime of the
// engine; the entry is a single pointer, so the unbounded-by-session-count map
// is a negligible, bounded-in-practice cost.
func (e *Engine) lockWorker(id domain.SessionID) func() {
	e.triggerMu.Lock()
	mu, ok := e.triggerLocks[id]
	if !ok {
		mu = &sync.Mutex{}
		e.triggerLocks[id] = mu
	}
	e.triggerMu.Unlock()
	mu.Lock()
	return mu.Unlock
}

// tryLockWorker is lockWorker for background work that must not queue behind
// a trigger or restore in progress: ok is false when the worker is busy.
func (e *Engine) tryLockWorker(id domain.SessionID) (unlock func(), ok bool) {
	e.triggerMu.Lock()
	mu, found := e.triggerLocks[id]
	if !found {
		mu = &sync.Mutex{}
		e.triggerLocks[id] = mu
	}
	e.triggerMu.Unlock()
	if !mu.TryLock() {
		return nil, false
	}
	return mu.Unlock, true
}

// waitForChatRecovery keeps a missing startup controller from being treated as
// proof of exit. Recovery itself bypasses this gate; provider result callbacks
// must also remain available while native conversations reconnect.
func (e *Engine) waitForChatRecovery(ctx stdctx.Context) error {
	if e.chatRecoveryDone == nil {
		return nil
	}
	select {
	case <-e.chatRecoveryDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
