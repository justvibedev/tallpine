package controllers_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	backlogsvc "tallpine.eu/tallpine/backend/internal/service/backlog"
)

func TestOrchestratorLaneIsResolvedFromABacklogRef(t *testing.T) {
	srv, svc, backlog := newBacklogSpawnServer(t, func(b *backlogsvc.Service) controllers.BacklogSpawner { return b })
	ctx := context.Background()
	topic, err := backlog.Create(ctx, "tp", human(), backlogsvc.CreateInput{Title: "mobile"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := backlog.Create(ctx, "tp", human(), backlogsvc.CreateInput{Title: "push", ParentRef: topic.Ref()})
	if err != nil {
		t.Fatal(err)
	}

	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/orchestrators", `{"projectId":"tp","lane":"`+topic.Ref()+`"}`)
	if status != http.StatusCreated || svc.orchestratorLane != topic.ID {
		t.Fatalf("status=%d lane=%q, want 201 and item id %q; body=%s", status, svc.orchestratorLane, topic.ID, body)
	}
	body, status, _ = doRequest(t, srv, http.MethodPost, "/api/v1/orchestrators", `{"projectId":"tp","lane":"`+child.Ref()+`"}`)
	assertErrorCode(t, body, status, http.StatusConflict, "BACKLOG_LANE_NOT_TOP_LEVEL")
	body, status, _ = doRequest(t, srv, http.MethodPost, "/api/v1/orchestrators", `{"projectId":"tp","lane":"B999"}`)
	if status < 400 {
		t.Fatalf("unknown lane accepted: %d %s", status, body)
	}
	svc.orchestratorLane = "stale"
	if _, status, _ = doRequest(t, srv, http.MethodPost, "/api/v1/orchestrators", `{"projectId":"tp"}`); status != http.StatusCreated || svc.orchestratorLane != "" {
		t.Fatalf("default orchestrator carried lane %q (status %d)", svc.orchestratorLane, status)
	}
}

func TestSpawnWithBacklogPassesTheItemsParentAsLane(t *testing.T) {
	srv, svc, backlog := newBacklogSpawnServer(t, func(b *backlogsvc.Service) controllers.BacklogSpawner { return b })
	ctx := context.Background()
	topic, _ := backlog.Create(ctx, "tp", human(), backlogsvc.CreateInput{Title: "mobile"})
	child, err := backlog.Create(ctx, "tp", human(), backlogsvc.CreateInput{Title: "push", ParentRef: topic.Ref()})
	if err != nil {
		t.Fatal(err)
	}
	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions",
		`{"projectId":"tp","kind":"worker","harness":"codex","backlogRef":"`+child.Ref()+`"}`)
	if status != http.StatusCreated || svc.lastSpawn.LaneItemID != topic.ID {
		t.Fatalf("status=%d lane=%q, want the parent item id %q; body=%s", status, svc.lastSpawn.LaneItemID, topic.ID, body)
	}
}

// A dropped lane item cannot start an orchestrator, but a still-running one
// stays reachable, so `tallpine orchestrator new --lane X` can find it to end it.
func TestOrchestratorNewReturnsARunningOrchestratorOfAClosedLane(t *testing.T) {
	srv, svc, backlog := newBacklogSpawnServer(t, func(b *backlogsvc.Service) controllers.BacklogSpawner { return b })
	ctx := context.Background()
	topic, _ := backlog.Create(ctx, "tp", human(), backlogsvc.CreateInput{Title: "mobile"})
	if _, err := backlog.SetState(ctx, "tp", topic.ID, domain.BacklogDropped, ""); err != nil {
		t.Fatal(err)
	}
	req := `{"projectId":"tp","lane":"` + topic.Ref() + `"}`
	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/orchestrators", req)
	assertErrorCode(t, body, status, http.StatusConflict, "BACKLOG_LANE_CLOSED")

	svc.laneOrchestrator = domain.Session{SessionRecord: domain.SessionRecord{ID: "tp-orch-lane", ProjectID: "tp"}}
	body, status, _ = doRequest(t, srv, http.MethodPost, "/api/v1/orchestrators", req)
	if status != http.StatusOK || !strings.Contains(string(body), "tp-orch-lane") {
		t.Fatalf("status=%d body=%s, want the running orchestrator tp-orch-lane", status, body)
	}
}

func TestEndLaneResolvesTheLaneToItsOrchestrator(t *testing.T) {
	srv, svc, backlog := newBacklogSpawnServer(t, func(b *backlogsvc.Service) controllers.BacklogSpawner { return b })
	ctx := context.Background()
	topic, _ := backlog.Create(ctx, "tp", human(), backlogsvc.CreateInput{Title: "mobile"})
	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/orchestrators/end", `{"projectId":"tp","lane":"`+topic.Ref()+`"}`)
	assertErrorCode(t, body, status, http.StatusNotFound, "LANE_ORCHESTRATOR_NOT_FOUND")

	svc.laneOrchestrator = domain.Session{SessionRecord: domain.SessionRecord{ID: "tp-orch-lane", ProjectID: "tp"}}
	if body, status, _ = doRequest(t, srv, http.MethodPost, "/api/v1/orchestrators/end", `{"projectId":"tp","lane":"`+topic.Ref()+`"}`); status != http.StatusOK || svc.endLaneID != "tp-orch-lane" {
		t.Fatalf("by lane: status=%d ended=%q body=%s", status, svc.endLaneID, body)
	}
	svc.endLaneID = ""
	if _, status, _ = doRequest(t, srv, http.MethodPost, "/api/v1/orchestrators/end", `{"sessionId":"tp-9"}`); status != http.StatusOK || svc.endLaneID != "tp-9" {
		t.Fatalf("by session: status=%d ended=%q", status, svc.endLaneID)
	}
	body, status, _ = doRequest(t, srv, http.MethodPost, "/api/v1/orchestrators/end", `{}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "END_LANE_TARGET_REQUIRED")
}
