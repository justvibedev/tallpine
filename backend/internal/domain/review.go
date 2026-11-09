package domain

import (
	"errors"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/pkg/contract"
)

// ErrDuplicateReviewRun is returned by InsertReviewRun when a run already exists
// for the same worker session and target commit (the partial unique index from
// migration 0013). It lets the review engine fall back to the recorded run
// instead of surfacing a raw storage error after a reviewer may have launched.
var ErrDuplicateReviewRun = errors.New("domain: review run already exists for session and target sha")

// Review is the per-worker, per-reviewer-harness code-review record. A repeat
// trigger for the same harness reuses this row; the per-pass facts live on
// ReviewRun.
type Review struct {
	ID        string          `json:"id"`
	SessionID SessionID       `json:"sessionId"`
	ProjectID ProjectID       `json:"projectId"`
	Harness   ReviewerHarness `json:"harness"`
	PRURL     string          `json:"prUrl"`
	// ReviewerHandleID is the runtime handle of the live reviewer pane, reused
	// across passes and exposed so the UI can attach its terminal.
	ReviewerHandleID string `json:"reviewerHandleId"`
	AgentSessionID   string `json:"agentSessionId"`
	// ReviewerLaunchID is the Tallpine runtime generation that owns the live reviewer
	// pane on this row. It fences delayed hooks from an older replaced reviewer.
	ReviewerLaunchID string `json:"-"`
	// ReviewerActivityState is the latest activity hook reported by the review
	// pane itself. It is separate from ReviewRun.Status so the UI can distinguish
	// "review pass exists" from "reviewer is actively working right now".
	ReviewerActivityState ActivityState `json:"reviewerActivityState,omitempty"`
	// InterfaceMode selects the durable reviewer surface. Chat reviewers own a
	// native conversation; TUI reviewers continue to use their terminal handle.
	InterfaceMode          ReviewerInterfaceMode `json:"interfaceMode"`
	ProviderConversationID string                `json:"providerConversationId"`
	ControllerGeneration   string                `json:"controllerGeneration"`
	ControllerError        string                `json:"controllerError"`
	CreatedAt              time.Time             `json:"createdAt"`
	UpdatedAt              time.Time             `json:"updatedAt"`

	// IsArchived retires the reviewer surface without deleting its history.
	IsArchived bool `json:"-"`
}

// ReviewerInterfaceMode selects the durable UI surface for a reviewer.
type ReviewerInterfaceMode string

const (
	// ReviewerInterfaceTUI uses the reviewer's terminal handle.
	ReviewerInterfaceTUI ReviewerInterfaceMode = "tui"
	// ReviewerInterfaceChat uses a reviewer-owned native chat.
	ReviewerInterfaceChat ReviewerInterfaceMode = "chat"
)

// ReviewerSurface gives clients one stable identifier for either reviewer UI.
type ReviewerSurface struct {
	Mode            ReviewerInterfaceMode `json:"mode"`
	ReviewID        string                `json:"reviewId"`
	Harness         ReviewerHarness       `json:"harness"`
	HandleID        string                `json:"handleId,omitempty"`
	ControllerError string                `json:"controllerError,omitempty"`
}

