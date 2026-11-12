package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	sessionsvc "tallpine.eu/tallpine/backend/internal/service/session"
)

type recordingOwnerNotifier struct {
	got      []ports.NotificationIntent
	resolved []ports.NotificationResolution
	err      error
}

func (r *recordingOwnerNotifier) Notify(_ context.Context, in ports.NotificationIntent) error {
	r.got = append(r.got, in)
	return r.err
}

func TestPlanOwnerAlert_WritesPlanWaitingNotification(t *testing.T) {
	rec := &recordingOwnerNotifier{}
	alert := planOwnerAlert(rec, nil, nil)
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	plan := domain.BacklogPlan{ID: "plan_1", ProjectID: "p1", PlannerSessionID: "s-planner", Status: domain.PlanAwaitingApproval, UpdatedAt: at}
	alert(context.Background(), plan, "B7", "Add seats")
	plan.Status = domain.PlanAwaitingAnswers
	alert(context.Background(), plan, "B7", "Add seats")
	plan.PlannerSessionID = ""
	alert(context.Background(), plan, "B7", "Add seats") // no session to hang a notification on

	if len(rec.got) != 2 {
		t.Fatalf("want 2 notifications, got %d", len(rec.got))
	}
	a, q := rec.got[0], rec.got[1]
	if a.Type != domain.NotificationPlanWaiting || a.SessionID != "s-planner" || a.ProjectID != "p1" || !strings.Contains(a.Title, "B7") || !strings.Contains(a.Title, "approval") {
		t.Fatalf("approval intent = %+v", a)
	}
	if !strings.Contains(q.Title, "questions") || q.SourceKey == a.SourceKey {
		t.Fatalf("answers intent = %+v (source keys must differ per state)", q)
	}
	if planOwnerAlert(nil, nil, nil) != nil {
		t.Fatal("nil notifier must disable the alert")
	}
}

func TestPlanOwnerAlert_TextAndSourceKey(t *testing.T) {
	rec := &recordingOwnerNotifier{}
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	plan := domain.BacklogPlan{ID: "plan_1", ProjectID: "p1", PlannerSessionID: "s", Status: domain.PlanAwaitingAnswers, UpdatedAt: at}
	planOwnerAlert(rec, nil, nil)(context.Background(), plan, "B7", "Add seats")
	planOwnerAlert(rec, nil, nil)(context.Background(), plan, "B7", "")
	in := rec.got[0]
	if in.Title != "Plan for B7 has questions for you" || in.Body != "Add seats\nOpen the plan card to answer it." {
		t.Fatalf("text = %q / %q", in.Title, in.Body)
	}
	if want := "plan-waiting:plan_1:awaiting_answers:" + at.Format(time.RFC3339Nano); in.SourceKey != want {
		t.Fatalf("source key = %q, want %q", in.SourceKey, want)
	}
	if rec.got[1].Body != "Open the plan card to answer it." {
		t.Fatalf("body without item title = %q", rec.got[1].Body)
	}
}

func TestPlanOwnerAlert_NotifyErrorIsSwallowed(t *testing.T) {
	rec := &recordingOwnerNotifier{err: errors.New("db down")}
	plan := domain.BacklogPlan{ID: "plan_1", ProjectID: "p1", PlannerSessionID: "s", Status: domain.PlanAwaitingApproval}
	planOwnerAlert(rec, nil, nil)(context.Background(), plan, "B7", "x") // must neither panic nor return an error
	if len(rec.got) != 1 {
		t.Fatalf("notify calls = %d", len(rec.got))
	}
}

type leadSessions struct {
	items map[string]domain.BacklogItem
	orchs []domain.Session
	sent  []string
}

func (l *leadSessions) List(context.Context, sessionsvc.ListFilter) ([]domain.Session, error) {
	return l.orchs, nil
}
func (l *leadSessions) SendWithOptions(_ context.Context, id domain.SessionID, msg string, _ *ports.SpawnAttachment, _ ports.MessageDeliveryOptions) error {
	l.sent = append(l.sent, string(id)+"|"+msg)
	return nil
}
func (l *leadSessions) GetBacklogItem(_ context.Context, id string) (domain.BacklogItem, bool, error) {
	it, ok := l.items[id]
	return it, ok, nil
}

