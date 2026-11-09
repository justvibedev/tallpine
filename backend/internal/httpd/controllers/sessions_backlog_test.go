package controllers_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	backlogsvc "tallpine.eu/tallpine/backend/internal/service/backlog"
	sessionsvc "tallpine.eu/tallpine/backend/internal/service/session"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

type failingLinker struct{ *backlogsvc.Service }

func (failingLinker) LinkSession(context.Context, string, domain.SessionID, string) error {
	return errors.New("db is down")
}

func newBacklogSpawnServer(t *testing.T, spawner func(*backlogsvc.Service) controllers.BacklogSpawner) (*httptest.Server, *fakeSessionService, *backlogsvc.Service) {
	t.Helper()
	s := sqlitetest.MustOpen(t)
	if err := s.UpsertProject(context.Background(), domain.ProjectRecord{ID: "tp", Path: "/tmp/tallpine", RegisteredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	backlog := backlogsvc.New(backlogsvc.Deps{Store: s})
	svc := newFakeSessionService()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	deps := httpd.APIDeps{Sessions: svc, BacklogSpawner: spawner(backlog)}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{AllowedOrigins: []string{"app://renderer"}}, log, nil, deps, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv, svc, backlog
}

func human() backlogsvc.Actor { return backlogsvc.Actor{Kind: domain.BacklogOriginHuman} }

func TestSpawnWithBacklogLinksSessionAndAddsTaskContext(t *testing.T) {
	srv, svc, backlog := newBacklogSpawnServer(t, func(b *backlogsvc.Service) controllers.BacklogSpawner { return b })
	ctx := context.Background()
	blocker, _ := backlog.Create(ctx, "tp", human(), backlogsvc.CreateInput{Title: "blocker"})
	item, err := backlog.Create(ctx, "tp", human(), backlogsvc.CreateInput{Title: "Fix leak", Note: "see sessions.go", DoneWhen: "no leak", Scope: "pr", AgentNotes: "use the pool"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backlog.Link(ctx, "tp", item.Ref(), domain.BacklogLinkBlockedBy, blocker.Ref(), ""); err != nil {
		t.Fatal(err)
	}

	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions",
		`{"projectId":"tp","kind":"worker","harness":"codex","displayName":"leak","parentSessionId":"","backlogRef":"`+item.Ref()+`","backlogForce":true}`)
	if status != http.StatusCreated {
		t.Fatalf("spawn = %d: %s", status, body)
	}
	var res struct {
		Session  struct{ ID string }
		Warnings []string
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || res.Warnings[0] != item.Ref()+" is blocked by "+blocker.Ref() {
		t.Fatalf("warnings = %v", res.Warnings)
	}
	cfg := svc.lastSpawn
	if cfg.IssueID != "" {
		t.Fatalf("issue id = %q: the backlog link must not use issue_id", cfg.IssueID)
	}
	for _, want := range []string{"Fix leak", "see sessions.go", "no leak", "use the pool"} {
		if !strings.Contains(cfg.BacklogContext, want) {
			t.Errorf("backlog context missing %q: %s", want, cfg.BacklogContext)
		}
	}
	got, err := backlog.Get(ctx, "tp", item.ID)
	if err != nil || len(got.LinkViews) != 2 {
		t.Fatalf("links = %+v err=%v", got.LinkViews, err)
	}
	var session *backlogsvc.LinkView
	for i := range got.LinkViews {
		if got.LinkViews[i].Kind == domain.BacklogLinkSession {
			session = &got.LinkViews[i]
		}
	}
	if session == nil || session.Ref != res.Session.ID || session.CreatedBy != "human" {
		t.Fatalf("session link = %+v", session)
	}
}

func TestSpawnWithBacklogRejectsBeforeLaunching(t *testing.T) {
	srv, svc, backlog := newBacklogSpawnServer(t, func(b *backlogsvc.Service) controllers.BacklogSpawner { return b })
	ctx := context.Background()
	parent, _ := backlog.Create(ctx, "tp", human(), backlogsvc.CreateInput{Title: "parent"})
	if _, err := backlog.Create(ctx, "tp", human(), backlogsvc.CreateInput{Title: "child", ParentRef: parent.Ref()}); err != nil {
		t.Fatal(err)
	}
	held, _ := backlog.Create(ctx, "tp", human(), backlogsvc.CreateInput{Title: "held", Hold: "wait"})

	for name, tc := range map[string]struct {
		body string
		code string
	}{
		"parent":        {`{"projectId":"tp","backlogRef":"` + parent.Ref() + `","displayName":"x"}`, "BACKLOG_ITEM_IS_PARENT"},
		"held":          {`{"projectId":"tp","backlogRef":"` + held.Ref() + `","displayName":"x"}`, "BACKLOG_ITEM_HELD"},
		"unknown":       {`{"projectId":"tp","backlogRef":"B404","displayName":"x"}`, "BACKLOG_ITEM_NOT_FOUND"},
		"no project":    {`{"backlogRef":"B1","displayName":"x"}`, "BACKLOG_PROJECT_REQUIRED"},
		"other project": {`{"projectId":"web","backlogRef":"B1","displayName":"x"}`, ""},
	} {
		svc.lastSpawn.ProjectID = ""
		body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions", tc.body)
		if status < 400 {
			t.Errorf("%s: status %d: %s", name, status, body)
		}
		if tc.code != "" && !strings.Contains(string(body), tc.code) {
			t.Errorf("%s: body %s lacks %s", name, body, tc.code)
		}
		if svc.lastSpawn.ProjectID != "" {
			t.Errorf("%s: a session was launched for a rejected item", name)
		}
	}
	// With --force the held item starts.
	_, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions", `{"projectId":"tp","backlogRef":"`+held.Ref()+`","backlogForce":true,"displayName":"x"}`)
	if status != http.StatusCreated {
		t.Fatalf("forced held spawn = %d", status)
	}
}

func TestSpawnWithBacklogReportsLinkFailureAsWarning(t *testing.T) {
	srv, _, backlog := newBacklogSpawnServer(t, func(b *backlogsvc.Service) controllers.BacklogSpawner { return failingLinker{b} })
	item, _ := backlog.Create(context.Background(), "tp", human(), backlogsvc.CreateInput{Title: "x", Scope: "pr"})
	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions", `{"projectId":"tp","backlogRef":"`+item.Ref()+`","displayName":"x"}`)
	if status != http.StatusCreated || !strings.Contains(string(body), "could not be linked") {
		t.Fatalf("status=%d body=%s: the spawn must stand and carry a warning", status, body)
	}
}

// A replayed spawn (same clientRequestId) re-runs the link: an identical link
// already there is success, with no warning and no duplicate.
func TestSpawnWithBacklogReplayLinksIdempotently(t *testing.T) {
	srv, _, backlog := newBacklogSpawnServer(t, func(b *backlogsvc.Service) controllers.BacklogSpawner { return b })
	item, _ := backlog.Create(context.Background(), "tp", human(), backlogsvc.CreateInput{Title: "x", Scope: "pr"})
	req := `{"projectId":"tp","backlogRef":"` + item.Ref() + `","displayName":"x","clientRequestId":"req-1"}`
	for i := 0; i < 2; i++ {
		body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions", req)
		if status != http.StatusCreated || strings.Contains(string(body), "warnings") {
			t.Fatalf("spawn %d = %d: %s (no warning expected)", i, status, body)
		}
	}
	got, err := backlog.Get(context.Background(), "tp", item.ID)
	if err != nil || len(got.LinkViews) != 1 {
		t.Fatalf("links after replay = %+v err=%v", got.LinkViews, err)
	}
}

// The desktop's New Task path is delegate, not spawn: it must carry a backlog
// item the same way (context in the worker's task, session linked to the item).
func TestDelegateWithBacklogLinksWorkerAndAddsTaskContext(t *testing.T) {
	srv, svc, backlog := newBacklogSpawnServer(t, func(b *backlogsvc.Service) controllers.BacklogSpawner { return b })
	ctx := context.Background()
	blocker, _ := backlog.Create(ctx, "tp", human(), backlogsvc.CreateInput{Title: "blocker"})
	item, err := backlog.Create(ctx, "tp", human(), backlogsvc.CreateInput{Title: "Fix leak", Note: "see sessions.go", DoneWhen: "no leak", Scope: "pr", AgentNotes: "use the pool"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backlog.Link(ctx, "tp", item.Ref(), domain.BacklogLinkBlockedBy, blocker.Ref(), ""); err != nil {
		t.Fatal(err)
	}

	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/orchestrators/delegate",
		`{"projectId":"tp","brief":"`+item.Ref()+`: Fix leak","backlogRef":"`+item.Ref()+`","backlogForce":true}`)
	if status != http.StatusAccepted {
		t.Fatalf("delegate = %d: %s", status, body)
	}
	var res struct {
		WorkerID string
		Warnings []string
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || res.Warnings[0] != item.Ref()+" is blocked by "+blocker.Ref() {
		t.Fatalf("warnings = %v", res.Warnings)
	}
	for _, want := range []string{"Fix leak", "see sessions.go", "no leak", "use the pool"} {
		if !strings.Contains(svc.delegationInput.BacklogContext, want) {
			t.Errorf("backlog context missing %q: %s", want, svc.delegationInput.BacklogContext)
		}
	}
	got, err := backlog.Get(ctx, "tp", item.ID)
	if err != nil {
		t.Fatal(err)
	}
	linked := false
	for _, l := range got.LinkViews {
		linked = linked || (l.Kind == domain.BacklogLinkSession && l.Ref == res.WorkerID)
	}
	if !linked {
		t.Fatalf("links = %+v, want the delegated worker %q linked", got.LinkViews, res.WorkerID)
	}
}

func TestDelegateWithBacklogRejectsBeforeLaunching(t *testing.T) {
	srv, svc, backlog := newBacklogSpawnServer(t, func(b *backlogsvc.Service) controllers.BacklogSpawner { return b })
	ctx := context.Background()
	parent, _ := backlog.Create(ctx, "tp", human(), backlogsvc.CreateInput{Title: "parent"})
	if _, err := backlog.Create(ctx, "tp", human(), backlogsvc.CreateInput{Title: "child", ParentRef: parent.Ref()}); err != nil {
		t.Fatal(err)
	}
	held, _ := backlog.Create(ctx, "tp", human(), backlogsvc.CreateInput{Title: "held", Hold: "wait"})
	for name, tc := range map[string]struct{ ref, code string }{
		"parent":  {parent.Ref(), "BACKLOG_ITEM_IS_PARENT"},
		"held":    {held.Ref(), "BACKLOG_ITEM_HELD"},
		"unknown": {"B404", "BACKLOG_ITEM_NOT_FOUND"},
	} {
		svc.delegationInput = sessionsvc.DelegateTaskInput{}
		body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/orchestrators/delegate", `{"projectId":"tp","brief":"x","backlogRef":"`+tc.ref+`"}`)
		if status < 400 || !strings.Contains(string(body), tc.code) {
			t.Errorf("%s: status %d body %s, want %s", name, status, body, tc.code)
		}
		if svc.delegationInput.ProjectID != "" {
			t.Errorf("%s: a worker was delegated for a rejected item", name)
		}
	}
	_, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/orchestrators/delegate", `{"projectId":"tp","brief":"x","backlogRef":"`+held.Ref()+`","backlogForce":true}`)
	if status != http.StatusAccepted {
		t.Fatalf("forced held delegate = %d", status)
	}
}

func TestDelegateWithBacklogReportsLinkFailureAsWarning(t *testing.T) {
	srv, _, backlog := newBacklogSpawnServer(t, func(b *backlogsvc.Service) controllers.BacklogSpawner { return failingLinker{b} })
	item, _ := backlog.Create(context.Background(), "tp", human(), backlogsvc.CreateInput{Title: "x"})
	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/orchestrators/delegate", `{"projectId":"tp","brief":"x","backlogRef":"`+item.Ref()+`"}`)
	if status != http.StatusAccepted || !strings.Contains(string(body), "could not be linked") {
		t.Fatalf("status=%d body=%s: the delegation must stand and carry a warning", status, body)
	}
}

// With plan-in-place on and plan-first required, spawning on an item reserves a
// drafting plan and records the spawned session as its author (B335).
func TestSpawnWithBacklogPlanInPlaceAttachesAuthor(t *testing.T) {
	s := sqlitetest.MustOpen(t)
	ctx := context.Background()
	if err := s.UpsertProject(ctx, domain.ProjectRecord{ID: "tp", Path: "/tmp/tallpine", RegisteredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	backlog := backlogsvc.New(backlogsvc.Deps{Store: s})
	plans := backlogsvc.NewPlanService(backlogsvc.PlanDeps{
		Backlog: backlog, Store: s, Sessions: apiPlanSessions{store: s}, Enabled: true,
		Mode:    func(context.Context, domain.ProjectID) domain.PlanningMode { return domain.PlanningAlways },
		InPlace: func(context.Context, domain.ProjectID) bool { return true },
	})
	svc := newFakeSessionService()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{AllowedOrigins: []string{"app://renderer"}}, log, nil, httpd.APIDeps{Sessions: svc, BacklogSpawner: backlog}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	item, err := backlog.Create(ctx, "tp", human(), backlogsvc.CreateInput{Title: "Share seats", Scope: "pr"})
	if err != nil {
		t.Fatal(err)
	}

	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions",
		`{"projectId":"tp","kind":"worker","harness":"codex","displayName":"seats","backlogRef":"`+item.Ref()+`"}`)
	if status != http.StatusCreated {
		t.Fatalf("spawn = %d: %s", status, body)
	}
	var res struct{ Session struct{ ID string } }
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatal(err)
	}
	plan, ok, err := plans.Current(ctx, item.ID)
	if err != nil || !ok || plan.Status != domain.PlanDrafting || string(plan.PlannerSessionID) != res.Session.ID {
		t.Fatalf("plan = %+v ok=%v err=%v, want drafting with author %s", plan, ok, err, res.Session.ID)
	}
	if !strings.Contains(svc.lastSpawn.BacklogContext, "Plan in place") {
		t.Fatalf("task context lacks the in-place brief: %s", svc.lastSpawn.BacklogContext)
	}

	// --plan without a backlog item is a validation error.
	if _, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions", `{"projectId":"tp","kind":"worker","harness":"codex","backlogPlan":"in-place"}`); status != http.StatusBadRequest {
		t.Fatalf("backlogPlan without backlogRef = %d, want 400", status)
	}
}
