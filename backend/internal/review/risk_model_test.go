package review

import (
	"context"
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// riskPaths are the globs the tests below use; the built-in default is only migrations.
var riskPaths = []string{"backend/internal/service/{chat,report,turnnotify,review}/**", "backend/internal/storage/**", "backend/internal/httpd/**", "**/migrations/**"}

func triggerWithChanged(t *testing.T, cfg domain.ProjectConfig, changed []string, override domain.AgentConfig) (*fakeLauncher, domain.ReviewRun) {
	t.Helper()
	return triggerWithChangedLauncher(t, cfg, changed, &fakeLauncher{handle: "review-mer-1"}, override)
}

// notInstalled fails Preflight for one reviewer only, as a missing binary would.
type notInstalled struct {
	*fakeLauncher
	harness domain.ReviewerHarness
}

func (n notInstalled) Preflight(ctx context.Context, id domain.SessionID, h domain.ReviewerHarness, ws string, m ...domain.ReviewerInterfaceMode) error {
	if h == n.harness {
		return errors.New(string(h) + " not installed")
	}
	return n.fakeLauncher.Preflight(ctx, id, h, ws, m...)
}

func triggerWithChangedLauncher(t *testing.T, cfg domain.ProjectConfig, changed []string, launcher Launcher, overrides ...domain.AgentConfig) (*fakeLauncher, domain.ReviewRun) {
	t.Helper()
	var override domain.AgentConfig
	if len(overrides) > 0 {
		override = overrides[0]
	}
	store := &fakeStore{}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{cfg: cfg}, launcher)
	eng.changedPaths = func(context.Context, string, domain.PullRequest) ([]string, error) { return changed, nil }
	res, err := trigger(context.Background(), eng, "mer-1", "", override)
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if n, ok := launcher.(notInstalled); ok {
		return n.fakeLauncher, res.Run
	}
	return launcher.(*fakeLauncher), res.Run
}

func TestRiskyPathSelectsOpusAndRecordsReason(t *testing.T) {
	cfg := domain.ProjectConfig{ReviewRisk: domain.ReviewRiskConfig{Paths: riskPaths}, Reviewers: []domain.ReviewerConfig{{Harness: domain.ReviewerClaudeCode, AgentConfig: domain.AgentConfig{Model: "claude-sonnet-5-5"}}}}
	for _, tc := range []struct {
		name    string
		cfg     domain.ProjectConfig
		changed []string
		want    string
	}{
		{"chat service", cfg, []string{"README.md", "backend/internal/service/chat/x.go"}, "claude-opus-5-5"},
		{"migration anywhere", cfg, []string{"x/y/migrations/1.sql"}, "claude-opus-5-5"},
		{"storage", cfg, []string{"backend/internal/storage/sqlite/a.go"}, "claude-opus-5-5"},
		{"safe path keeps default", cfg, []string{"frontend/src/a.ts", "backend/internal/service/other/a.go"}, "claude-sonnet-5-5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			launcher, run := triggerWithChanged(t, tc.cfg, tc.changed, domain.AgentConfig{})
			if run.Model != tc.want || launcher.gotSpec.AgentConfig.Model != tc.want {
				t.Fatalf("run model %q, launched %q, want %q", run.Model, launcher.gotSpec.AgentConfig.Model, tc.want)
			}
			if (tc.want == "claude-opus-5-5") != (run.ModelReason != "") {
				t.Fatalf("model reason = %q for model %q", run.ModelReason, run.Model)
			}
		})
	}
}

func TestRiskConfigOverridesGlobsAndModel(t *testing.T) {
	cfg := domain.ProjectConfig{ReviewRisk: domain.ReviewRiskConfig{Paths: []string{"docs/**"}, Model: "claude-fable-5-1"}}
	if _, run := triggerWithChanged(t, cfg, []string{"docs/a.md"}, domain.AgentConfig{}); run.Model != "claude-fable-5-1" {
		t.Fatalf("custom glob model = %q", run.Model)
	}
	if _, run := triggerWithChanged(t, cfg, []string{"backend/internal/storage/a.go"}, domain.AgentConfig{}); run.Model == "claude-opus-5-5" || run.ModelReason != "" {
		t.Fatalf("default globs still applied: %q %q", run.Model, run.ModelReason)
	}
}

