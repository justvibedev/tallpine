package push

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestMessageForLanePlanWaitingFlagsLaneChat(t *testing.T) {
	rec := domain.NotificationRecord{ID: "n1", Type: domain.NotificationPlanWaiting, SessionID: "lane1", ProjectID: "p1", SourceKey: domain.PlanLaneSourcePrefix + "plan_1"}
	m := messageFor(rec, "ExponentPushToken[p]", "h_a", "Host", "Lane")
	if m.Data["laneChat"] != true || m.Data["sessionId"] != "lane1" {
		t.Fatalf("lane data = %+v", m.Data)
	}
	rec.SourceKey = "plan-waiting:plan_1"
	if _, ok := messageFor(rec, "t", "h_a", "Host", "Planner").Data["laneChat"]; ok {
		t.Fatal("a main/planner alert must not carry laneChat")
	}
}
