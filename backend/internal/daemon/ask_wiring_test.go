package daemon

import (
	"context"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type askOwner struct {
	recordingOwnerNotifier
}

func (a *askOwner) Resolve(_ context.Context, r ports.NotificationResolution) error {
	a.resolved = append(a.resolved, r)
	return nil
}

// The owner's card answer goes to whichever orchestrator is live for the
// ask's project and lane now, not the session that asked (it may be gone), as
// the owner's own message.
func TestAskRouterSendsToTheLiveOrchestratorOfTheLane(t *testing.T) {
	f := &fakeRunSessions{}
	var gotProject domain.ProjectID
	var gotLane string
	r := askRouter{sessions: f, pick: func(_ context.Context, p domain.ProjectID, lane string) (domain.SessionID, bool) {
		gotProject, gotLane = p, lane
		return "orch-after-restart", true
	}}
	if err := r.SendOwnerReply(context.Background(), "app", "B7", "Re Q3: Yes"); err != nil {
		t.Fatal(err)
	}
	if gotProject != "app" || gotLane != "B7" {
		t.Fatalf("picked for %s/%s", gotProject, gotLane)
	}
	if len(f.sent) != 1 || f.sent[0] != "orch-after-restart: Re Q3: Yes" || !f.opts[0].AuthoredByUser {
		t.Fatalf("sent %v opts %+v; want one owner-authored message to the live orchestrator", f.sent, f.opts)
	}
}

func TestAskRouterReportsNoLiveOrchestrator(t *testing.T) {
	f := &fakeRunSessions{}
	r := askRouter{sessions: f, pick: fakePick("")}
	if err := r.SendOwnerReply(context.Background(), "app", "", "Re Q1: x"); err == nil || len(f.sent) != 0 {
		t.Fatalf("err %v sent %v", err, f.sent)
	}
}

func TestAskNotifierOpensOneCriticalNotificationAndResolvesIt(t *testing.T) {
	o := &askOwner{}
	n := askNotifier{owner: o}
	n.Opened(context.Background(), domain.OwnerAsk{ID: "id1", Ref: "Q1", ProjectID: "p1", SessionID: "orch", Text: "Ship it?"})
	n.Opened(context.Background(), domain.OwnerAsk{ID: "id2", Ref: "Q2", ProjectID: "p1", SessionID: "lane-orch", Lane: "B7", Text: strings.Repeat("x", 300)})
	if len(o.got) != 2 {
		t.Fatalf("got %d notifications", len(o.got))
	}
	main, lane := o.got[0], o.got[1]
	if main.Type != domain.NotificationPlanWaiting || main.SessionID != "orch" || main.SourceKey != "ask-waiting:id1" || !strings.Contains(main.Title, "Q1") {
		t.Fatalf("main = %+v", main)
	}
	if lane.SourceKey != "ask-waiting-lane:id2" || len([]rune(lane.Body)) > 201 {
		t.Fatalf("lane = %+v", lane)
	}
	rec := domain.NotificationRecord{Type: domain.NotificationPlanWaiting, SourceKey: lane.SourceKey}
	if !rec.IsAsk() || !rec.LaneChat() {
		t.Fatal("lane ask notification must open the lane chat")
	}
	if (domain.NotificationRecord{Type: domain.NotificationPlanWaiting, SourceKey: main.SourceKey}).LaneChat() {
		t.Fatal("a main-chat ask is not a lane chat")
	}
	n.Closed(context.Background(), domain.OwnerAsk{ID: "id1"})
	if len(o.resolved) != 2 || o.resolved[0].SourceKeyPrefix != "ask-waiting:id1" {
		t.Fatalf("resolved = %+v", o.resolved)
	}
}
