package controllers_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	backlogsvc "tallpine.eu/tallpine/backend/internal/service/backlog"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

type apiPlanSessions struct{ store *sqlite.Store }

func (f apiPlanSessions) SpawnPlanner(ctx context.Context, in backlogsvc.PlannerSpawn) (domain.SessionID, error) {
	now := time.Now().UTC()
	rec, err := f.store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: in.ProjectID, Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Role: domain.SessionRolePlanner,
		Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
		Metadata: domain.SessionMetadata{Branch: "plan/x", WorkspacePath: "/ws"}, CreatedAt: now, UpdatedAt: now,
	})
	return rec.ID, err
}

func (f apiPlanSessions) SpawnInPlace(ctx context.Context, in backlogsvc.PlannerSpawn) (domain.SessionID, error) {
	now := time.Now().UTC()
	rec, err := f.store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: in.ProjectID, Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
		Metadata: domain.SessionMetadata{Branch: "b/x", WorkspacePath: "/ws"}, CreatedAt: now, UpdatedAt: now,
	})
	return rec.ID, err
}

func (apiPlanSessions) TerminatePlanner(context.Context, domain.SessionID) error { return nil }

func (apiPlanSessions) PlannerLive(context.Context, domain.SessionID) bool { return true }

func (apiPlanSessions) SendToPlanner(context.Context, domain.SessionID, string) (bool, error) {
	return true, nil
}

func newPlanAPI(t *testing.T, enabled bool) *backlogAPI {
	t.Helper()
	return newPlanAPIWithLimits(t, enabled, nil)
}

func newPlanAPIWithLimits(t *testing.T, enabled bool, limits controllers.UsageLimitReader) *backlogAPI {
	t.Helper()
	s := sqlitetest.MustOpen(t)
	if err := s.UpsertProject(context.Background(), domain.ProjectRecord{ID: "p1", Path: "/tmp/p1", RegisteredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	bl := backlogsvc.New(backlogsvc.Deps{Store: s})
	plans := backlogsvc.NewPlanService(backlogsvc.PlanDeps{Backlog: bl, Store: s, Sessions: apiPlanSessions{store: s}, Enabled: enabled})
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if h := req.Header.Get("X-Test-Agent"); h != "" {
				role, sess, _ := strings.Cut(h, ":")
				req = req.WithContext(requestscope.WithAgent(req.Context(), requestscope.Agent{Role: requestscope.AgentRole(role), SessionID: domain.SessionID(sess), ProjectID: "p1"}))
			}
			next.ServeHTTP(w, req)
		})
	})
	r.Route("/api/v1", func(r chi.Router) {
		(&controllers.BacklogController{Svc: bl}).Register(r)
		(&controllers.BacklogPlanController{Svc: plans, UsageLimits: limits}).Register(r)
	})
	return &backlogAPI{t: t, handler: r, store: s}
}

func TestPlanAPI_404WhenSwitchOff(t *testing.T) {
	a := newPlanAPI(t, false)
	base := "/projects/p1/backlog"
	a.must(201, "POST", base, "", map[string]any{"title": "first"})
	for _, rt := range []struct{ method, path string }{
		{"GET", base + "/B1/plan"}, {"POST", base + "/B1/plan"}, {"POST", base + "/B1/plan/replan"},
		{"POST", base + "/B1/plan/questions"}, {"POST", base + "/B1/plan/submit"}, {"POST", base + "/B1/plan/answers"},
		{"POST", base + "/B1/plan/defaults"}, {"POST", base + "/B1/plan/approve"}, {"POST", base + "/B1/plan/build-anyway"},
	} {
		code, out := a.do(rt.method, rt.path, "orchestrator:o1", map[string]any{})
		if code != 404 || !strings.Contains(string(runBody(t, out)), "PLANNER_DISABLED") {
			t.Errorf("%s %s = %d %v, want 404 PLANNER_DISABLED", rt.method, rt.path, code, out)
		}
	}
}

