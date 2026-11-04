package controllers_test

import (
	"context"
	"net/http"
	"testing"

	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
)

// planInPlace is the one setting the creating orchestrator may write but the
// project's own orchestrator may not (B335 eval projects). These cases go
// through the real controllers, so they exercise the humanOnlyKeys gate, not
// just the scope middleware.
func TestPlanInPlace_CreatorOrchestratorOnly(t *testing.T) {
	s := newWriteServer(t, "", nil)
	for _, id := range []string{"own", "created"} {
		mustMkProject(t, s, id)
	}
	flag := func(project string) bool {
		t.Helper()
		rec, ok, err := s.store.GetProject(context.Background(), project)
		if err != nil || !ok {
			t.Fatalf("GetProject(%s) = %v, %v", project, ok, err)
		}
		return rec.Config.PlanInPlace
	}
	orch := s.as(t, requestscope.Agent{Role: requestscope.RoleOrchestrator, SessionID: "own-orch", ProjectID: "own", CrossProject: true})

	// The creating orchestrator turns it on through the config route.
	if raw, status, _ := doRequest(t, orch.srv, "PUT", "/api/v1/projects/created/config", `{"config":{"planInPlace":true}}`); status != http.StatusOK {
		t.Fatalf("creator PUT config = %d; %s", status, raw)
	}
	if !flag("created") {
		t.Fatal("the creator's write did not store planInPlace")
	}
	// ... and registers a project with it on.
	if raw, status, _ := doRequest(t, orch.srv, "POST", "/api/v1/projects", `{"path":`+quote(gitRepo(t, "fresh"))+`,"projectId":"fresh","config":{"planInPlace":true}}`); status != http.StatusCreated {
		t.Fatalf("creator POST /projects = %d; %s", status, raw)
	}
	if !flag("fresh") {
		t.Fatal("a registered project did not store planInPlace")
	}

	// The project's own orchestrator may not, by key.
	if _, raw, status := orch.patch(t, `{"project":"own","set":{"project.planInPlace":true}}`); status != http.StatusForbidden {
		t.Fatalf("own orchestrator PATCH = %d; %s", status, raw)
	}
	if flag("own") {
		t.Fatal("the own orchestrator changed planInPlace")
	}

	// A worker is refused by key here; its project routes are closed by the scope middleware (httpd TestPlanInPlaceConfigOnlyOnCreatedProjects).
	worker := s.as(t, requestscope.Agent{Role: requestscope.RoleWorker, SessionID: "own-1", ProjectID: "own"})
	if _, raw, status := worker.patch(t, `{"project":"created","set":{"project.planInPlace":false}}`); status != http.StatusForbidden {
		t.Fatalf("worker PATCH = %d; %s", status, raw)
	}
}

func mustMkProject(t *testing.T, s writeServer, id string) {
	t.Helper()
	mustStatus(t, s.srv, "POST", "/api/v1/projects", `{"path":`+quote(gitRepo(t, id))+`,"projectId":"`+id+`"}`, http.StatusCreated)
}

// eval marks a throwaway project the creating orchestrator may drive; like
// planInPlace only the user and the creating orchestrator write it.
func TestEvalMark_CreatorOrchestratorOnly(t *testing.T) {
	s := newWriteServer(t, "", nil)
	for _, id := range []string{"own", "created"} {
		mustMkProject(t, s, id)
	}
	marked := func(project string) bool {
		t.Helper()
		rec, ok, err := s.store.GetProject(context.Background(), project)
		if err != nil || !ok {
			t.Fatalf("GetProject(%s) = %v, %v", project, ok, err)
		}
		return rec.Config.Eval
	}
	orch := s.as(t, requestscope.Agent{Role: requestscope.RoleOrchestrator, SessionID: "own-orch", ProjectID: "own", CrossProject: true})
	if raw, status, _ := doRequest(t, orch.srv, "PUT", "/api/v1/projects/created/config", `{"config":{"eval":true}}`); status != http.StatusOK {
		t.Fatalf("creator PUT config = %d; %s", status, raw)
	}
	if !marked("created") {
		t.Fatal("the creator's write did not store the eval mark")
	}
	if _, raw, status := orch.patch(t, `{"project":"own","set":{"project.eval":true}}`); status != http.StatusForbidden {
		t.Fatalf("own orchestrator PATCH eval = %d; %s", status, raw)
	}
	if marked("own") {
		t.Fatal("the own orchestrator marked its own project eval")
	}
	worker := s.as(t, requestscope.Agent{Role: requestscope.RoleWorker, SessionID: "own-1", ProjectID: "own"})
	if _, raw, status := worker.patch(t, `{"project":"created","set":{"project.eval":false}}`); status != http.StatusForbidden {
		t.Fatalf("worker PATCH eval = %d; %s", status, raw)
	}
	if !marked("created") {
		t.Fatal("a worker cleared the mark")
	}
}
