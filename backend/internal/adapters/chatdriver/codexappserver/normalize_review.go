package codexappserver

import (
	"encoding/json"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/commanddetail"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func autoReviewStarted(n notification, _ time.Time) []ports.ChatEvent {
	// The provider is deciding, on the user's behalf, whether an action is safe to
	// run unattended. VERIFIED live with approvalsReviewer=auto_review: a curl
	// command produced started, then a guardianWarning, then completed carrying
	// riskLevel "low" and the reviewer's rationale.
	//
	// Surfaced because of what it is: consent nobody gave explicitly. A user who
	// can see "this was auto-approved as low risk, here is why" can disagree with
	// it; one who cannot see it has had a decision made silently.
	var p codexproto.ItemGuardianApprovalReviewStartedNotification
	if err := json.Unmarshal(n.Params, &p); err != nil {
		return nil
	}
	return []ports.ChatEvent{{
		Kind:           ports.ChatEventActivityStarted,
		ProviderTurnID: p.TurnID,
		ProviderItemID: reviewItemID(p.ReviewID),
		ActivityKind:   domain.ActivityKindAutoReview,
		ActivityStatus: domain.ActivityStatusRunning,
		Summary:        autoReviewSummary(p.Action, p.Review),
		Detail: encodeDetail(autoReviewDetail(
			p.ReviewID, p.TargetItemID, p.Action, p.Review, "", p.StartedAtMs, 0)),
	}}
}

func autoReviewCompleted(n notification, _ time.Time) []ports.ChatEvent {
	var p codexproto.ItemGuardianApprovalReviewCompletedNotification
	if err := json.Unmarshal(n.Params, &p); err != nil {
		return nil
	}
	return []ports.ChatEvent{{
		Kind:           ports.ChatEventActivityCompleted,
		ProviderTurnID: p.TurnID,
		ProviderItemID: reviewItemID(p.ReviewID),
		ActivityKind:   domain.ActivityKindAutoReview,
		ActivityStatus: autoReviewStatus(p.Review.Status),
		Summary:        autoReviewSummary(p.Action, p.Review),
		Detail: encodeDetail(autoReviewDetail(
			p.ReviewID, p.TargetItemID, p.Action, p.Review,
			string(p.DecisionSource), p.StartedAtMs, p.CompletedAtMs)),
	}}
}

// reviewItemID keys an auto-review activity.
//
// The provider gives a reviewId but no item id, and the review is not an item. The
// prefix is what keeps a synthetic key from ever colliding with a real provider
// item id (which are `rs_`, `msg_`, `exec-`, or a uuid), so the started and
// completed notifications update one row instead of creating two.
func reviewItemID(reviewID string) string {
	if reviewID == "" {
		return ""
	}
	return "ao-review-" + reviewID
}

// autoReviewStatus maps a review outcome onto an activity status. Anything that is
// not an approval is a failure: timedOut and aborted mean the action did not get
// consent, and reporting them as completed would read as approval.
func autoReviewStatus(status codexproto.GuardianApprovalReviewStatus) domain.ActivityStatus {
	switch status {
	case codexproto.GuardianApprovalReviewStatusApproved:
		return domain.ActivityStatusCompleted
	case codexproto.GuardianApprovalReviewStatusInProgress:
		return domain.ActivityStatusRunning
	default:
		return domain.ActivityStatusFailed
	}
}

// autoReviewSummary labels an auto-review with the decision and what it was about.
func autoReviewSummary(
	action codexproto.GuardianApprovalReviewAction,
	review codexproto.GuardianApprovalReview,
) string {
	subject := autoReviewSubject(action)
	switch review.Status {
	case codexproto.GuardianApprovalReviewStatusApproved:
		risk := ""
		if review.RiskLevel != nil {
			risk = fmt.Sprintf(" (%s risk)", *review.RiskLevel)
		}
		return "Auto-approved " + subject + risk
	case codexproto.GuardianApprovalReviewStatusDenied:
		return "Auto-declined " + subject
	case codexproto.GuardianApprovalReviewStatusInProgress:
		return "Reviewing " + subject
	default:
		return fmt.Sprintf("Auto-review %s: %s", review.Status, subject)
	}
}

// autoReviewSubject names the action under review, preferring the concrete thing
// over the category.
func autoReviewSubject(action codexproto.GuardianApprovalReviewAction) string {
	switch {
	case action.Command != nil && *action.Command != "":
		return commandSummary(*action.Command)
	case action.ToolName != nil && *action.ToolName != "":
		return "tool " + *action.ToolName
	case action.Host != nil && *action.Host != "":
		return "network access to " + *action.Host
	case len(action.Files) > 0:
		return fmt.Sprintf("changes to %d file(s)", len(action.Files))
	case action.Program != nil && *action.Program != "":
		return *action.Program
	default:
		return string(action.Type)
	}
}

// autoReviewDetail is the neutral payload for an auto-review row. Only named
// fields: the provider's action union carries a dozen optional members and passing
// it through verbatim would put a provider DTO on the wire.
func autoReviewDetail(
	reviewID string,
	targetItemID *string,
	action codexproto.GuardianApprovalReviewAction,
	review codexproto.GuardianApprovalReview,
	decisionSource string,
	startedAtMs, completedAtMs int64,
) map[string]any {
	detail := map[string]any{
		"reviewId":   reviewID,
		"actionType": string(action.Type),
		"status":     string(review.Status),
	}
	if targetItemID != nil && *targetItemID != "" {
		// The item the review was about, so a client can attach the decision to the
		// command it gated instead of showing it floating on its own.
		detail["targetItemId"] = *targetItemID
	}
	if action.Command != nil && *action.Command != "" {
		detail["command"] = commanddetail.UnwrapShell(*action.Command)
		detail["rawCommand"] = *action.Command
	}
	if action.Cwd != nil && *action.Cwd != "" {
		detail["cwd"] = string(*action.Cwd)
	}
	if action.Source != nil {
		detail["commandSource"] = string(*action.Source)
	}
	if action.ToolName != nil && *action.ToolName != "" {
		detail["toolName"] = *action.ToolName
	}
	if action.Server != nil && *action.Server != "" {
		detail["server"] = *action.Server
	}
	if action.Host != nil && *action.Host != "" {
		detail["host"] = *action.Host
	}
	if len(action.Files) > 0 {
		paths := make([]string, 0, len(action.Files))
		for _, file := range action.Files {
			paths = append(paths, string(file))
		}
		detail["files"] = paths
	}
	if review.RiskLevel != nil {
		detail["riskLevel"] = string(*review.RiskLevel)
	}
	if review.UserAuthorization != nil {
		detail["userAuthorization"] = string(*review.UserAuthorization)
	}
	if review.Rationale != nil && *review.Rationale != "" {
		detail["rationale"] = *review.Rationale
	}
	if decisionSource != "" {
		// Who decided. "agent" means a model made the call, which is a materially
		// different thing to show a user than a policy rule matching.
		detail["decisionSource"] = decisionSource
	}
	if startedAtMs > 0 && completedAtMs > startedAtMs {
		detail["durationMs"] = completedAtMs - startedAtMs
	}
	return detail
}
