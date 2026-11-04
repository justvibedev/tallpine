package daemon

import (
	"context"
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	backlogsvc "tallpine.eu/tallpine/backend/internal/service/backlog"
	sessionsvc "tallpine.eu/tallpine/backend/internal/service/session"
)

type fakeRunSessions struct {
	orchestrators []domain.Session
	filter        sessionsvc.ListFilter
	sent          []string
	opts          []ports.MessageDeliveryOptions
}

func (f *fakeRunSessions) List(_ context.Context, filter sessionsvc.ListFilter) ([]domain.Session, error) {
	f.filter = filter
	return f.orchestrators, nil
}

func (f *fakeRunSessions) SendWithOptions(_ context.Context, id domain.SessionID, message string, _ *ports.SpawnAttachment, o ports.MessageDeliveryOptions) error {
	f.sent = append(f.sent, string(id)+": "+message)
	f.opts = append(f.opts, o)
	return nil
}

func TestBacklogRunNotifierMessagesTheLiveOrchestratorOnce(t *testing.T) {
	f := &fakeRunSessions{orchestrators: []domain.Session{{ID: "app-orch"}}}
	if err := (backlogRunNotifier{sessions: f, pick: fakePick("app-orch")}).NotifyOrchestrator(context.Background(), "app", "run R1 requested"); err != nil {
		t.Fatal(err)
	}
	if f.filter.ProjectID != "app" || !f.filter.OrchestratorOnly || f.filter.Active == nil || !*f.filter.Active {
		t.Fatalf("lookup filter = %+v, want the project's active orchestrator", f.filter)
	}
	if len(f.sent) != 1 || f.sent[0] != "app-orch: run R1 requested" || !f.opts[0].AuthoredByUser {
		t.Fatalf("sent = %v opts %+v, want one message authored by the owner", f.sent, f.opts)
	}
}

func TestBacklogRunNotifierReportsNoOrchestrator(t *testing.T) {
	f := &fakeRunSessions{}
	err := (backlogRunNotifier{sessions: f, pick: fakePick("")}).NotifyOrchestrator(context.Background(), "app", "x")
	if !errors.Is(err, backlogsvc.ErrNoOrchestrator) || len(f.sent) != 0 {
		t.Fatalf("err = %v sent %v, want ErrNoOrchestrator and nothing sent", err, f.sent)
	}
}

func fakePick(id domain.SessionID) func(context.Context, domain.ProjectID, string) (domain.SessionID, bool) {
	return func(context.Context, domain.ProjectID, string) (domain.SessionID, bool) { return id, id != "" }
}

// Two live orchestrators: an older unrelated one and the starter. A dead starter
// falls back to the main orchestrator from the routing rule, never found[0].
func TestPlanNoticeGoesToStarterNotFirstOrchestrator(t *testing.T) {
	f := &fakeRunSessions{orchestrators: []domain.Session{{ID: "unrelated"}, {ID: "starter"}}}
	n := backlogRunNotifier{sessions: f, pick: fakePick("main")}
	if err := n.NotifyPlanOrchestrator(context.Background(), "app", "starter", "approved"); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 1 || f.sent[0] != "starter: approved" || f.opts[0].AuthoredByUser {
		t.Fatalf("sent %v opts %+v, want the starter, not owner-authored", f.sent, f.opts)
	}
	if err := n.NotifyPlanOrchestrator(context.Background(), "app", "dead", "stale"); err != nil {
		t.Fatal(err)
	}
	if f.sent[1] != "main: stale" || f.opts[1].AuthoredByUser {
		t.Fatalf("fallback sent %v opts %+v", f.sent, f.opts)
	}
}

func TestNotifierWithoutPickerAnswersNoOrchestrator(t *testing.T) {
	f := &fakeRunSessions{orchestrators: []domain.Session{{ID: "other"}}}
	err := (backlogRunNotifier{sessions: f}).NotifyPlanOrchestrator(context.Background(), "app", "", "x")
	if !errors.Is(err, backlogsvc.ErrNoOrchestrator) || len(f.sent) != 0 {
		t.Fatalf("err = %v sent %v, want ErrNoOrchestrator and no panic", err, f.sent)
	}
}
