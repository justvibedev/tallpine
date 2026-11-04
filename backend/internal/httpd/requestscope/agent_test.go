package requestscope

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type evalSet map[domain.ProjectID]bool

func (e evalSet) EvalProject(_ context.Context, p domain.ProjectID) bool { return e[p] }

// EvalAccess is for an orchestrator with cross-project orchestration on, on a
// project that is not its own and is both created by it and marked eval.
func TestAgentEvalAccess(t *testing.T) {
	ctx := context.Background()
	base := Agent{Role: RoleOrchestrator, ProjectID: "app", CrossProject: true, Eval: evalSet{"lab": true}}
	cases := []struct {
		name    string
		agent   func(Agent) Agent
		project domain.ProjectID
		want    bool
	}{
		{"creator on an eval project", func(a Agent) Agent { return a }, "lab", true},
		{"not created or not marked", func(a Agent) Agent { return a }, "web", false},
		{"its own project", func(a Agent) Agent { a.Eval = evalSet{"app": true}; return a }, "app", false},
		{"empty project", func(a Agent) Agent { return a }, "", false},
		{"a worker even with the checker set", func(a Agent) Agent { a.Role = RoleWorker; return a }, "lab", false},
		{"a reviewer even with the checker set", func(a Agent) Agent { a.Role = RoleReviewer; return a }, "lab", false},
		{"cross-project policy off", func(a Agent) Agent { a.CrossProject = false; return a }, "lab", false},
		{"no checker", func(a Agent) Agent { a.Eval = nil; return a }, "lab", false},
	}
	for _, c := range cases {
		if got := c.agent(base).EvalAccess(ctx, c.project); got != c.want {
			t.Errorf("%s: EvalAccess(%q) = %v, want %v", c.name, c.project, got, c.want)
		}
	}
}
