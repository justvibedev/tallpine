package restartresume

import (
	"context"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
)

func TestResumeCollapsesManyWorkersIntoOneNotice(t *testing.T) {
	ctx := context.Background()
	tr, st := newTracker(t,
		worker("w-1", domain.ActivityActive, false),
		worker("w-2", domain.ActivityActive, false),
		worker("w-3", domain.ActivityActive, false),
	)
	if err := tr.Record(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []domain.SessionID{"w-1", "w-2", "w-3"} {
		st.recs[id] = worker(string(id), domain.ActivityIdle, false)
	}
	sender, reports := &fakeSender{}, &fakeReports{}
	tr.Resume(ctx, sender)
	tr.NotifyRestart(ctx, reports)
	if len(sender.sent) != 3 {
		t.Fatalf("sent = %v, want every worker messaged", sender.sent)
	}
	if len(reports.ids) != 1 {
		t.Fatalf("reports = %v, want one collapsed notice (B362)", reports.ids)
	}
}

func TestResumeAndWakeShareOneRestartLine(t *testing.T) {
	ctx := context.Background()
	tr, st := newTracker(t, worker("w-1", domain.ActivityActive, false), worker("w-2", domain.ActivityIdle, false))
	if err := tr.Record(ctx); err != nil {
		t.Fatal(err)
	}
	st.recs["w-1"] = worker("w-1", domain.ActivityIdle, false)
	seedLostJobs(tr, "w-2")
	sender, reports := &fakeSender{}, &fakeReports{}
	tr.Resume(ctx, sender)
	tr.WakeIdle(ctx, sender, nil)
	tr.NotifyRestart(ctx, reports)
	tr.NotifyRestart(ctx, reports) // flushed once
	if len(reports.ids) != 1 {
		t.Fatalf("reports = %v, want one line for the whole restart (B362)", reports.ids)
	}
}

func TestRestartLineGoesToEachWorkersOwnProject(t *testing.T) {
	ctx := context.Background()
	a, b := worker("a-1", domain.ActivityIdle, false), worker("b-1", domain.ActivityIdle, false)
	a.ProjectID, b.ProjectID = "pa", "pb"
	tr, _ := newTracker(t, a, b)
	seedLostJobs(tr, "a-1", "b-1")
	rec := &projectReports{}
	tr.WakeIdle(ctx, &fakeSender{}, nil)
	tr.NotifyRestart(ctx, rec)
	if len(rec.byProject) != 2 || rec.byProject["a-1"] != 1 || rec.byProject["b-1"] != 1 {
		t.Fatalf("notices by first worker = %v, want one per project", rec.byProject)
	}
	for _, m := range rec.msgs {
		if strings.Contains(m, "a-1") && strings.Contains(m, "b-1") {
			t.Fatalf("notice mixes projects: %s", m)
		}
	}
}

type projectReports struct {
	byProject map[domain.SessionID]int
	msgs      []string
}

func (p *projectReports) Create(_ context.Context, in reportsvc.CreateInput) (domain.ReportRecord, error) {
	if p.byProject == nil {
		p.byProject = map[domain.SessionID]int{}
	}
	p.byProject[in.SessionID]++
	p.msgs = append(p.msgs, in.Message)
	return domain.ReportRecord{}, nil
}
