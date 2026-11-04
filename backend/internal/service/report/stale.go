package report

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// StaleReportStore is an optional DeliveryStore capability. A store without it
// delivers every claimed report, as before stale suppression existed.
type StaleReportStore interface {
	ListPRsBySession(context.Context, domain.SessionID) ([]domain.PullRequest, error)
	// DropClaimedReport settles one claimed report undelivered, keeping the reason.
	DropClaimedReport(ctx context.Context, id, token string, at time.Time, reason string) (bool, error)
}

// dropStale settles, without delivering, claimed reports that no longer matter
// by delivery time, and returns the rest. The rows stay in history with the
// reason in last_error. Any lookup failure keeps the report: delivering a
// stale one is cheaper than losing a live one.
func (c *Coordinator) dropStale(ctx context.Context, token string, reports []domain.ReportRecord) []domain.ReportRecord {
	store, ok := c.store.(StaleReportStore)
	if !ok {
		return reports
	}
	kept := make([]domain.ReportRecord, 0, len(reports))
	for _, r := range reports {
		reason := c.quietStaleReason(ctx, r)
		if reason == "" {
			reason = c.staleReason(ctx, store, r)
		}
		if reason == "" {
			reason = c.supersededHeadReason(ctx, r)
		}
		if reason == "" {
			kept = append(kept, r)
			continue
		}
		dropped, err := store.DropClaimedReport(ctx, r.ID, token, c.now().UTC(), reason)
		if err != nil || !dropped {
			kept = append(kept, r)
			continue
		}
		c.log.Info("worker report dropped undelivered: stale", "report", r.ID, "session", r.SessionID, "reason", reason)
	}
	return kept
}

// staleReason says why a report carries nothing the orchestrator still needs,
// or "" when it must be delivered. Only plain status and decision requests
// (checkpoint, needs_input) are ever dropped; stuck (blocked/failed) and done
// reports always go out. A report that names PRs is stale only when every one
// is merged or closed (for a live worker, only if the report predates that change); one that names none is stale only when its worker is
// terminated and every PR the worker owns is merged or closed. A PR still open
// (for example a newly pushed head) keeps the report.
func (c *Coordinator) staleReason(ctx context.Context, store StaleReportStore, r domain.ReportRecord) string {
	if r.State != domain.ReportCheckpoint && r.State != domain.ReportNeedsInput {
		return ""
	}
	prs, err := store.ListPRsBySession(ctx, r.SessionID)
	if err != nil {
		return ""
	}
	byURL := make(map[string]domain.PullRequest, len(prs))
	for _, pr := range prs {
		byURL[pr.URL] = pr
	}
	session, found, err := c.store.GetSession(ctx, r.SessionID)
	if err != nil || !found {
		return ""
	}
	var named []domain.PullRequest
	for _, o := range r.Outputs {
		if o.Kind != domain.ReportOutputPRCreated && o.Kind != domain.ReportOutputPRReviewed {
			return "" // an artifact is new information
		}
		pr, tracked := byURL[o.Reference]
		if !tracked {
			return ""
		}
		named = append(named, pr)
	}
	if len(named) == 0 {
		if !session.IsTerminated {
			return ""
		}
		named = prs
	}
	if len(named) == 0 {
		return ""
	}
	urls := make([]string, 0, len(named))
	for _, pr := range named {
		if !pr.Merged && !pr.Closed {
			return ""
		}
		// A live worker's report written after the PR changed state reacts to
		// that event (reopen or abandon? start part 2?) and must be seen.
		if !session.IsTerminated && (pr.StateChangedAt.IsZero() || !r.CreatedAt.Before(pr.StateChangedAt)) {
			return ""
		}
		urls = append(urls, pr.URL)
	}
	return fmt.Sprintf("not delivered: stale at delivery, PR already merged or closed (%s)", strings.Join(urls, ", "))
}

// QuietReportStore is an optional store capability for quiet reports (held
// FYIs such as turn_ended or progress checkpoints).
type QuietReportStore interface {
	// QuietReportState reports whether id is quiet, and if so whether its
	// session filed a later report of any state.
	QuietReportState(ctx context.Context, id string) (quiet, superseded bool, err error)
}

// quietStaleReason discards a held-back quiet FYI that no longer informs the
// orchestrator: its sender was terminated, or the same session filed a later
// report (B403). Non-quiet reports are never touched; a lookup failure keeps
// the report.
func (c *Coordinator) quietStaleReason(ctx context.Context, r domain.ReportRecord) string {
	qs, ok := c.store.(QuietReportStore)
	if !ok || !sessionScoped(r) {
		return ""
	}
	quiet, superseded, err := qs.QuietReportState(ctx, r.ID)
	if err != nil || !quiet {
		return ""
	}
	if superseded {
		return "quiet report superseded by a later report of the same session"
	}
	if session, found, err := c.store.GetSession(ctx, r.SessionID); err == nil && found && session.IsTerminated {
		return "quiet report from a terminated session"
	}
	return ""
}

// sessionScoped reports whether a quiet report speaks only for its own sender.
// Daemon notices that merely borrow a worker as carrier (the restart notice,
// B362) are project-wide and must outlive that worker.
func sessionScoped(r domain.ReportRecord) bool {
	if !r.Synthesized {
		return true
	}
	return strings.HasPrefix(r.Message, "[auto] turn_ended:") || strings.HasPrefix(r.Message, "[auto] usage_limited:")
}

// SessionReportLister is an optional DeliveryStore capability; a store without
// it never supersedes head reports.
type SessionReportLister interface {
	ListReportsBySession(context.Context, domain.SessionID) ([]domain.ReportRecord, error)
}

// supersededHeadReason drops an undelivered needs_input report about a PR when
// the same worker filed a later needs_input or done report naming that PR (a
// newer head while the first still waits): the later one carries the news, and
// each would otherwise be its own orchestrator wake (B426). Daemon notices and
// reports without PR outputs are never touched; a lookup failure keeps it.
func (c *Coordinator) supersededHeadReason(ctx context.Context, r domain.ReportRecord) string {
	lister, ok := c.store.(SessionReportLister)
	if !ok || r.Synthesized || r.State != domain.ReportNeedsInput {
		return ""
	}
	prs := map[string]bool{}
	for _, o := range r.Outputs {
		if o.Kind == domain.ReportOutputPRCreated || o.Kind == domain.ReportOutputPRReviewed {
			prs[o.Reference] = true
		}
	}
	if len(prs) == 0 {
		return ""
	}
	later, err := lister.ListReportsBySession(ctx, r.SessionID)
	if err != nil {
		return ""
	}
	for _, n := range later {
		if n.ID == r.ID || n.Synthesized || (n.State != domain.ReportNeedsInput && n.State != domain.ReportDone) {
			continue
		}
		if n.CreatedAt.Before(r.CreatedAt) || (n.CreatedAt.Equal(r.CreatedAt) && n.ID < r.ID) {
			continue
		}
		for _, o := range n.Outputs {
			if prs[o.Reference] {
				return "not delivered: superseded by a later report of the same session about the same PR"
			}
		}
	}
	return ""
}
