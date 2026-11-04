package domain

import (
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/pkg/contract"
)

// ---- PR read model ----

// PRFacts is the per-session PR snapshot the status derivation reads from the
// pr table.
type PRFacts struct {
	URL            string
	Number         int
	Draft          bool
	Merged         bool
	Closed         bool
	CI             CIState
	Review         ReviewDecision
	Mergeability   Mergeability
	ReviewComments bool // has unresolved review comments (any author) to address
	SourceBranch   string
	TargetBranch   string
	HeadSHA        string
	UpdatedAt      time.Time
	// StateChangedAt, CIChangedAt and LastReviewAt are when the lifecycle
	// state, the CI result and the newest review last changed. Unlike
	// UpdatedAt, none of them advance on an unchanged poll; zero is unknown.
	StateChangedAt time.Time
	CIChangedAt    time.Time
	LastReviewAt   time.Time
	// ExternalApproved and ExternalChangesRequested are the human review
	// verdicts Tallpine did not author. Review above aggregates Tallpine's own provider
	// reviews with everyone else's, so it cannot say whose turn the
	// review-feedback loop is on.
	ExternalApproved         bool
	ExternalChangesRequested bool
	ExternalComments         bool
}

// PullRequest is the app-level representation of one tracked pull request as
// persisted by the PR store. It is intentionally separate from the sqlc
// generated sqlite row type so storage details do not leak outside sqlite.
type PullRequest struct {
	URL string
	// URLAlias is a transient provider-resolved URL that should point at URL.
	// It is persisted in the alias table, not in the pr row itself.
	URLAlias     string
	SessionID    SessionID
	Number       int
	Draft        bool
	Merged       bool
	Closed       bool
	CI           CIState
	Review       ReviewDecision
	Mergeability Mergeability
	UpdatedAt    time.Time
	// StateChangedAt is when the current normalized PR lifecycle state became
	// active. It is seeded from provider timestamps and updated when Tallpine observes
	// a draft/open/merged/closed transition.
	StateChangedAt time.Time

	Provider string
	Host     string
	Repo     string
	// ProviderID is immutable within a provider and host and survives repository
	// renames or transfers.
	ProviderID string

	SourceBranch string
	TargetBranch string
	HeadSHA      string
	Title        string
	Additions    int
	Deletions    int
	ChangedFiles int
	// Paths are the changed file paths as last observed (the provider caps the
	// list). Nil means not observed: stored paths are kept, not cleared.
	Paths           []string
	Author          string
	AuthorAvatarURL string
	BaseSHA         string
	MergeCommitSHA  string

	ProviderState            string
	ProviderMergeable        string
	ProviderMergeStateStatus string
	HTMLURL                  string

	CreatedAtProvider time.Time
	UpdatedAtProvider time.Time
	MergedAtProvider  time.Time
	ClosedAtProvider  time.Time

	MetadataHash string
	CIHash       string
	ReviewHash   string

	ObservedAt       time.Time
	CIObservedAt     time.Time
	ReviewObservedAt time.Time
	// ReviewPartial records that the latest review-thread observation hit the
	// provider's thread-window cap, so stored thread rows are a partial view.
	ReviewPartial bool
	AutoInjectCI  bool
}

// PullRequestCheck is one normalized CI check run for a pull request.
type PullRequestCheck struct {
	Name       string
	CommitHash string
	Status     PRCheckStatus
	Conclusion string
	URL        string
	Details    string
	LogTail    string
	CreatedAt  time.Time
}

// PullRequestComment is one normalized review comment for a pull request.
type PullRequestComment struct {
	ThreadID         string
	ReviewID         string
	ID               string
	Author           string
	File             string
	Line             int
	Body             string
	URL              string
	Resolved         bool
	IsBot            bool
	CreatedAt        time.Time
	AutoInjectReview bool
}

// IsActionableReviewComment reports whether a review comment should block
// ready-to-merge state and be surfaced to the agent. Human comments are always
// actionable; bot comments need a concrete file and line anchor so status
// chatter does not become a merge blocker.
func IsActionableReviewComment(resolved, isBot bool, file string, line int) bool {
	if resolved {
		return false
	}
	return !isBot || (strings.TrimSpace(file) != "" && line > 0)
}

// PullRequestReviewThread is one normalized review thread for a pull request.
type PullRequestReviewThread struct {
	ThreadID     string
	Path         string
	Line         int
	Resolved     bool
	IsBot        bool
	SemanticHash string
	UpdatedAt    time.Time
}

