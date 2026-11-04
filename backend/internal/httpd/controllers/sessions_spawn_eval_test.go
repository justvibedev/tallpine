package controllers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
)

type evalSet map[domain.ProjectID]bool

func (e evalSet) EvalProject(_ context.Context, p domain.ProjectID) bool { return e[p] }

// An orchestrator may spawn a worker in an eval project its own project
// created, and in no other project; the spawned session is not its child.
func TestScopeAgentSpawn_EvalProject(t *testing.T) {
	ctx := context.Background()
	orch := requestscope.Agent{Role: requestscope.RoleOrchestrator, SessionID: "app-orch", ProjectID: "app", CrossProject: true, Eval: evalSet{"lab": true}}
	worker := requestscope.Agent{Role: requestscope.RoleWorker, SessionID: "app-1", ProjectID: "app", Eval: evalSet{"lab": true}}
	cases := []struct {
		name    string
		agent   requestscope.Agent
		project domain.ProjectID
		parent  domain.SessionID
		refused bool
		want    domain.SessionID // parent after scoping
	}{
		{"own project", orch, "app", "", false, "app-orch"},
		{"eval project created by the caller", orch, "lab", "", false, ""},
		{"another project (not eval or not created)", orch, "web", "", true, ""},
		{"eval project, foreign parent", orch, "lab", "someone-else", true, ""},
		{"a worker never reaches another project", worker, "lab", "", true, ""},
		{"cross-project policy off", requestscope.Agent{Role: requestscope.RoleOrchestrator, SessionID: "app-orch", ProjectID: "app", Eval: evalSet{"lab": true}}, "lab", "", true, ""},
	}
	for _, c := range cases {
		in := SpawnSessionRequest{ProjectID: c.project, Kind: domain.KindWorker, ParentSessionID: c.parent}
		reason := scopeAgentSpawn(ctx, c.agent, &in)
		if (reason != "") != c.refused {
			t.Errorf("%s: reason = %q, refused want %v", c.name, reason, c.refused)
			continue
		}
		if !c.refused && in.ParentSessionID != c.want {
			t.Errorf("%s: parent = %q, want %q", c.name, in.ParentSessionID, c.want)
		}
	}
}

// A session list shows an eval project's sessions to its creating orchestrator
// only; a worker and an orchestrator without the mark see just their own.
func TestVisibleToAgent_EvalProject(t *testing.T) {
	sessions := []domain.Session{{ID: "app-1", ProjectID: "app", Kind: domain.KindWorker}, {ID: "lab-1", ProjectID: "lab", Kind: domain.KindWorker}, {ID: "web-1", ProjectID: "web", Kind: domain.KindWorker}}
	ids := func(agent requestscope.Agent) []domain.SessionID {
		r := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(requestscope.WithAgent(context.Background(), agent))
		var out []domain.SessionID
		for _, s := range visibleToAgent(r, append([]domain.Session(nil), sessions...)) {
			out = append(out, s.ID)
		}
		return out
	}
	orch := requestscope.Agent{Role: requestscope.RoleOrchestrator, SessionID: "app-orch", ProjectID: "app", CrossProject: true, Eval: evalSet{"lab": true}}
	if got := ids(orch); len(got) != 2 || got[0] != "app-1" || got[1] != "lab-1" {
		t.Errorf("creating orchestrator sees %v, want app-1 and lab-1 (never web-1)", got)
	}
	worker := requestscope.Agent{Role: requestscope.RoleWorker, SessionID: "app-1", ProjectID: "app", Eval: evalSet{"lab": true}}
	if got := ids(worker); len(got) != 1 || got[0] != "app-1" {
		t.Errorf("worker sees %v, want only itself", got)
	}
}

// A session list is the caller's own project, or an eval project it created;
// asking for any other project is refused, not rewritten.
func TestScopeSessionListProject(t *testing.T) {
	ctx := context.Background()
	orch := requestscope.Agent{Role: requestscope.RoleOrchestrator, SessionID: "app-orch", ProjectID: "app", CrossProject: true, Eval: evalSet{"lab": true}}
	for _, c := range []struct {
		asked   domain.ProjectID
		want    domain.ProjectID
		allowed bool
	}{{"", "app", true}, {"app", "app", true}, {"lab", "lab", true}, {"web", "", false}} {
		got, ok := scopeSessionListProject(ctx, orch, c.asked)
		if got != c.want || ok != c.allowed {
			t.Errorf("asked %q = (%q, %v), want (%q, %v)", c.asked, got, ok, c.want, c.allowed)
		}
	}
	worker := requestscope.Agent{Role: requestscope.RoleWorker, SessionID: "app-1", ProjectID: "app", Eval: evalSet{"lab": true}}
	if _, ok := scopeSessionListProject(ctx, worker, "lab"); ok {
		t.Error("a worker listed an eval project")
	}
}
