package lifecycle

import (
	"context"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// ApplySCMObservation is the provider-neutral lifecycle entrypoint used by the
// SCM observer. The existing reaction logic still operates on PRObservation, so
// lifecycle performs the compatibility projection internally instead of leaking
// the old PR DTO back into the observer/provider boundary.
func (m *Manager) ApplySCMObservation(ctx context.Context, id domain.SessionID, o ports.SCMObservation) error {
	if !o.Fetched {
		return nil
	}
	m.NoteMergeRequester(id, ports.MergeRequester(ctx))
	if err := m.ApplyPRObservation(ctx, id, scmToPRObservation(o)); err != nil {
		return err
	}
	ready, err := m.scmObservationReadyToMerge(ctx, o)
	if err != nil {
		return err
	}
	intent, err := m.notificationIntentForCurrentSCM(ctx, id, o, ready)
	if err != nil {
		return err
	}
	m.emitNotification(ctx, intent)
	m.resolveNotifications(ctx, readyToMergeResolutions(id, o, ready, m.clock())...)
	return nil
}

// readyToMergeResolutions reports the ready-to-merge notification this
// observation made stale. The PR either got merged/closed, or stopped being
// mergeable — either way the "this is ready for you to merge" ping no longer
// describes anything the user can act on.
func readyToMergeResolutions(id domain.SessionID, o ports.SCMObservation, ready bool, now time.Time) []ports.NotificationResolution {
	if ready {
		return nil
	}
	return []ports.NotificationResolution{{
		Type:       domain.NotificationReadyToMerge,
		SessionID:  id,
		PRURL:      firstSCMNonEmpty(o.PR.URL, o.PR.HTMLURL),
		ResolvedAt: timeOr(o.ObservedAt, now),
	}}
}

func (m *Manager) notificationIntentForCurrentSCM(ctx context.Context, id domain.SessionID, o ports.SCMObservation, ready bool) (*ports.NotificationIntent, error) {
	// Serialize the session snapshot with this session's activity reducers (the
	// session lock) so a ready-to-merge notification does not race a
	// simultaneous waiting_input update.
	defer m.sessionLocks.lock(id)()
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return m.notificationIntentForSCM(rec, o, ready), nil
}

func (m *Manager) notificationIntentForSCM(rec domain.SessionRecord, o ports.SCMObservation, ready bool) *ports.NotificationIntent {
	prURL := firstSCMNonEmpty(o.PR.URL, o.PR.HTMLURL)
	base := ports.NotificationIntent{
		SessionID:          rec.ID,
		ProjectID:          rec.ProjectID,
		PRURL:              prURL,
		CreatedAt:          timeOr(o.ObservedAt, m.clock()),
		SessionDisplayName: rec.DisplayName,
		PRNumber:           o.PR.Number,
		PRTitle:            o.PR.Title,
		PRSourceBranch:     o.PR.SourceBranch,
		PRTargetBranch:     o.PR.TargetBranch,
		Provider:           o.Provider,
		Repo:               o.Repo,
	}
	if o.PR.Merged {
		base.Type = domain.NotificationPRMerged
		return &base
	}
	if o.PR.Closed {
		base.Type = domain.NotificationPRClosedUnmerged
		return &base
	}
	if rec.IsTerminated || rec.Activity.State.NeedsInput() || !ready {
		return nil
	}
	base.Type = domain.NotificationReadyToMerge
	return &base
}

// scmObservationIsReadyToMerge projects a live observation into the shared
// readiness rule (domain.MergeReadiness). Startup reconciliation applies the
// same rule to the stored facts, so the two paths cannot disagree about what
// "ready to merge" means.
func (m *Manager) scmObservationReadyToMerge(ctx context.Context, o ports.SCMObservation) (bool, error) {
	ready := domain.MergeReadiness{
		Draft:              o.PR.Draft,
		Merged:             o.PR.Merged,
		Closed:             o.PR.Closed,
		CI:                 domain.CIState(o.CI.Summary),
		Review:             domain.ReviewDecision(o.Review.Decision),
		Mergeability:       domain.Mergeability(o.Mergeability.State),
		UnresolvedComments: hasUnresolvedSCMComments(o.Review.Threads),
	}.ReadyToMerge()
	if !ready {
		return false, nil
	}
	prURL := firstSCMNonEmpty(o.PR.URL, o.PR.HTMLURL)
	comments, err := m.store.ListPRComments(ctx, prURL)
	if err != nil {
		return false, fmt.Errorf("list persisted comments for %s: %w", prURL, err)
	}
	for _, comment := range comments {
		if domain.IsActionableReviewComment(comment.Resolved, comment.IsBot, comment.File, comment.Line) {
			return false, nil
		}
	}
	return true, nil
}

func hasUnresolvedSCMComments(threads []ports.SCMReviewThreadObservation) bool {
	for _, th := range threads {
		for _, c := range th.Comments {
			if domain.IsActionableReviewComment(th.Resolved, c.IsBot, th.Path, th.Line) {
				return true
			}
		}
	}
	return false
}

func scmToPRObservation(o ports.SCMObservation) ports.PRObservation {
	pr := ports.PRObservation{
		Fetched:      o.Fetched,
		URL:          firstSCMNonEmpty(o.PR.URL, o.PR.HTMLURL),
		Number:       o.PR.Number,
		Title:        o.PR.Title,
		SourceBranch: o.PR.SourceBranch,
		TargetBranch: o.PR.TargetBranch,
		Draft:        o.PR.Draft,
		Merged:       o.PR.Merged,
		Closed:       o.PR.Closed,
		CI:           domain.CIState(o.CI.Summary),
		Review:       domain.ReviewDecision(o.Review.Decision),
		Mergeability: domain.Mergeability(o.Mergeability.State),
		// The noCI hold masks the provider's "mergeable"; carry it so the
		// merge-conflict re-arm still sees the conflict cleared.
		MergeableHeldByNoCI: o.Mergeability.HeldByNoCI(),
	}
	if pr.CI == "" {
		pr.CI = domain.CIUnknown
	}
	if pr.Review == "" {
		pr.Review = domain.ReviewNone
	}
	if pr.Mergeability == "" {
		pr.Mergeability = domain.MergeUnknown
	}
	checkCommit := firstSCMNonEmpty(o.CI.HeadSHA, o.PR.HeadSHA)
	for _, ch := range o.CI.FailedChecks {
		status := domain.PRCheckStatus(ch.Status)
		if status == "" {
			status = domain.PRCheckFailed
		}
		logTail := ch.LogTail
		if logTail == "" {
			logTail = o.CI.FailureLogTail
		}
		pr.Checks = append(pr.Checks, ports.PRCheckObservation{
			Name:       ch.Name,
			CommitHash: checkCommit,
			Status:     status,
			URL:        ch.URL,
			LogTail:    logTail,
		})
	}
	return pr
}

func prCommentObservations(comments []domain.PullRequestComment) []ports.PRCommentObservation {
	out := make([]ports.PRCommentObservation, 0, len(comments))
	for _, comment := range comments {
		if !domain.IsActionableReviewComment(comment.Resolved, comment.IsBot, comment.File, comment.Line) {
			continue
		}
		out = append(out, ports.PRCommentObservation{
			ID:               comment.ID,
			ThreadID:         comment.ThreadID,
			ReviewID:         comment.ReviewID,
			Author:           comment.Author,
			File:             comment.File,
			Line:             comment.Line,
			Body:             comment.Body,
			URL:              comment.URL,
			Resolved:         comment.Resolved,
			AutoInjectReview: comment.AutoInjectReview,
		})
	}
	return out
}