// PullRequestReview is one submitted provider review for a pull request.
type PullRequestReview struct {
	ID               string
	Author           string
	State            ReviewDecision
	URL              string
	Body             string
	IsBot            bool
	TargetSHA        string
	SubmittedAt      time.Time
	AutoInjectReview bool
}

// CIState is the aggregate CI status of a PR.
type CIState = contract.CIState

// CI states.
const (
	CIUnknown = contract.CIUnknown
	CIPending = contract.CIPending
	CIPassing = contract.CIPassing
	CIFailing = contract.CIFailing
)

// ReviewDecision is the aggregate human-review verdict on a PR.
type ReviewDecision = contract.ReviewDecision

// Review decisions.
const (
	ReviewNone           = contract.ReviewNone
	ReviewApproved       = contract.ReviewApproved
	ReviewChangesRequest = contract.ReviewChangesRequest
	ReviewRequired       = contract.ReviewRequired
)

// Mergeability is whether a PR can currently be merged.
type Mergeability = contract.Mergeability

// Mergeability states.
const (
	MergeUnknown     = contract.MergeUnknown
	MergeMergeable   = contract.MergeMergeable
	MergeConflicting = contract.MergeConflicting
	MergeBlocked     = contract.MergeBlocked
	MergeUnstable    = contract.MergeUnstable
)

// PRState is the normalized lifecycle of one tracked pull request as stored in
// the pr table.
type PRState = contract.PRState

// PR states.
const (
	PRStateDraft  = contract.PRStateDraft
	PRStateOpen   = contract.PRStateOpen
	PRStateMerged = contract.PRStateMerged
	PRStateClosed = contract.PRStateClosed
)

// PRCheckStatus is one CI check run's normalized status.
type PRCheckStatus = contract.PRCheckStatus

// PR check statuses.
const (
	PRCheckUnknown    = contract.PRCheckUnknown
	PRCheckQueued     = contract.PRCheckQueued
	PRCheckInProgress = contract.PRCheckInProgress
	PRCheckPassed     = contract.PRCheckPassed
	PRCheckFailed     = contract.PRCheckFailed
	PRCheckSkipped    = contract.PRCheckSkipped
	PRCheckCancelled  = contract.PRCheckCancelled
)

// MergeReadiness is the set of durable PR facts that decide whether a PR is
// waiting on nothing but the user's merge click. It is deliberately the stored
// shape rather than a provider observation, so the live SCM path and the
// startup reconciliation path can share one rule instead of writing it twice
// and drifting apart.
type MergeReadiness struct {
	Draft              bool
	Merged             bool
	Closed             bool
	CI                 CIState
	Review             ReviewDecision
	Mergeability       Mergeability
	UnresolvedComments bool
}

// MergeBlocker names the first merge precondition a PR fails. The values are
// stable API codes: they reach `tallpine pr merge` and the PR_PRECONDITIONS_UNMET
// error details.
type MergeBlocker string

// MergeBlocker values; MergeBlockerNone means no precondition fails.
const (
	MergeBlockerNone               MergeBlocker = ""
	MergeBlockerMerged             MergeBlocker = "merged"
	MergeBlockerClosed             MergeBlocker = "closed"
	MergeBlockerDraft              MergeBlocker = "draft"
	MergeBlockerCIUnknown          MergeBlocker = "ci_unknown"
	MergeBlockerCIPending          MergeBlocker = "ci_pending"
	MergeBlockerCIFailing          MergeBlocker = "ci_failing"
	MergeBlockerChangesRequested   MergeBlocker = "changes_requested"
	MergeBlockerUnresolvedComments MergeBlocker = "unresolved_comments"
	MergeBlockerNotMergeable       MergeBlocker = "not_mergeable"
	// MergeBlockerHeadUnknown and the blockers below it are judged by the
	// merge action against a fresh provider read rather than by MergeReadiness.
	MergeBlockerHeadUnknown       MergeBlocker = "head_unknown"
	MergeBlockerCIHeadMismatch    MergeBlocker = "ci_head_mismatch"
	MergeBlockerReviewDataPartial MergeBlocker = "review_data_partial"
	// MergeBlockerReviewApprovalRequired and the review-gate blockers below it
	// apply only to a ProjectConfig.NoCI project whose PR head has no provider
	// checks: an approved Tallpine review of that exact head stands in for CI.
	MergeBlockerReviewApprovalRequired         MergeBlocker = "review_approval_required"
	MergeBlockerTallpineReviewRunning          MergeBlocker = "ao_review_running"
	MergeBlockerTallpineReviewChangesRequested MergeBlocker = "ao_changes_requested"
)