func TestExplicitModelBeatsRiskSelection(t *testing.T) {
	_, run := triggerWithChanged(t, domain.ProjectConfig{ReviewRisk: domain.ReviewRiskConfig{Paths: riskPaths}}, []string{"backend/internal/httpd/a.go"}, domain.AgentConfig{Model: "claude-haiku-5-5"})
	if run.Model != "claude-haiku-5-5" || run.ModelReason != "" {
		t.Fatalf("explicit model overridden: %q %q", run.Model, run.ModelReason)
	}
}

func TestRiskModelRespectsProjectModelPolicy(t *testing.T) {
	cfg := domain.ProjectConfig{
		ReviewRisk: domain.ReviewRiskConfig{Paths: riskPaths},
		Reviewers:  []domain.ReviewerConfig{{Harness: domain.ReviewerClaudeCode, AgentConfig: domain.AgentConfig{Model: "claude-sonnet-5-5"}}},
		Models:     domain.ModelPolicy{Denylist: []string{"claude-opus-5-5"}},
	}
	_, run := triggerWithChanged(t, cfg, []string{"backend/internal/httpd/a.go"}, domain.AgentConfig{})
	if run.Model != "claude-sonnet-5-5" || run.ModelReason != "" {
		t.Fatalf("denied risk model used: %q %q", run.Model, run.ModelReason)
	}
	cfg.Models.Allowlist = []string{"claude-opus-5-5"}
	if _, run := triggerWithChanged(t, cfg, []string{"backend/internal/httpd/a.go"}, domain.AgentConfig{}); run.Model != "claude-opus-5-5" {
		t.Fatalf("allowlisted risk model not used: %q", run.Model)
	}
}

