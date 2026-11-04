package scm

import (
	"context"
	"log/slog"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

const (
	// DefaultTickInterval is the SCM observer's PR/CI polling cadence.
	DefaultTickInterval = 30 * time.Second
	// DefaultReviewInterval is the minimum interval between review-thread polls
	// for a PR whose review state warrants thread refresh.
	DefaultReviewInterval = 2 * time.Minute
	// DefaultCacheMax bounds each in-memory ETag/review cache map.
	DefaultCacheMax = 512
	// DefaultPRMaxAge bounds how long a locally-open tracked PR may go without a
	// forced re-fetch. It is a bounded-staleness backstop, not a distrust of the
	// ETag: the refresh signals the observer consults (the repo PR-list guard and
	// the per-commit checks guard) do not track a PR's own state, so a merged or
	// closed PR whose head SHA is unchanged can otherwise read stale. This caps
	// how long that inference can persist.
	DefaultPRMaxAge = 5 * time.Minute
	// BatchSize is the maximum number of PRs in one provider batch fetch.
	BatchSize = 25
)

// identityKey builds the cache key for a per-provider, per-host identity.
// GitHub repos carry Host="github.com" but GitHub identity is not
// host-scoped, so all GitHub hosts collapse to the same key. GitLab repos
// on self-managed hosts get a distinct key from gitlab.com.
func identityKey(provider, host string) string {
	if provider == "github" {
		return provider // GitHub identity is not host-scoped
	}
	return provider + "|" + host
}

// Provider is the normalized SCM provider contract used by the observer.
type Provider interface {
	ParseRepository(remote string) (ports.SCMRepo, bool)
	RepoPRListGuard(ctx context.Context, repo ports.SCMRepo, etag string) (ports.SCMGuardResult, error)
	ListPRsByRepo(ctx context.Context, repo ports.SCMRepo, updatedAfter time.Time) ([]ports.SCMPRObservation, error)
	CommitChecksGuard(ctx context.Context, repo ports.SCMRepo, headSHA, etag string) (ports.SCMGuardResult, error)
	// FetchPullRequests returns observations positionally aligned with refs:
	// result[i] answers refs[i], with a Fetched=false placeholder (optionally
	// carrying a per-observation Error) when refs[i] could not be fetched.
	// The observer relies on this alignment to attribute each observation to
	// the subject it was requested for — repo renames make any content-based
	// re-derivation (repo name, URL) ambiguous.
	FetchPullRequests(ctx context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error)
	FetchFailedCheckLogTail(ctx context.Context, repo ports.SCMRepo, check ports.SCMCheckObservation) (string, error)
	FetchReviewThreads(ctx context.Context, ref ports.SCMPRRef) (ports.SCMReviewObservation, error)
}

// Store is the persistence contract the observer needs for discovery, local
// hash reads, and transactional SCM writes.
type Store interface {
	ListLiveSessions(ctx context.Context) ([]domain.SessionRecord, error)
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
	UpsertProject(ctx context.Context, row domain.ProjectRecord) error
	ListWorkspaceRepos(ctx context.Context, projectID string) ([]domain.WorkspaceRepoRecord, error)
	ListPRsBySession(ctx context.Context, sessionID domain.SessionID) ([]domain.PullRequest, error)
	ListChecks(ctx context.Context, prURL string) ([]domain.PullRequestCheck, error)
	WriteSCMObservation(ctx context.Context, pr domain.PullRequest, checks []domain.PullRequestCheck, reviews []domain.PullRequestReview, threads []domain.PullRequestReviewThread, comments []domain.PullRequestComment, reviewMode ports.ReviewWriteMode) error
}

// Lifecycle is the provider-neutral lifecycle notification sink.
type Lifecycle interface {
	ApplySCMObservation(ctx context.Context, sessionID domain.SessionID, obs ports.SCMObservation) error
}

// endedPRSessionLister is an optional Store capability: sessions that ended
// while a PR they opened is still open (backlog-linked or not, B397).
// Their PRs stay polled, so a merge that lands after the worker was killed is
// still recorded and the PR never lingers as stale 'open' (nothing else
// observes a dead session).
type endedPRSessionLister interface {
	ListEndedSessionsWithOpenBacklogPRs(ctx context.Context) ([]domain.SessionRecord, error)
}

type credentialChecker interface {
	SCMCredentialsAvailable(ctx context.Context) (bool, error)
}

// Config holds optional observer knobs. Zero values use production defaults.
type Config struct {
	// Tick is the fast PR/CI polling interval. Zero uses DefaultTickInterval.
	Tick time.Duration
	// ReviewInterval is the slower review-thread refresh interval.
	ReviewInterval time.Duration
	// Clock supplies timestamps for observations and tests. Nil uses time.Now.
	Clock func() time.Time
	// Logger receives operational diagnostics for provider/store/lifecycle failures.
	Logger *slog.Logger
	// CacheMax bounds each in-memory ETag/review cache. Zero uses DefaultCacheMax.
	CacheMax int
	// ScopedIdentityResolver resolves the active human account per provider and
	// host. Without a matching identity, new automatic attachments are disabled;
	// existing attachments continue to refresh and explicit claims still work.
	ScopedIdentityResolver ports.ScopedIdentityResolver
	// BranchStates refreshes sessions' local commit and push facts on this
	// tick. Nil skips it.
	BranchStates BranchStateReconciler
	// ReviewRuns reads Tallpine's recorded review passes for the ProjectConfig.NoCI
	// gate: a no-CI project's PR counts as CI-passing only once Tallpine approved its
	// current head. Nil means no head is ever Tallpine-approved.
	ReviewRuns ReviewRunReader
}

// ReviewRunReader lists the Tallpine review passes recorded for a worker session.
type ReviewRunReader interface {
	ListReviewRunsBySession(ctx context.Context, id domain.SessionID) ([]domain.ReviewRun, error)
}

// BranchStateReconciler reads every live session's branch facts from git.
type BranchStateReconciler interface {
	ReconcileBranchStates(ctx context.Context) error
}

type subject struct {
	session domain.SessionRecord
	repo    ports.SCMRepo
	branch  string
	known   domain.PullRequest
	hasPR   bool
}

// Head names are scoped to repo's provider and host. Each base has a separate
// set because overlapping workspace checkouts can have different push targets.
type sessionRepo struct {
	session   domain.SessionRecord
	repo      ports.SCMRepo
	headNames map[string]struct{}
	branch    string
	workspace bool
	// trackOnly marks an ended session kept to refresh its open PRs; it never
	// claims newly discovered PRs.
	trackOnly bool
}

type repoGuardState struct {
	result  ports.SCMGuardResult
	hadETag bool
	err     error
}

type pendingCacheString struct {
	key   string
	value string
}

type refreshSelection struct {
	// refs and refKeys are parallel: refKeys[i] is the subject key
	// refs[i] was issued for.
	refs          []ports.SCMPRRef
	refKeys       []string
	subjectsByPR  map[string]*subject
	commitETags   map[string]pendingCacheString
	candidateKeys map[string]bool
	// noCIRechecks are noCI staleness tokens for PRs promoted only by
	// noCIStateStale; they are recorded in Observer.noCIRecheck once the
	// refetch succeeds, so a failed or rate-limited refetch is retried.
	noCIRechecks map[string]string
}

type persistenceOptions struct {
	reviewFetched               bool
	preserveLocalMetadataHash   bool
	preserveLocalCIHash         bool
	preserveLocalReviewHash     bool
	preserveLocalReviewDecision bool
}
