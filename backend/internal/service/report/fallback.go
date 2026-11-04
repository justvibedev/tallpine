package report

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// NoOrchestratorGrace is how long a report waits for its project's
// orchestrator to start before it is surfaced to the human instead.
const NoOrchestratorGrace = 2 * time.Minute

// Notifier records an Inbox notification for the human.
type Notifier interface {
	Notify(ctx context.Context, intent ports.NotificationIntent) error
}

// deliverWithoutOrchestrator handles a lane whose project has no live
// orchestrator: reports go to the worker's parent orchestrator (another
// project) when it has one, else to the Inbox once the grace period passed.
// The batch is acknowledged either way, so a late orchestrator never gets it
// a second time. It returns errNoOrchestrator while the reports still wait.
func (c *Coordinator) deliverWithoutOrchestrator(ctx context.Context, projectID domain.ProjectID, lane string) error {
	ls, hasLanes := c.store.(LaneStore)
	if !hasLanes && c.notifier == nil {
		return errNoOrchestrator
	}
	batch, err := c.claim(ctx, projectID, lane, "", projectRoster{})
	if err != nil || len(batch.Reports) == 0 {
		return err
	}
	now := c.now().UTC()
	if hasLanes {
		if parent, ok := c.parentOrchestrator(ctx, ls, projectID, batch.Reports); ok {
			return c.deliverBatch(ctx, parent, batch)
		}
	}
	if !batch.hasDeliverable() {
		_, err = c.store.AcknowledgeReportBatch(ctx, batch.ProjectID, batch.Token, now)
		return err
	}
	if c.notifier == nil || now.Sub(oldestReport(batch.Reports)) < NoOrchestratorGrace {
		_, relErr := c.store.ReleaseReportBatch(ctx, batch.ProjectID, batch.Token, now, "")
		if relErr != nil {
			return relErr
		}
		return errNoOrchestrator
	}
	first := batch.Reports[0]
	var body strings.Builder
	body.WriteString("Project " + string(projectID) + " has no orchestrator, so these reports came to you:")
	for _, r := range coalesceReports(batch.Reports, batch.Superseded) {
		if _, superseded := batch.Superseded[r.ID]; superseded {
			continue
		}
		fmt.Fprintf(&body, "\n- %s (%s): %s", reportSessionLink(r), r.State, strings.TrimSpace(r.Note))
	}
	if err := c.notifier.Notify(ctx, ports.NotificationIntent{
		Type: domain.NotificationNeedsInput, SessionID: first.SessionID, ProjectID: projectID,
		CreatedAt: now, SourceKey: "report-fallback:" + batch.ID,
		SessionDisplayName: first.SessionDisplayName,
		Body:               body.String(),
	}); err != nil {
		return c.backoff(ctx, batch, err)
	}
	c.clearFailures(batch)
	_, err = c.store.AcknowledgeReportBatch(ctx, batch.ProjectID, batch.Token, now)
	return err
}

// parentOrchestrator returns the live orchestrator of another project that
// spawned every reporting worker of the batch.
func (c *Coordinator) parentOrchestrator(ctx context.Context, ls LaneStore, projectID domain.ProjectID, reports []domain.ReportRecord) (domain.SessionRecord, bool) {
	var parent domain.SessionID
	for _, r := range reports {
		sl, err := ls.SessionLane(ctx, r.SessionID)
		if err != nil || sl.ParentSessionID == "" || (parent != "" && parent != sl.ParentSessionID) {
			return domain.SessionRecord{}, false
		}
		parent = sl.ParentSessionID
	}
	rec, ok, err := c.store.GetSession(ctx, parent)
	if err != nil || !ok || !liveOrchestratorElsewhere(rec, projectID) {
		return domain.SessionRecord{}, false
	}
	return rec, true
}

func oldestReport(reports []domain.ReportRecord) time.Time {
	oldest := reports[0].CreatedAt
	for _, r := range reports[1:] {
		if r.CreatedAt.Before(oldest) {
			oldest = r.CreatedAt
		}
	}
	return oldest
}

// RouteNotice says where a worker's reports go when that is not the project's
// own orchestrator; empty means they reach it. parent may be empty, in which
// case the worker's persisted parent is used.
func (s *Service) RouteNotice(ctx context.Context, project domain.ProjectID, worker, parent domain.SessionID) string {
	lister, ok := s.store.(interface {
		ListSessions(context.Context, domain.ProjectID) ([]domain.SessionRecord, error)
	})
	if !ok {
		return ""
	}
	recs, err := lister.ListSessions(ctx, project)
	if err != nil {
		return ""
	}
	if _, live := domain.PickOrchestrator(recs, nil, ""); live {
		return ""
	}
	if ls, ok := s.store.(LaneStore); ok && parent == "" && worker != "" {
		if sl, err := ls.SessionLane(ctx, worker); err == nil {
			parent = sl.ParentSessionID
		}
	}
	if parent != "" {
		if rec, found, err := s.store.GetSession(ctx, parent); err == nil && found && liveOrchestratorElsewhere(rec, project) {
			return fmt.Sprintf("project %s has no orchestrator: reports go to the parent orchestrator %s", project, parent)
		}
	}
	return fmt.Sprintf("project %s has no orchestrator: reports go to the Inbox after %d minutes", project, int(NoOrchestratorGrace.Minutes()))
}

func liveOrchestratorElsewhere(rec domain.SessionRecord, project domain.ProjectID) bool {
	return rec.ProjectID != project && rec.Kind == domain.KindOrchestrator && !rec.IsTerminated && rec.Activity.State != domain.ActivityExited
}
