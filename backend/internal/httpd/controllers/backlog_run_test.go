package controllers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	backlogsvc "tallpine.eu/tallpine/backend/internal/service/backlog"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

type countingNotifier struct{ n int }

func (c *countingNotifier) NotifyOrchestrator(context.Context, domain.ProjectID, string) error {
	c.n++
	return nil
}

func newBacklogRunAPI(t *testing.T, enabled bool) (*backlogAPI, *countingNotifier) {
	t.Helper()
	s := sqlitetest.MustOpen(t)
	if err := s.UpsertProject(context.Background(), domain.ProjectRecord{ID: "p1", Path: "/tmp/p1", RegisteredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	n := &countingNotifier{}
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
		(&controllers.BacklogController{Svc: backlogsvc.New(backlogsvc.Deps{Store: s})}).Register(r)
		(&controllers.BacklogRunController{Svc: backlogsvc.NewRunService(backlogsvc.RunDeps{Store: s, Notify: n, Enabled: enabled})}).Register(r)
	})
	return &backlogAPI{t: t, handler: r, store: s}, n
}

func TestBacklogRunAPIRoundTrip(t *testing.T) {
	a, notify := newBacklogRunAPI(t, true)
	base := "/projects/p1/backlog"
	a.must(201, "POST", base, "", map[string]any{"title": "first"})
	a.must(201, "POST", base, "", map[string]any{"title": "second"})

	run := a.must(201, "POST", base+"-runs", "", map[string]any{"scope": "local", "selection": "all"})["run"].(map[string]any)
	if run["scope"] != "local" || run["ref"] != "R1" || run["status"] != "planning" || run["requestedBy"] != "human" || notify.n != 1 {
		t.Fatalf("run = %v, notifications %d (want one for the owner's request)", run, notify.n)
	}
	a.must(409, "POST", base+"-runs", "orchestrator:o1", map[string]any{"scope": "pr", "selection": "all"})

	plan := map[string]any{"lanes": []any{[]any{map[string]any{"ref": "B1", "model": "opus"}}, []any{map[string]any{"ref": "B2", "model": "sonnet"}}}}
	a.must(403, "PUT", base+"-runs/R1/plan", "", plan)
	a.must(403, "PUT", base+"-runs/R1/plan", "worker:w1", plan)
	got := a.must(200, "PUT", base+"-runs/R1/plan", "orchestrator:o1", plan)["run"].(map[string]any)
	if got["status"] != "running" || len(got["items"].([]any)) != 2 {
		t.Fatalf("after plan: %v", got)
	}
	a.must(200, "POST", base+"-runs/R1/items/B1", "orchestrator:o1", map[string]any{"status": "active"})
	a.must(400, "POST", base+"-runs/R1/items/B2", "orchestrator:o1", map[string]any{"status": "needs_you"}) // no question
	a.must(200, "POST", base+"-runs/R1/items/B2", "orchestrator:o1", map[string]any{"status": "needs_you", "question": "Which API?"})

	pause := a.must(200, "POST", base+"-runs/R1/pause", "", map[string]any{"reason": "usage limit"})["run"].(map[string]any)
	if pause["status"] != "paused" || pause["pauseReason"] != "usage limit" || notify.n != 2 {
		t.Fatalf("pause = %v, notifications %d (want the owner's click to nudge once)", pause, notify.n)
	}
	a.must(200, "POST", base+"-runs/R1/resume", "", nil)
	a.must(200, "POST", base+"-runs/R1/stop", "", nil)
	a.must(409, "POST", base+"-runs/R1/stop", "", nil)
	a.must(200, "POST", base+"-runs/R1/items/B1", "orchestrator:o1", map[string]any{"status": "landed"})
	done := a.must(200, "POST", base+"-runs/R1/finish", "orchestrator:o1", map[string]any{"summary": "B1 landed, B2 waits on you"})["run"].(map[string]any)
	if done["status"] != "done" || done["summary"] != "B1 landed, B2 waits on you" || done["endedAt"] == nil {
		t.Fatalf("finished = %v", done)
	}
	counts := done["counts"].(map[string]any)
	if counts["landed"] != float64(1) || counts["needs_you"] != float64(1) {
		t.Fatalf("counts = %v", counts)
	}
	list := a.must(200, "GET", base+"-runs", "", nil)["runs"].([]any)
	if len(list) != 1 || len(a.must(200, "GET", base+"-runs?open=1", "", nil)["runs"].([]any)) != 0 {
		t.Fatalf("runs list = %v", list)
	}
	// The item closing on its own (lifecycle) overrules the stored status in what the page shows.
	a.must(200, "POST", base+"/B2/state", "", map[string]any{"state": "done"})
	for _, it := range a.must(200, "GET", base+"-runs/R1", "", nil)["run"].(map[string]any)["items"].([]any) {
		it := it.(map[string]any)
		if it["ref"] == "B2" && (it["status"] != "needs_you" || it["displayStatus"] != "landed") {
			t.Fatalf("B2 = %v, want stored needs_you but shown landed", it)
		}
	}
	a.must(404, "GET", base+"-runs/R9", "", nil)
	if usage := a.must(200, "GET", base+"/B1/usage", "orchestrator:o1", nil)["usage"].(map[string]any); usage["costComplete"] != false {
		t.Fatalf("usage = %v", usage)
	}
}

func TestBacklogRunAPIIsOffUnlessSwitched(t *testing.T) {
	a, notify := newBacklogRunAPI(t, false)
	base := "/projects/p1/backlog"
	a.must(201, "POST", base, "", map[string]any{"title": "first"})
	routes := []struct{ method, path string }{
		{"GET", base + "-runs"}, {"POST", base + "-runs"}, {"GET", base + "-runs/R1"}, {"PUT", base + "-runs/R1/plan"},
		{"POST", base + "-runs/R1/pause"}, {"POST", base + "-runs/R1/resume"}, {"POST", base + "-runs/R1/stop"},
		{"POST", base + "-runs/R1/finish"}, {"POST", base + "-runs/R1/items/B1"}, {"GET", base + "/B1/usage"},
	}
	for _, rt := range routes {
		code, out := a.do(rt.method, rt.path, "orchestrator:o1", map[string]any{"scope": "pr", "selection": "all"})
		if code != 404 || !strings.Contains(string(runBody(t, out)), "BACKLOG_RUN_DISABLED") {
			t.Errorf("%s %s = %d %v, want 404 BACKLOG_RUN_DISABLED", rt.method, rt.path, code, out)
		}
	}
	if notify.n != 0 {
		t.Fatalf("switch off still messaged the orchestrator %d times", notify.n)
	}
}

func runBody(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
