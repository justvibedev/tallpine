package domain

import (
	"errors"
	"time"
)

// NotificationType identifies a user-facing notification kind persisted for the dashboard.
type NotificationType string

const (
	// NotificationNeedsInput means an agent session is waiting for user input.
	NotificationNeedsInput NotificationType = "needs_input"
	// NotificationReadyToMerge means a PR has no known merge blockers.
	NotificationReadyToMerge NotificationType = "ready_to_merge"
	// NotificationPRMerged means a tracked PR was merged.
	NotificationPRMerged NotificationType = "pr_merged"
	// NotificationPRClosedUnmerged means a tracked PR closed without merging.
	NotificationPRClosedUnmerged NotificationType = "pr_closed_unmerged"
	// NotificationReviewCompleted means a Tallpine reviewer approved a pull request.
	NotificationReviewCompleted NotificationType = "review_completed"
	// NotificationReviewChangesRequested means a Tallpine reviewer found changes to make.
	NotificationReviewChangesRequested NotificationType = "review_changes_requested"
	// NotificationOrchestratorApproval means an orchestrator answered a worker's
	// permission prompt. A terminal fact for the human, never held open.
	NotificationOrchestratorApproval NotificationType = "orchestrator_approval"
	// NotificationPlanWaiting means a plan reached awaiting_answers or
	// awaiting_approval and waits on the owner. Surfaced once as unseen.
	NotificationPlanWaiting NotificationType = "plan_waiting"
	// NotificationWorktreeKept means cleanup kept an ended session's worktree
	// (salvage failed, unmerged work, a nested repo) and said why. A settled
	// fact: sent once per worktree and reason.
	NotificationWorktreeKept NotificationType = "worktree_kept"
)

// Valid reports whether t is one of the v1 notification kinds.
func (t NotificationType) Valid() bool {
	switch t {
	case NotificationNeedsInput, NotificationReadyToMerge, NotificationPRMerged, NotificationPRClosedUnmerged,
		NotificationReviewCompleted, NotificationReviewChangesRequested, NotificationOrchestratorApproval, NotificationPlanWaiting,
		NotificationWorktreeKept:
		return true
	default:
		return false
	}
}

// NeedsResolution reports whether t describes an issue that stays open until
// something changes (an agent waiting on the user, a PR waiting on a merge).
// Terminal facts — a PR that merged or closed — describe something that already
// happened, so they are surfaced once as unseen and never held as unresolved.
func (t NotificationType) NeedsResolution() bool {
	return t == NotificationNeedsInput || t == NotificationReadyToMerge
}

// NotificationUrgency says how insistently a notification should signal the user.
type NotificationUrgency string

const (
	// NotificationUrgencyCritical means an agent or plan is blocked on the human:
	// keep signalling (sound, flash, a dock bounce that lasts) until they return.
	NotificationUrgencyCritical NotificationUrgency = "critical"
	// NotificationUrgencyAttention means the human has something to act on, but
	// nothing is blocked: sound and flash, a one-shot dock bounce.
	NotificationUrgencyAttention NotificationUrgency = "attention"
	// NotificationUrgencyInfo means a settled fact: a toast and nothing louder.
	NotificationUrgencyInfo NotificationUrgency = "info"
)

// Urgency is the single home of the per-type urgency rule. Electron main and
// the mobile app read it off the wire instead of copying the type list. An
// unknown type is informational.
func (t NotificationType) Urgency() NotificationUrgency {
	switch t {
	case NotificationNeedsInput, NotificationPlanWaiting:
		return NotificationUrgencyCritical
	case NotificationReadyToMerge, NotificationReviewChangesRequested, NotificationOrchestratorApproval:
		return NotificationUrgencyAttention
	default:
		return NotificationUrgencyInfo
	}
}

// NotificationStatus is the seen state for a stored notification. The stored
// values remain "unread"/"read" for wire and schema compatibility; "unread"
// means the user has not opened the notification panel since it arrived.
type NotificationStatus string

const (
	// NotificationUnread marks a notification that has not been acknowledged.
	NotificationUnread NotificationStatus = "unread"
	// NotificationRead marks a notification that has been acknowledged.
	NotificationRead NotificationStatus = "read"
)

// Valid reports whether s is a supported notification read state.
func (s NotificationStatus) Valid() bool {
	switch s {
	case NotificationUnread, NotificationRead:
		return true
	default:
		return false
	}
}

// NotificationListStatus selects which stored notifications are returned.
type NotificationListStatus string

const (
	// NotificationListUnread returns only notifications that still need acknowledgement.
	NotificationListUnread NotificationListStatus = "unread"
	// NotificationListAll returns both read and unread notifications.
	NotificationListAll NotificationListStatus = "all"
	// NotificationListUnresolved returns notifications whose underlying issue is
	// still open, regardless of whether the user has already seen them.
	NotificationListUnresolved NotificationListStatus = "unresolved"
)

// Valid reports whether s is a supported notification list filter.
func (s NotificationListStatus) Valid() bool {
	switch s {
	case NotificationListUnread, NotificationListAll, NotificationListUnresolved:
		return true
	default:
		return false
	}
}

// NotificationRecord is the durable notification persistence shape.
type NotificationRecord struct {
	ID        string
	SessionID SessionID
	ProjectID ProjectID
	PRURL     string
	Type      NotificationType
	Title     string
	Body      string
	Status    NotificationStatus
	CreatedAt time.Time
	// ResolvedAt is when the underlying issue went away — the session received
	// its input, or the PR stopped waiting on a merge. Zero means still open.
	// Only Tallpine writes it; there is no user-facing "resolve" action.
	ResolvedAt time.Time
	// SourceKey identifies the durable transition that produced a terminal
	// notification. It is internal dedupe metadata and is not exposed over HTTP.
	SourceKey string
}

// Resolved reports whether the issue behind this notification is closed.
func (r NotificationRecord) Resolved() bool { return !r.ResolvedAt.IsZero() }

// NotificationEventKind distinguishes the live notification stream events.
type NotificationEventKind string

const (
	// NotificationCreated announces a newly persisted notification.
	NotificationCreated NotificationEventKind = "created"
	// NotificationResolved announces that a stored notification's underlying
	// issue went away, so open dashboards can drop it from the unresolved list.
	NotificationResolved NotificationEventKind = "resolved"
	// NotificationDeleted announces that one stored notification was deleted.
	NotificationDeleted NotificationEventKind = "deleted"
	// NotificationCleared announces that notification history was deleted.
	NotificationCleared NotificationEventKind = "cleared"
)

// NotificationEvent is one live notification-stream message.
type NotificationEvent struct {
	Kind          NotificationEventKind
	Record        NotificationRecord
	ClearID       string
	ClearEpoch    string
	ClearSequence int64
}

var (
	// ErrInvalidNotificationType reports an unknown notification type.
	ErrInvalidNotificationType = errors.New("invalid notification type")
	// ErrInvalidNotificationStatus reports an unknown notification status.
	ErrInvalidNotificationStatus = errors.New("invalid notification status")
	// ErrInvalidNotificationRecord reports a missing required notification field.
	ErrInvalidNotificationRecord = errors.New("invalid notification record")
)

// Validate checks the required fields and enum values for a stored notification.
func (r NotificationRecord) Validate() error {
	if r.SessionID == "" || r.Title == "" || r.CreatedAt.IsZero() {
		return ErrInvalidNotificationRecord
	}
	if !r.Type.Valid() {
		return ErrInvalidNotificationType
	}
	if !r.Status.Valid() {
		return ErrInvalidNotificationStatus
	}
	return nil
}