func leadOrch(id, lane string) domain.Session {
	return domain.Session{SessionRecord: domain.SessionRecord{ID: domain.SessionID(id)}, Lane: lane}
}

func TestPlanOwnerAlert_LanePlanNotifiesDefaultOrchestratorOnce(t *testing.T) {
	ls := &leadSessions{items: map[string]domain.BacklogItem{"it": {ID: "it", ParentID: "par"}, "par": {ID: "par", Seq: 9}}, orchs: []domain.Session{leadOrch("lane1", "B9"), leadOrch("main", "")}}
	alert := planOwnerAlert(&recordingOwnerNotifier{}, ls, ls)
	plan := domain.BacklogPlan{ID: "p1", ProjectID: "pr", ItemID: "it", PlannerSessionID: "pl", Status: domain.PlanAwaitingApproval}
	alert(context.Background(), plan, "B248", "t")
	alert(context.Background(), plan, "B248", "t") // same state again: no repeat
	if len(ls.sent) != 1 || ls.sent[0] != "main|[auto] plan_waiting: B248 in lane B9 is awaiting the owner (awaiting_approval)" {
		t.Fatalf("sent = %v", ls.sent)
	}
	plan.Status = domain.PlanAwaitingAnswers
	alert(context.Background(), plan, "B248", "t") // new state: notified
	if len(ls.sent) != 2 {
		t.Fatalf("sent = %v", ls.sent)
	}
}

func TestPlanOwnerAlert_DefaultOrchestratorPlanSendsNoLeadNotice(t *testing.T) {
	ls := &leadSessions{items: map[string]domain.BacklogItem{"it": {ID: "it", ParentID: "par"}, "par": {ID: "par", Seq: 5}}, orchs: []domain.Session{leadOrch("main", "")}}
	plan := domain.BacklogPlan{ID: "p1", ProjectID: "pr", ItemID: "it", PlannerSessionID: "pl", Status: domain.PlanAwaitingApproval}
	planOwnerAlert(&recordingOwnerNotifier{}, ls, ls)(context.Background(), plan, "B1", "t")
	if len(ls.sent) != 0 {
		t.Fatalf("sent = %v", ls.sent)
	}
}

func TestPlanOwnerAlert_LaneItemPlannedByItsOwnLaneNotifiesDefault(t *testing.T) {
	ls := &leadSessions{items: map[string]domain.BacklogItem{"it": {ID: "it", Seq: 9}}, orchs: []domain.Session{leadOrch("lane1", "B9"), leadOrch("main", "")}}
	plan := domain.BacklogPlan{ID: "p1", ProjectID: "pr", ItemID: "it", PlannerSessionID: "pl", Status: domain.PlanAwaitingApproval}
	planOwnerAlert(&recordingOwnerNotifier{}, ls, ls)(context.Background(), plan, "B9", "t")
	if len(ls.sent) != 1 {
		t.Fatalf("sent = %v", ls.sent)
	}
}

func (r *recordingOwnerNotifier) Resolve(_ context.Context, res ports.NotificationResolution) error {
	r.resolved = append(r.resolved, res)
	return r.err
}

// B681: clearing resolves the planner's plan_waiting row, so the desktop
// and phone stop showing an answered plan as pending.
func TestPlanOwnerClear_ResolvesPlannerPlanWaiting(t *testing.T) {
	rec := &recordingOwnerNotifier{}
	planOwnerClear(rec)(context.Background(), domain.BacklogPlan{ID: "plan_1", PlannerSessionID: "s-planner", Status: domain.PlanWriting})
	if len(rec.resolved) != 1 || rec.resolved[0].Type != domain.NotificationPlanWaiting || rec.resolved[0].SessionID != "s-planner" {
		t.Fatalf("resolved = %+v", rec.resolved)
	}
	if planOwnerClear(nil) != nil {
		t.Fatal("nil notifier must disable the clear")
	}
}
