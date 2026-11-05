package review

import (
	stdctx "context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// A native review launch takes the project's reviewer-role plugin and
// auto-memory overrides; an unset role leaves the launch unchanged.
func TestReviewLaunchSpecAppliesReviewerClaudeLaunch(t *testing.T) {
	ctx := stdctx.Background()
	worker := domain.SessionRecord{ID: "w1", ProjectID: "p1"}
	cfg := domain.ProjectConfig{
		ClaudePlugins:    map[string]map[string]bool{"reviewer": {"impeccable@x": false}},
		ClaudeAutoMemory: map[string]bool{"reviewer": false},
	}
	e := &Engine{projects: fakeProjects{cfg: cfg}}
	spec := e.reviewLaunchSpec(ctx, worker, domain.ReviewerClaudeCode, domain.AgentConfig{Model: "m"}, domain.ReviewRun{}, nil, "", "")
	if spec.AgentConfig.Launch.EnabledPlugins != `{"impeccable@x":false}` || !spec.AgentConfig.Launch.AutoMemoryOff || spec.AgentConfig.Model != "m" {
		t.Fatalf("reviewer launch = %+v", spec.AgentConfig)
	}
	e = &Engine{projects: fakeProjects{}}
	spec = e.reviewLaunchSpec(ctx, worker, domain.ReviewerClaudeCode, domain.AgentConfig{}, domain.ReviewRun{}, nil, "", "")
	if spec.AgentConfig.Launch.EnabledPlugins != "" || spec.AgentConfig.Launch.AutoMemoryOff {
		t.Fatalf("unset reviewer role changed the launch: %+v", spec.AgentConfig.Launch)
	}
}
