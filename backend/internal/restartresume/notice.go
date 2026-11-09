package restartresume

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
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

// noteNotResumed notes every worker still pending once Resume has used its
// attempts: its turn was cut off and nothing delivered the resume, so the
// orchestrator has to hear about it (B622). The entries stay on file.
func (t *Tracker) noteNotResumed(ctx context.Context) {
	t.mu.Lock()
	entries := t.load()
	t.mu.Unlock()
	for _, e := range entries {
		rec, ok, err := t.store.GetSession(ctx, e.SessionID)
		if err != nil || !ok || rec.Kind != domain.KindWorker || rec.IsTerminated {
			continue
		}
		slog.Default().WarnContext(ctx, "restart resume: worker not resumed", "session", e.SessionID, "state", rec.Activity.State)
		t.mu.Lock()
		if t.notResumed == nil {
			t.notResumed = map[domain.ProjectID][]Entry{}
		}
		if !slices.ContainsFunc(t.notResumed[rec.ProjectID], func(have Entry) bool { return have.SessionID == e.SessionID }) {
			t.notResumed[rec.ProjectID] = append(t.notResumed[rec.ProjectID], e)
		}
		t.mu.Unlock()
	}
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
// background jobs were stopped, plus a separate notice naming any worker that
// did not resume. Call it after both.
func (t *Tracker) NotifyRestart(ctx context.Context, reports Reports) {
	t.mu.Lock()
	notices := t.notice
	t.notice = nil
	notResumed := t.notResumed
	t.notResumed = nil
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
	for _, entries := range notResumed {
		reportNotResumed(ctx, reports, entries)
	}
}

// reportNotResumed files the notice for one project's workers that did not
// resume. Unlike the resumed line it is not quiet: a stopped worker needs
// action. Its id comes from exactly the workers it names, because these entries
// stay on file across boots: the same set dedupes, and a new one is delivered.
func reportNotResumed(ctx context.Context, reports Reports, entries []Entry) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].SessionID < entries[j].SessionID })
	ids := make([]domain.SessionID, len(entries))
	keys := make([]string, len(entries))
	var at time.Time
	for i, e := range entries {
		ids[i] = e.SessionID
		keys[i] = ReportID(e)
		if e.InterruptedAt.After(at) {
			at = e.InterruptedAt
		}
	}
	sum := sha256.Sum256([]byte(strings.Join(keys, "\n")))
	id := "rpt_restart_not_resumed_" + hex.EncodeToString(sum[:])[:16]
	msg := fmt.Sprintf("[auto] restart: app restarted at %s: did not resume %d workers: %s (run tallpine session restore <id> on any still stopped if the work is still needed).",
		at.UTC().Format(time.RFC3339), len(ids), joinIDs(ids))
	_, err := reports.Create(ctx, reportsvc.CreateInput{ID: id, SessionID: ids[0], Synthesized: true, Immediate: true, Message: msg})
	if err != nil && !errors.Is(err, domain.ErrDuplicateReport) {
		slog.Default().WarnContext(ctx, "restart notice: not-resumed report failed", "session", ids[0], "err", err)
	}
}

func joinIDs(ids []domain.SessionID) string {
	names := make([]string, len(ids))
	for i, id := range ids {
		names[i] = string(id)
	}
	return strings.Join(names, ", ")
}