// Describe is a one-line human explanation of the blocker.
func (b MergeBlocker) Describe() string {
	switch b {
	case MergeBlockerMerged:
		return "pull request is already merged"
	case MergeBlockerClosed:
		return "pull request is closed"
	case MergeBlockerDraft:
		return "pull request is a draft"
	case MergeBlockerCIUnknown:
		return "CI status is unknown (the provider reported no checks for the head; set the project's noCI option if this project has no CI)"
	case MergeBlockerCIPending:
		return "CI checks are still running"
	case MergeBlockerCIFailing:
		return "CI checks are failing"
	case MergeBlockerChangesRequested:
		return "a reviewer requested changes"
	case MergeBlockerUnresolvedComments:
		return "there are unresolved review comments"
	case MergeBlockerNotMergeable:
		return "the provider does not report the pull request as mergeable"
	case MergeBlockerHeadUnknown:
		return "pull request head is unknown"
	case MergeBlockerCIHeadMismatch:
		return "CI results are for a different commit than the pull request head"
	case MergeBlockerReviewApprovalRequired:
		return "the project has no CI (noCI), so an approved " + brand.ProductName + " review of the current head is required; run `tallpine review trigger`"
	case MergeBlockerTallpineReviewRunning:
		return "the project has no CI (noCI) and the " + brand.ProductName + " review of the current head is still running"
	case MergeBlockerTallpineReviewChangesRequested:
		return "the project has no CI (noCI) and the " + brand.ProductName + " review of the current head requested changes"
	case MergeBlockerReviewDataPartial:
		return "review threads could not be read completely"
	default:
		return string(b)
	}
}

// ReadyToMerge reports whether the PR has no known blocker left. An unknown or
// still-running CI result is treated as a blocker: Tallpine only claims readiness it
// can actually prove.
func (r MergeReadiness) ReadyToMerge() bool {
	return r.Blocker() == MergeBlockerNone
}

// Blocker returns the first precondition that keeps the PR from being ready to
// merge, or MergeBlockerNone.
func (r MergeReadiness) Blocker() MergeBlocker {
	switch {
	case r.Merged:
		return MergeBlockerMerged
	case r.Closed:
		return MergeBlockerClosed
	case r.Draft:
		return MergeBlockerDraft
	}
	switch r.CI {
	case CIFailing:
		return MergeBlockerCIFailing
	case CIPending:
		return MergeBlockerCIPending
	case CIPassing:
	default:
		return MergeBlockerCIUnknown
	}
	if r.Review == ReviewChangesRequest {
		return MergeBlockerChangesRequested
	}
	if r.UnresolvedComments {
		return MergeBlockerUnresolvedComments
	}
	if r.Mergeability != MergeMergeable {
		return MergeBlockerNotMergeable
	}
	return MergeBlockerNone
}

// CIHash markers the SCM observer appends when the ProjectConfig.NoCI review
// gate decided a PR's stored CI state. They contain no ':' so the failure
// fingerprint prefix of a CIHash is never confused with them.
const (
	// NoCIWaivedHashSuffix: CI is "passing" only because Tallpine approved a head
	// with no provider checks.
	NoCIWaivedHashSuffix = "+noci"
	// NoCIAwaitingApprovalHashSuffix: the head has no provider checks and no
	// Tallpine approval yet, so CI stays "unknown" and the PR is held back as
	// blocked even if the provider calls it mergeable.
	NoCIAwaitingApprovalHashSuffix = "+nociwait"
)

// NoCIWaived reports whether the stored CI "passing" came from the noCI
// review-gate waiver rather than provider checks.
func (pr PullRequest) NoCIWaived() bool {
	return strings.HasSuffix(pr.CIHash, NoCIWaivedHashSuffix)
}

// NoCIAwaitingApproval reports whether the noCI review gate holds the stored
// PR back until a Tallpine review approves its current head.
func (pr PullRequest) NoCIAwaitingApproval() bool {
	return strings.HasSuffix(pr.CIHash, NoCIAwaitingApprovalHashSuffix)
}

// MergeReasonTallpineReviewRequired is the mergeability reason for a PR the noCI
// review gate holds back.
const MergeReasonTallpineReviewRequired = "ao_review_required"

// NoCIGateApplies reports whether a ProjectConfig.NoCI project's merge gate
// decides this CI result: the provider authoritatively reported no checks at
// all for the head (noChecks) and therefore could only say "unknown".
func NoCIGateApplies(ci CIState, noChecks bool) bool {
	return noChecks && (ci == "" || ci == CIUnknown)
}

