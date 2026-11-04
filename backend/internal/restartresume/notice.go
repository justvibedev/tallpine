package restartresume

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
)

// projectNotice collects one project's workers a restart resumed or woke, so
// its orchestrator gets one line for the whole restart instead of a card per
// worker (B362).
type projectNotice struct {
	at      time.Time
	id      string
	first   domain.SessionID
	resumed []domain.SessionID
	woken   []domain.SessionID
}

func (t *Tracker) noteResumed(e Entry, project domain.ProjectID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.note(project, e.SessionID, e.InterruptedAt, ReportID(e), false)
}

func (t *Tracker) noteWoken(id domain.SessionID, project domain.ProjectID, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.note(project, id, at, WakeID(id, at), true)
}

// note records id under its project. Caller holds t.mu.
func (t *Tracker) note(project domain.ProjectID, id domain.SessionID, at time.Time, reportID string, woken bool) {
	if t.notice == nil {
		t.notice = map[domain.ProjectID]*projectNotice{}
	}
	n := t.notice[project]
	if n == nil {
		n = &projectNotice{at: at, id: reportID, first: id}
		t.notice[project] = n
	}
	for _, have := range append(append([]domain.SessionID{}, n.resumed...), n.woken...) {
		if have == id {
			return
		}
	}
	if woken {
		n.woken = append(n.woken, id)
	} else {
		n.resumed = append(n.resumed, id)
	}
}

// NotifyRestart files one orchestrator-facing line per project for this
// restart: the workers Resume messaged and the ones WakeIdle told their
// background jobs were stopped. Call it after both.
func (t *Tracker) NotifyRestart(ctx context.Context, reports Reports) {
	t.mu.Lock()
	notices := t.notice
	t.notice = nil
	t.mu.Unlock()
	if reports == nil {
		return
	}
	projects := make([]string, 0, len(notices))
	for p := range notices {
		projects = append(projects, string(p))
	}
	sort.Strings(projects)
	for _, p := range projects {
		n := notices[domain.ProjectID(p)]
		parts := []string{}
		if len(n.resumed) > 0 {
			parts = append(parts, fmt.Sprintf("resumed %d workers: %s", len(n.resumed), joinIDs(n.resumed)))
		}
		if len(n.woken) > 0 {
			parts = append(parts, fmt.Sprintf("background jobs stopped for %d workers: %s", len(n.woken), joinIDs(n.woken)))
		}
		msg := fmt.Sprintf("[auto] restart: app restarted at %s: %s.", n.at.UTC().Format(time.RFC3339), strings.Join(parts, "; "))
		_, err := reports.Create(ctx, reportsvc.CreateInput{ID: n.id, SessionID: n.first, Synthesized: true, Immediate: true, Quiet: true, Message: msg})
		if err != nil && !errors.Is(err, domain.ErrDuplicateReport) {
			slog.Default().WarnContext(ctx, "restart notice: report failed", "session", n.first, "err", err)
		}
	}
}

func joinIDs(ids []domain.SessionID) string {
	names := make([]string, len(ids))
	for i, id := range ids {
		names[i] = string(id)
	}
	return strings.Join(names, ", ")
}
