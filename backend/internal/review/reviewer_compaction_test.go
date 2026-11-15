package review

import (
	stdctx "context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type compactDefaultsFunc func(domain.AgentHarness) string

func (f compactDefaultsFunc) AutoCompactDefault(_ stdctx.Context, h domain.AgentHarness) string {
	return f(h)
}

// codexCatalog is a saved Codex catalog with one 272k model.
type codexCatalog struct{}

func (codexCatalog) Models(_ stdctx.Context, agentID, _ string, _ bool) (ports.AgentModelCatalog, error) {
	if agentID != string(domain.HarnessCodex) {
		return ports.AgentModelCatalog{}, nil
	}
	return ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: "gpt-x", ContextWindow: 272_000}}}, nil
}

// A Claude Code or Codex reviewer launches with the window a worker of its
// agent gets: the Settings default for that agent and the reviewer model's
// window, so the adapter resolves it (B734). Other reviewers are unchanged.
func TestReviewLaunchSpecCarriesTheWorkerCompactionWindow(t *testing.T) {
	ctx := stdctx.Background()
	worker := domain.SessionRecord{ID: "w1", ProjectID: "p1"}
	defaults := compactDefaultsFunc(func(h domain.AgentHarness) string {
		return map[domain.AgentHarness]string{domain.HarnessClaudeCode: "40%", domain.HarnessCodex: "20%"}[h]
	})
	e := &Engine{projects: fakeProjects{}, compactDefaults: defaults, modelCatalog: codexCatalog{}}
	for _, tt := range []struct {
		name    string
		harness domain.ReviewerHarness
		config  domain.AgentConfig
		window  string
		model   int64
		tokens  int
	}{
		{"claude code on opus", domain.ReviewerClaudeCode, domain.AgentConfig{Model: "opus"}, "40%", 1_000_000, 400_000},
		{"claude code on haiku leaves the agent default", domain.ReviewerClaudeCode, domain.AgentConfig{Model: "haiku"}, "40%", 200_000, 0},
		{"codex", domain.ReviewerCodex, domain.AgentConfig{Model: "gpt-x"}, "20%", 272_000, 200_000},
		{"a window the config carries is kept", domain.ReviewerClaudeCode, domain.AgentConfig{Model: "opus", Launch: domain.AgentLaunchOptions{AutoCompactWindow: "250k"}}, "250k", 1_000_000, 250_000},
		{"kiro has no window", domain.ReviewerKiro, domain.AgentConfig{Model: "opus"}, "", 0, 0},
	} {
		spec := e.reviewLaunchSpec(ctx, worker, tt.harness, tt.config, domain.ReviewRun{}, nil, "", "")
		launch := spec.AgentConfig.Launch
		if launch.AutoCompactWindow != tt.window || launch.ModelContextWindow != tt.model {
			t.Errorf("%s: launch window %q model %d, want %q and %d", tt.name, launch.AutoCompactWindow, launch.ModelContextWindow, tt.window, tt.model)
		}
		if got := domain.ResolveAutoCompactWindow(launch.AutoCompactWindow, launch.ModelContextWindow, 0); got != tt.tokens {
			t.Errorf("%s: resolves to %d, want %d", tt.name, got, tt.tokens)
		}
	}

	// No Settings default: the reviewer keeps its agent's own.
	e = &Engine{projects: fakeProjects{}, compactDefaults: compactDefaultsFunc(func(domain.AgentHarness) string { return "" })}
	if launch := e.reviewLaunchSpec(ctx, worker, domain.ReviewerClaudeCode, domain.AgentConfig{Model: "opus"}, domain.ReviewRun{}, nil, "", "").AgentConfig.Launch; launch.AutoCompactWindow != "" || launch.ModelContextWindow != 0 {
		t.Fatalf("no default changed the launch: %+v", launch)
	}
}

// reviewerBaselines is a store that measures every reviewer at one baseline.
type reviewerBaselines struct {
	Store
	baseline int64
	asked    []domain.SessionKind
}

func (b *reviewerBaselines) Baseline(_ stdctx.Context, _ domain.ProjectID, kind domain.SessionKind, _ domain.AgentHarness) (int64, error) {
	b.asked = append(b.asked, kind)
	return b.baseline, nil
}

// A reviewer's window keeps room above the measured baseline, read as a
// worker's (B735): an explicit 120k with a 66k baseline runs at 166k.
func TestReviewLaunchSpecFloorsTheWindowAtTheWorkerBaseline(t *testing.T) {
	ctx := stdctx.Background()
	worker := domain.SessionRecord{ID: "w1", ProjectID: "p1"}
	baselines := &reviewerBaselines{baseline: 66_000}
	defaults := compactDefaultsFunc(func(domain.AgentHarness) string { return "120k" })
	e := &Engine{projects: fakeProjects{}, compactDefaults: defaults, store: baselines, modelCatalog: codexCatalog{}}
	launch := e.reviewLaunchSpec(ctx, worker, domain.ReviewerCodex, domain.AgentConfig{Model: "gpt-x"}, domain.ReviewRun{}, nil, "", "").AgentConfig.Launch
	if launch.AutoCompactBaseline != 66_000 || len(baselines.asked) != 1 || baselines.asked[0] != domain.KindWorker {
		t.Fatalf("launch %+v asked %v, want baseline 66000 read once as a worker", launch, baselines.asked)
	}
	if got := domain.ResolveAutoCompactWindow(launch.AutoCompactWindow, launch.ModelContextWindow, launch.AutoCompactBaseline); got != 166_000 {
		t.Fatalf("resolves to %d, want 166000", got)
	}
}
