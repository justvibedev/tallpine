package review

import (
	"context"
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type appDefaultProjects struct {
	fakeProjects
	app domain.WorkflowDefaults
	err error
}

func (p appDefaultProjects) WorkflowDefaults(context.Context) (domain.WorkflowDefaults, error) {
	return p.app, p.err
}

func TestProjectInheritsAppDefaultReviewer(t *testing.T) {
	app := domain.WorkflowDefaults{ReviewerHarness: domain.ReviewerCodex, ReviewerModel: "app-model"}
	cases := []struct {
		name        string
		cfg         domain.ProjectConfig
		wantHarness domain.ReviewerHarness
		wantModel   string
	}{
		{"no project reviewer uses the app default", domain.ProjectConfig{}, domain.ReviewerCodex, "app-model"},
		{"explicit project reviewer overrides", domain.ProjectConfig{Reviewers: []domain.ReviewerConfig{{Harness: domain.ReviewerClaudeCode, AgentConfig: domain.AgentConfig{Model: "own"}}}}, domain.ReviewerClaudeCode, "own"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng := newEngineForTest(&queueStore{}, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), appDefaultProjects{fakeProjects{cfg: tc.cfg}, app, nil}, &fakeLauncher{})
			harness, cfg, err := eng.selectReviewer(context.Background(), liveWorker())
			if err != nil {
				t.Fatal(err)
			}
			if harness != tc.wantHarness || cfg.Model != tc.wantModel {
				t.Fatalf("got %s/%q, want %s/%q", harness, cfg.Model, tc.wantHarness, tc.wantModel)
			}
		})
	}
}

// A failed read of the app defaults is an error, not a silent fall back to the
// worker-derived reviewer.
func TestSelectReviewerFailsWhenAppDefaultsUnreadable(t *testing.T) {
	boom := errors.New("read failed")
	eng := newEngineForTest(&queueStore{}, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), appDefaultProjects{fakeProjects{}, domain.WorkflowDefaults{}, boom}, &fakeLauncher{})
	if _, _, err := eng.selectReviewer(context.Background(), liveWorker()); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the read error", err)
	}
}