// CIWithNoCIPolicy resolves the CI state that merge readiness should judge.
// A project that opted into ProjectConfig.NoCI has no CI, so Tallpine review is its
// merge gate: when NoCIGateApplies, CI counts as passing only once a Tallpine
// review approved the PR's current head (tallpineReview, see TallpineReviewGateForHead);
// an unreviewed PR stays at "unknown" and is therefore never ready to merge.
// The provider's aggregate review decision is deliberately not consulted: it
// is not tied to a commit (without "dismiss stale approvals" an approval of an
// older head still reads APPROVED after a new push).
// Anything the provider did report — pending, failing, passing — is returned
// unchanged, and without the opt-in zero checks stay unknown, because a fresh
// push in a CI repository can briefly have no registered checks.
func CIWithNoCIPolicy(ci CIState, noChecks, projectNoCI bool, tallpineReview TallpineReviewGate) CIState {
	if projectNoCI && NoCIGateApplies(ci, noChecks) && tallpineReview == TallpineReviewGateApproved {
		return CIPassing
	}
	return ci
}

// TallpineReviewGate is Tallpine's own review verdict on one PR head, judged from the
// review runs Tallpine recorded. It is not GitHub's reviewDecision: Tallpine reviewers
// post COMMENT reviews, so only Tallpine's run records carry the verdict.
type TallpineReviewGate string

// TallpineReviewGate values.
const (
	TallpineReviewGateMissing          TallpineReviewGate = "missing"
	TallpineReviewGateRunning          TallpineReviewGate = "running"
	TallpineReviewGateChangesRequested TallpineReviewGate = "changes_requested"
	TallpineReviewGateApproved         TallpineReviewGate = "approved"
)

// Blocker maps a non-approved gate to its merge blocker.
func (g TallpineReviewGate) Blocker() MergeBlocker {
	switch g {
	case TallpineReviewGateApproved:
		return MergeBlockerNone
	case TallpineReviewGateRunning:
		return MergeBlockerTallpineReviewRunning
	case TallpineReviewGateChangesRequested:
		return MergeBlockerTallpineReviewChangesRequested
	default:
		return MergeBlockerReviewApprovalRequired
	}
}

// TallpineReviewGateForHead judges Tallpine's review passes of prURL against exactly
// head. Runs for any other commit never count, so approving an older head
// does not carry over to new pushes. Only each reviewer harness's latest pass
// on the head is considered; a pass that requested changes, or one still
// running, outranks an approval from another harness. Failed and cancelled
// passes carry no verdict and are ignored.
func TallpineReviewGateForHead(runs []ReviewRun, prURL, head string) TallpineReviewGate {
	prURL = strings.TrimSpace(prURL)
	head = strings.TrimSpace(head)
	if prURL == "" || head == "" {
		return TallpineReviewGateMissing
	}
	latest := map[ReviewerHarness]ReviewRun{}
	for _, run := range runs {
		if !strings.EqualFold(strings.TrimSpace(run.PRURL), prURL) || !strings.EqualFold(strings.TrimSpace(run.TargetSHA), head) {
			continue
		}
		prev, ok := latest[run.Harness]
		if !ok || run.CreatedAt.After(prev.CreatedAt) || (run.CreatedAt.Equal(prev.CreatedAt) && run.ID > prev.ID) {
			latest[run.Harness] = run
		}
	}
	gate := TallpineReviewGateMissing
	for _, run := range latest {
		switch {
		case run.Verdict == VerdictChangesRequested:
			return TallpineReviewGateChangesRequested
		case run.Status == ReviewRunRunning:
			gate = TallpineReviewGateRunning
		case run.Verdict == VerdictApproved && gate == TallpineReviewGateMissing:
			gate = TallpineReviewGateApproved
		}
	}
	return gate
}

// MergeReadinessOf projects stored PR facts into the shared readiness rule.
// hasUnresolvedComments comes from the pr_comment rows Tallpine keeps for the PR,
// filtered to actionable unresolved comments.
func MergeReadinessOf(pr PullRequest, hasUnresolvedComments bool) MergeReadiness {
	return MergeReadiness{
		Draft:              pr.Draft,
		Merged:             pr.Merged,
		Closed:             pr.Closed,
		CI:                 pr.CI,
		Review:             pr.Review,
		Mergeability:       pr.Mergeability,
		UnresolvedComments: hasUnresolvedComments,
	}
}

// State collapses the PR's bools into one lifecycle state.
func (p PullRequest) State() PRState { return PRLifecycleState(p.Merged, p.Closed, p.Draft) }

// State collapses the PR's bools into one lifecycle state.
func (f PRFacts) State() PRState { return PRLifecycleState(f.Merged, f.Closed, f.Draft) }