// ReviewRun is one review pass against a worker's PR.
type ReviewRun struct {
	ID        string    `json:"id"`
	ReviewID  string    `json:"reviewId"`
	SessionID SessionID `json:"sessionId"`
	// BatchID groups review runs created by one trigger so worker feedback can
	// be delivered once after the whole trigger batch is terminal. Empty marks
	// legacy/single-run delivery.
	BatchID string          `json:"batchId"`
	Harness ReviewerHarness `json:"harness"`
	// Model is the reviewer model this pass ran under when Tallpine resolved one;
	// empty means the harness default (or a pass recorded before Tallpine kept it).
	Model string `json:"model,omitempty"`
	// ModelReason says why Tallpine chose Model itself (a risky-path match); empty when explicit or default.
	ModelReason string `json:"modelReason,omitempty"`
	// TriggerSource records whether this pass was requested by a user, by an
	// Tallpine agent session through the CLI, or by the daemon auto-review coordinator.
	TriggerSource ReviewTriggerSource `json:"triggerSource" enum:"manual,agent,auto"`
	PRURL         string              `json:"prUrl"`
	// TargetSHA is the PR head commit this pass reviewed.
	TargetSHA string          `json:"targetSha"`
	Status    ReviewRunStatus `json:"status"`
	Verdict   ReviewVerdict   `json:"verdict"`
	// Body is the review text the reviewer submitted. It is recorded for Tallpine's
	// own tracking; the reviewer also posts the review to the PR itself.
	Body string `json:"body"`
	// GithubReviewID is the id of the GitHub PR review the reviewer posted for
	// this pass (the `gh api .../pulls/{n}/reviews` object id), recorded at
	// submit time. It is empty when the reviewer could not post to the provider.
	// When the pass requests changes, Tallpine includes it in the message to the
	// worker so the worker knows exactly which review to address and reply to.
	GithubReviewID string `json:"githubReviewId"`
	// PostFailed marks a finished pass whose review never reached the PR, so its
	// findings exist only in Tallpine. Derived from the other fields when a run is
	// read (see NotPostedToPR); it is not stored.
	PostFailed  bool       `json:"postFailed,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	DeliveredAt *time.Time `json:"deliveredAt,omitempty"`
	// AutoInjectReview snapshots the session policy when this result is first
	// recorded. Later toggle changes must not rewrite or deliver this run.
	AutoInjectReview bool `json:"autoInjectReview"`
}

// LocalReviewTargetPrefix starts the synthetic review key of a session branch
// that has no PR. Runs for such a branch are stored under local:<branch> in the
// PR URL column; nothing else is stored.
const LocalReviewTargetPrefix = "local:"

// IsLocalReviewTarget reports whether a review key names a local branch, not a PR.
func IsLocalReviewTarget(url string) bool { return strings.HasPrefix(url, LocalReviewTargetPrefix) }

// NotPostedToPR reports whether a finished pass on a PR recorded a verdict but
// no GitHub review: the reviewer could not post it. A local branch review has
// no PR to post to, and a pass without a verdict has nothing to post.
func (r ReviewRun) NotPostedToPR() bool {
	if r.Status != ReviewRunComplete && r.Status != ReviewRunDelivered {
		return false
	}
	return r.Verdict != VerdictNone && r.GithubReviewID == "" && !IsLocalReviewTarget(r.PRURL)
}

// ReviewTriggerSource identifies who initiated a review pass.
type ReviewTriggerSource string

const (
	// ReviewTriggerManual marks a user-initiated review pass.
	ReviewTriggerManual ReviewTriggerSource = "manual"
	// ReviewTriggerAgent marks a pass a Tallpine agent session (a worker reviewing
	// its own PR, or an orchestrator) requested through `tallpine review trigger`.
	ReviewTriggerAgent ReviewTriggerSource = "agent"
	// ReviewTriggerAuto marks a daemon-initiated review pass.
	ReviewTriggerAuto ReviewTriggerSource = "auto"
)

// ReviewRunStatus is the lifecycle state of a single review pass.
type ReviewRunStatus = contract.TallpineReviewRunStatus

// Review run statuses.
const (
	ReviewRunRunning   = contract.TallpineReviewRunRunning
	ReviewRunComplete  = contract.TallpineReviewRunComplete
	ReviewRunDelivered = contract.TallpineReviewRunDelivered
	ReviewRunFailed    = contract.TallpineReviewRunFailed
	ReviewRunCancelled = contract.TallpineReviewRunCancelled
)

// ReviewVerdict is the outcome a reviewer reports. The empty verdict marks a
// run that has not produced an outcome yet.
type ReviewVerdict = contract.TallpineReviewVerdict

// Review verdicts.
const (
	VerdictNone             = contract.TallpineReviewVerdictNone
	VerdictApproved         = contract.TallpineReviewVerdictApproved
	VerdictChangesRequested = contract.TallpineReviewVerdictChangesRequested
)

// QueuedReviewTrigger is a review pass waiting for its reviewer to be free: an
// agent asked for it while the reviewer was busy, or the one-shot retry of a
// dead pass found the reviewer busy. It names the PR, not a commit, so the pass
// reviews the head the PR has when it starts, and keeps the options the
// requester chose.
type QueuedReviewTrigger struct {
	SessionID     SessionID             `json:"sessionId"`
	PRURL         string                `json:"pullRequestUrl"`
	Harness       ReviewerHarness       `json:"reviewerHarness"`
	CreatedAt     time.Time             `json:"createdAt"`
	Model         string                `json:"model,omitempty"`
	Effort        string                `json:"effort,omitempty"`
	InterfaceMode ReviewerInterfaceMode `json:"interfaceMode,omitempty"`
	// TriggerSource is who asked: an automatic pass keeps its auto policy and
	// attribution when it finally starts.
	TriggerSource ReviewTriggerSource `json:"triggerSource"`
	// Attempts counts failed starts; NextAttemptAt is the earliest next one.
	Attempts      int       `json:"attempts"`
	NextAttemptAt time.Time `json:"nextAttemptAt"`
}

// CurrentHeadReviewRun is one Tallpine review pass recorded against a PR's current
// head commit, reduced to the fields a derived read model needs.
type CurrentHeadReviewRun struct {
	SessionID SessionID
	Harness   ReviewerHarness
	PRURL     string
	Status    ReviewRunStatus
	Verdict   ReviewVerdict
	ID        string
	CreatedAt time.Time
}

// ReviewerHandle names one live reviewer pane's runtime handle, independent
// of whether the worker session that spawned it still exists. A reviewer has
// no session row of its own — its identity is this review's id and
// SessionID (the worker it reviews) plus Harness — so the memory diagnostic
// needs this to find it by anything other than walking session roots.
type ReviewerHandle struct {
	ReviewID  string
	SessionID SessionID
	Harness   ReviewerHarness
	HandleID  string
}
