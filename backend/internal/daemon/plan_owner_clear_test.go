package daemon

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/notify"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

// B681, on the production path: a plan waiting on its answers stores one
// open plan_waiting row, and the clear the plan service runs once it is
// answered resolves exactly that row in the real store.
func TestPlanOwnerAlertThenClear_RealNotificationStore(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	mgr := notify.New(notify.Deps{Store: st})
	now := time.Now().UTC()
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "p1", Path: "/repo/p1", RegisteredAt: now}); err != nil {
		t.Fatal(err)
	}
	planner, err := st.CreateSession(ctx, domain.SessionRecord{ProjectID: "p1", Kind: domain.KindWorker, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	plan := domain.BacklogPlan{ID: "plan_1", ProjectID: "p1", PlannerSessionID: planner.ID, Status: domain.PlanAwaitingAnswers, UpdatedAt: time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)}

	alert := planOwnerAlert(mgr, nil, nil)
	alert(ctx, plan, "B45", "Ranker")
	alert(ctx, plan, "B45", "Ranker") // a repeat of the same state is not a second notification
	rows := listAll(t, st)
	if len(rows) != 1 || rows[0].Type != domain.NotificationPlanWaiting || rows[0].Resolved() {
		t.Fatalf("want one open plan_waiting row, got %+v", rows)
	}

	plan.Status = domain.PlanWriting
	planOwnerClear(mgr)(ctx, plan)
	rows = listAll(t, st)
	if len(rows) != 1 || !rows[0].Resolved() {
		t.Fatalf("answered plan must resolve its row, got %+v", rows)
	}
}

func listAll(t *testing.T, st interface {
	ListNotifications(context.Context, domain.NotificationListStatus, time.Time, string, int) ([]domain.NotificationRecord, error)
}) []domain.NotificationRecord {
	t.Helper()
	rows, err := st.ListNotifications(context.Background(), domain.NotificationListAll, time.Time{}, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// Review of #1172: one lane orchestrator's plans share its session, so clearing
// one plan must leave the lane's other waiting plan open. Runs on the real
// store, with the lane orchestrator long gone at clear time.
func TestPlanOwnerClear_LaneSiblingPlanStaysOpen(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	mgr := notify.New(notify.Deps{Store: st})
	now := time.Now().UTC()
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "p1", Path: "/repo/p1", RegisteredAt: now}); err != nil {
		t.Fatal(err)
	}
	mk := func() domain.SessionID {
		s, err := st.CreateSession(ctx, domain.SessionRecord{ProjectID: "p1", Kind: domain.KindWorker, CreatedAt: now, UpdatedAt: now})
		if err != nil {
			t.Fatal(err)
		}
		return s.ID
	}
	lane, pl58, pl59 := mk(), mk(), mk()
	ls := &leadSessions{orchs: []domain.Session{leadOrch(string(lane), "B1")}}
	at := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	p58 := domain.BacklogPlan{ID: "plan_58", ProjectID: "p1", PlannerSessionID: pl58, NotifySessionID: lane, Status: domain.PlanAwaitingApproval, UpdatedAt: at}
	p59 := domain.BacklogPlan{ID: "plan_59", ProjectID: "p1", PlannerSessionID: pl59, NotifySessionID: lane, Status: domain.PlanAwaitingApproval, UpdatedAt: at}
	alert := planOwnerAlert(mgr, ls, nil)
	alert(ctx, p58, "B58", "a")
	alert(ctx, p59, "B59", "b")
	ls.orchs = nil // the lane orchestrator ended while the plans wait
	p58.Status = domain.PlanApproved
	planOwnerClear(mgr)(ctx, p58)
	open := 0
	for _, r := range listAll(t, st) {
		if !r.Resolved() {
			open++
			if r.SessionID != lane || !r.LaneChat() {
				t.Fatalf("surviving row = %+v", r)
			}
		}
	}
	if open != 1 {
		t.Fatalf("want exactly B59's alert open, got %d open in %+v", open, listAll(t, st))
	}
}