func TestPlanAPI_RoundTrip(t *testing.T) {
	a := newPlanAPI(t, true)
	base := "/projects/p1/backlog"
	a.must(201, "POST", base, "", map[string]any{"title": "first", "open": true})
	plan := a.must(201, "POST", base+"/B1/plan", "", nil)["plan"].(map[string]any)
	if plan["status"] != "drafting" || plan["plannerSessionId"] == "" {
		t.Fatalf("started plan = %v", plan)
	}
	planner := "worker:" + plan["plannerSessionId"].(string)
	// Another worker cannot submit; the planner can.
	q := map[string]any{
		"questions":   []any{map[string]any{"id": "q1", "text": "Who pays?", "options": []any{map[string]any{"id": "a", "label": "Each"}, map[string]any{"id": "b", "label": "Booker"}}, "recommended": "a"}},
		"assumptions": []any{map[string]any{"id": "s1", "text": "Assumed: 10 minutes"}},
	}
	a.must(403, "POST", base+"/B1/plan/questions", "worker:other", q)
	got := a.must(200, "POST", base+"/B1/plan/questions", planner, q)["plan"].(map[string]any)
	if got["status"] != "awaiting_answers" || len(got["questions"].([]any)) != 1 {
		t.Fatalf("after questions = %v", got)
	}
	a.must(200, "POST", base+"/B1/plan/defaults", "orchestrator:o1", map[string]any{})
	sub := map[string]any{"plan": "# Plan", "decisions": "| q | a |", "prs": []any{map[string]any{"title": "PR1", "produces": []any{"X()"}}}, "focus": []any{"harm"}}
	a.must(200, "POST", base+"/B1/plan/submit", planner, sub)
	// An orchestrator cannot approve without the owner's message; the owner can.
	a.must(403, "POST", base+"/B1/plan/approve", "orchestrator:o1", map[string]any{})
	done := a.must(200, "POST", base+"/B1/plan/approve", "", map[string]any{})["plan"].(map[string]any)
	if done["status"] != "approved" || done["approvedBy"] != "user" {
		t.Fatalf("approved = %v", done)
	}
	read := a.must(200, "GET", base+"/B1/plan", "worker:w9", nil)["plan"].(map[string]any)
	if read["decisionsMarkdown"] != "| q | a |" {
		t.Fatalf("read = %v", read)
	}
}

type pausedPlanner struct {
	until time.Time
	on    map[domain.SessionID]bool
}

func (p pausedPlanner) UsageLimitPausedUntil(_ context.Context, id domain.SessionID) (time.Time, bool) {
	return p.until, p.on[id]
}

// A planner stopped on a provider usage limit shows the wait, with the reset
// time, while the plan is still being drafted or written; once the plan waits
// on the owner the pause is not its status.
func TestPlanAPI_PlannerUsageLimitShowsPausedUntil(t *testing.T) {
	until := time.Date(2026, 11, 2, 6, 20, 0, 0, time.UTC)
	limits := pausedPlanner{until: until, on: map[domain.SessionID]bool{}}
	a := newPlanAPIWithLimits(t, true, limits)
	base := "/projects/p1/backlog"
	a.must(201, "POST", base, "", map[string]any{"title": "first", "open": true})
	plan := a.must(201, "POST", base+"/B1/plan", "", nil)["plan"].(map[string]any)
	sid := domain.SessionID(plan["plannerSessionId"].(string))
	if _, ok := plan["pausedUntil"]; ok {
		t.Fatalf("a planner that is not limited must not report a pause: %v", plan)
	}
	limits.on[sid] = true
	drafting := a.must(200, "GET", base+"/B1/plan", "", nil)["plan"].(map[string]any)
	if drafting["pausedUntil"] != until.Format(time.RFC3339) {
		t.Fatalf("drafting pausedUntil = %v, want %v", drafting["pausedUntil"], until)
	}
	q := map[string]any{
		"questions":   []any{map[string]any{"id": "q1", "text": "Who pays?", "options": []any{map[string]any{"id": "a", "label": "Each"}}, "recommended": "a"}},
		"assumptions": []any{},
	}
	a.must(200, "POST", base+"/B1/plan/questions", "worker:"+string(sid), q)
	awaiting := a.must(200, "GET", base+"/B1/plan", "", nil)["plan"].(map[string]any)
	if _, ok := awaiting["pausedUntil"]; ok || awaiting["status"] != "awaiting_answers" {
		t.Fatalf("a plan waiting on the owner must not show the planner's pause: %v", awaiting)
	}
	a.must(200, "POST", base+"/B1/plan/defaults", "", map[string]any{})
	writing := a.must(200, "GET", base+"/B1/plan", "", nil)["plan"].(map[string]any)
	if writing["status"] != "writing" || writing["pausedUntil"] != until.Format(time.RFC3339) {
		t.Fatalf("writing plan = %v, want pausedUntil %v", writing, until)
	}
	list := a.must(200, "GET", "/projects/p1/plans", "", nil)["plans"].([]any)
	if got := list[0].(map[string]any)["plan"].(map[string]any)["pausedUntil"]; got != until.Format(time.RFC3339) {
		t.Fatalf("listed plan pausedUntil = %v", got)
	}
}