func TestRiskModelFirstReviewOnly(t *testing.T) {
	for _, tc := range []struct {
		name      string
		firstOnly bool
		prior     bool
		priorRun  domain.ReviewRun
		want      string
	}{
		{"first review gets risk model", true, false, domain.ReviewRun{}, "claude-opus-5-5"},
		{"re-review skips it when first-only", true, true, domain.ReviewRun{Harness: domain.ReviewerClaudeCode, Status: domain.ReviewRunComplete, Verdict: domain.VerdictChangesRequested}, ""},
		{"re-review keeps it when option off", false, true, domain.ReviewRun{Harness: domain.ReviewerClaudeCode, Status: domain.ReviewRunComplete}, "claude-opus-5-5"},
		{"failed first run is retried with the risk model", true, true, domain.ReviewRun{Harness: domain.ReviewerClaudeCode, Status: domain.ReviewRunFailed}, "claude-opus-5-5"},
		{"cancelled first run is retried with the risk model", true, true, domain.ReviewRun{Harness: domain.ReviewerClaudeCode, Status: domain.ReviewRunCancelled}, "claude-opus-5-5"},
		{"another agent's finished review counts as a prior review", true, true, domain.ReviewRun{Harness: domain.ReviewerCodex, Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := domain.ProjectConfig{ReviewRisk: domain.ReviewRiskConfig{FirstReviewOnly: tc.firstOnly, Paths: riskPaths}}
			prs := prAt("sha1")
			eng := newEngineForTest(&fakeStore{}, fakeSessions{rec: liveWorker(), ok: true}, prs, fakeProjects{cfg: cfg}, &fakeLauncher{})
			eng.changedPaths = func(context.Context, string, domain.PullRequest) ([]string, error) {
				return []string{"backend/internal/storage/a.go"}, nil
			}
			list, _ := prs.ListPRsBySession(context.Background(), "mer-1")
			var runs []domain.ReviewRun
			if tc.prior {
				run := tc.priorRun
				run.PRURL, run.TargetSHA = list[0].URL, "old"
				runs = []domain.ReviewRun{run}
			}
			pick, picked := eng.riskReviewerFor(context.Background(), liveWorker(), Plan(list, runs), runs, nil)
			got := pick.config.Model
			if !picked {
				got = ""
			}
			if got != tc.want {
				t.Fatalf("model = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRiskReviewerOnAnotherInstalledAgent(t *testing.T) {
	risky := []string{"backend/internal/httpd/a.go"}
	codex := domain.ReviewRiskConfig{Paths: riskPaths, Harness: domain.ReviewerCodex, Model: "gpt-6.1-sol", Effort: "high"}
	t.Run("codex picks harness, model and effort", func(t *testing.T) {
		launcher, run := triggerWithChanged(t, domain.ProjectConfig{ReviewRisk: codex}, risky, domain.AgentConfig{})
		if launcher.gotSpec.Harness != domain.ReviewerCodex || run.Model != "gpt-6.1-sol" || launcher.gotSpec.AgentConfig.Effort != "high" || run.ModelReason == "" {
			t.Fatalf("spec harness %q model %q effort %q reason %q", launcher.gotSpec.Harness, run.Model, launcher.gotSpec.AgentConfig.Effort, run.ModelReason)
		}
	})
	t.Run("not installed keeps the normal reviewer", func(t *testing.T) {
		launcher, run := triggerWithChangedLauncher(t, domain.ProjectConfig{ReviewRisk: codex}, risky, notInstalled{&fakeLauncher{handle: "h"}, domain.ReviewerCodex})
		if launcher.gotSpec.Harness != domain.ReviewerClaudeCode || run.ModelReason != "" || run.Model == "gpt-6.1-sol" {
			t.Fatalf("harness %q model %q reason %q", launcher.gotSpec.Harness, run.Model, run.ModelReason)
		}
	})
	t.Run("policy-denied model keeps the normal reviewer", func(t *testing.T) {
		cfg := domain.ProjectConfig{ReviewRisk: codex, Models: domain.ModelPolicy{Denylist: []string{"gpt-6.1-sol"}}}
		launcher, run := triggerWithChanged(t, cfg, risky, domain.AgentConfig{})
		if launcher.gotSpec.Harness != domain.ReviewerClaudeCode || run.ModelReason != "" {
			t.Fatalf("harness %q reason %q", launcher.gotSpec.Harness, run.ModelReason)
		}
	})
	t.Run("explicit harness override wins", func(t *testing.T) {
		store := &fakeStore{}
		launcher := &fakeLauncher{handle: "h"}
		eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{cfg: domain.ProjectConfig{ReviewRisk: codex}}, launcher)
		eng.changedPaths = func(context.Context, string, domain.PullRequest) ([]string, error) { return risky, nil }
		if _, err := eng.TriggerWithOptions(context.Background(), "mer-1", TriggerOptions{Source: domain.ReviewTriggerAgent, Harness: domain.ReviewerKiro}); err != nil {
			t.Fatal(err)
		}
		if launcher.gotSpec.Harness != domain.ReviewerKiro || launcher.gotSpec.AgentConfig.Model == "gpt-6.1-sol" {
			t.Fatalf("override lost: %q %q", launcher.gotSpec.Harness, launcher.gotSpec.AgentConfig.Model)
		}
	})
	t.Run("non-risky PR is unchanged", func(t *testing.T) {
		launcher, run := triggerWithChanged(t, domain.ProjectConfig{ReviewRisk: codex}, []string{"frontend/a.ts"}, domain.AgentConfig{})
		if launcher.gotSpec.Harness != domain.ReviewerClaudeCode || run.ModelReason != "" {
			t.Fatalf("harness %q reason %q", launcher.gotSpec.Harness, run.ModelReason)
		}
	})
}
