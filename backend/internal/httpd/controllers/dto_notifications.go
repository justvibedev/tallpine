package controllers

import (
	"time"
)

// ListNotificationsQuery is the query string accepted by GET /api/v1/notifications.
type ListNotificationsQuery struct {
	Status string `query:"status,omitempty" enum:"unread,all,unresolved" description:"Notification filter. Defaults to unread (unseen); unresolved returns notifications whose underlying issue is still open; all includes read history."`
	Limit  int    `query:"limit,omitempty" minimum:"1" maximum:"100" description:"Maximum notifications to return. Defaults to 100."`
	Cursor string `query:"cursor,omitempty" description:"Opaque cursor returned by the previous page."`
}

// NotificationStreamQuery is the query string accepted by GET /api/v1/notifications/stream.
type NotificationStreamQuery struct {
	ProjectID string `query:"projectId,omitempty" description:"Optional project id filter for live notifications."`
}

// NotificationIDParam is the {id} path parameter shared by notification routes.
type NotificationIDParam struct {
	ID string `path:"id" description:"Notification identifier."`
}

// NotificationTarget is the dashboard navigation target for a notification.
type NotificationTarget struct {
	Kind      string `json:"kind" enum:"session,pr"`
	SessionID string `json:"sessionId"`
	PRURL     string `json:"prUrl,omitempty"`
}

// NotificationResponse is one stored notification returned by the API.
type NotificationResponse struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	ProjectID string `json:"projectId"`
	PRURL     string `json:"prUrl"`
	Type      string `json:"type" enum:"needs_input,ready_to_merge,pr_merged,pr_closed_unmerged,review_completed,review_changes_requested,orchestrator_approval,plan_waiting,worktree_kept"`
	// Urgency is derived from Type (domain.NotificationType.Urgency).
	Urgency   string    `json:"urgency,omitempty" enum:"critical,attention,info" description:"How insistently clients should signal: critical = blocked on the human, attention = actionable, info = settled fact."`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	LaneChat  bool      `json:"laneChat,omitempty" description:"plan_waiting only: sessionId is the lane orchestrator whose chat holds the plan card, so open that session."`
	Status    string    `json:"status" enum:"unread,read" description:"Seen state. unread means the user has not opened the notification panel since it arrived."`
	CreatedAt time.Time `json:"createdAt"`
	// ResolvedAt is set by Tallpine when the underlying issue goes away (the session
	// received its input, the PR stopped waiting on a merge). Absent means the
	// issue is still open. There is no user-facing action that sets it.
	ResolvedAt *time.Time         `json:"resolvedAt,omitempty"`
	Target     NotificationTarget `json:"target"`
}

// ListNotificationsResponse is one history page from GET /api/v1/notifications.
type ListNotificationsResponse struct {
	Notifications   []NotificationResponse `json:"notifications"`
	NextCursor      string                 `json:"nextCursor,omitempty"`
	UnreadCount     int                    `json:"unreadCount"`
	UnresolvedCount int                    `json:"unresolvedCount"`
}

// MarkNotificationReadRequest is the body of PATCH /api/v1/notifications/{id}.
type MarkNotificationReadRequest struct {
	Status string `json:"status" enum:"read" description:"V1 supports only marking an unread notification read."`
}

// NotificationEnvelope is the { notification } response body for notification mutations.
type NotificationEnvelope struct {
	Notification NotificationResponse `json:"notification"`
}

// MarkAllNotificationsReadRequest is the optional body of
// POST /api/v1/notifications/read-all.
type MarkAllNotificationsReadRequest struct {
	IDs []string `json:"ids,omitempty" description:"Acknowledge exactly these notifications. Omit to acknowledge every unread notification; paginating clients should send the ids they actually rendered so later pages stay unread."`
}

// MarkAllNotificationsReadResponse is the body of POST /api/v1/notifications/read-all.
type MarkAllNotificationsReadResponse struct {
	Notifications []NotificationResponse `json:"notifications" description:"Deprecated compatibility field. Always empty so mark-all responses stay bounded."`
	UpdatedCount  int64                  `json:"updatedCount" description:"Number of notifications changed from unread to read."`
}

// ClearNotificationsResponse is the body of DELETE /api/v1/notifications.
type ClearNotificationsResponse struct {
	ClearedCount  int64  `json:"clearedCount" description:"Number of notifications deleted."`
	ClearID       string `json:"clearId" description:"Identifier shared with the ordered notification_cleared stream event."`
	ClearEpoch    string `json:"clearEpoch" description:"Daemon epoch for ordering notification clears across one daemon lifetime."`
	ClearSequence int64  `json:"clearSequence" description:"Monotonic notification-clear sequence within clearEpoch."`
}
