package domain

import (
	"strings"
	"testing"
)

func TestProjectConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ProjectConfig
		wantErr bool
	}{
		{"empty ok", ProjectConfig{}, false},
		{"good agent config", ProjectConfig{AgentConfig: AgentConfig{Model: "m", Permissions: PermissionModeAuto}}, false},
		{"good agent mode", ProjectConfig{AgentConfig: AgentConfig{Mode: "ultra"}}, false},
		{"bad agent mode", ProjectConfig{AgentConfig: AgentConfig{Mode: "turbo"}}, true},
		{"session mode misused as agent mode", ProjectConfig{AgentConfig: AgentConfig{Mode: "chat"}}, true},
		{"good default session mode", ProjectConfig{DefaultSessionMode: SessionModeChat}, false},
		{"bad default session mode", ProjectConfig{DefaultSessionMode: "ultra"}, true},
		{"bad permission", ProjectConfig{AgentConfig: AgentConfig{Permissions: "yolo"}}, true},
		{"good session prefix", ProjectConfig{SessionPrefix: "tp"}, false},
		{"session prefix with slash", ProjectConfig{SessionPrefix: "tp/project"}, true},
		{"session prefix with backslash", ProjectConfig{SessionPrefix: `tallpine\project`}, true},
		{"session prefix traversal component", ProjectConfig{SessionPrefix: ".."}, true},
		{"valid env", ProjectConfig{Env: map[string]string{"API_KEY": "a=b\nsecond line"}}, false},
		{"Tallpine-owned env name", ProjectConfig{Env: map[string]string{"TALLPINE_SESSION_ID": "spoof"}}, true},
		{"case-folded Tallpine-owned env name", ProjectConfig{Env: map[string]string{"tallpine_worktree_path": "spoof"}}, true},
		{"invalid env name", ProjectConfig{Env: map[string]string{"BAD KEY": "secret"}}, true},
		{"invalid env value", ProjectConfig{Env: map[string]string{"KEY": "bad\x00value"}}, true},
		{"case-folded duplicate env", ProjectConfig{Env: map[string]string{"KEY": "first", "key": "second"}}, true},
		{"good role override", ProjectConfig{Worker: RoleOverride{Harness: HarnessCodex}}, false},
		{"removed role harness is normalized", ProjectConfig{Orchestrator: RoleOverride{Harness: "gemini"}}, false},
		{"unknown role harness", ProjectConfig{Orchestrator: RoleOverride{Harness: "claud-code"}}, true},
		{"fake role harness", ProjectConfig{Worker: RoleOverride{Harness: HarnessFake}}, true},
		{"bad role agent config", ProjectConfig{Worker: RoleOverride{AgentConfig: AgentConfig{Permissions: "nope"}}}, true},
		{"good symlinks", ProjectConfig{Symlinks: []string{".env", "configs/dev.toml"}}, false},
		{"symlink absolute path", ProjectConfig{Symlinks: []string{"/etc/passwd"}}, true},
		{"symlink parent escape", ProjectConfig{Symlinks: []string{"../escape"}}, true},
		{"symlink embedded parent", ProjectConfig{Symlinks: []string{"a/../../b"}}, true},
		{"symlink bare ..", ProjectConfig{Symlinks: []string{".."}}, true},
		{"good prompt rules", ProjectConfig{AgentRules: "Run tests.", AgentRulesFile: "docs/agent-rules.md", OrchestratorRules: "Delegate work."}, false},
		{"agent rules file absolute path", ProjectConfig{AgentRulesFile: "/etc/passwd"}, true},
		{"agent rules file parent escape", ProjectConfig{AgentRulesFile: "../rules.md"}, true},
		{"agent rules file cleans to dot", ProjectConfig{AgentRulesFile: "docs/.."}, true},
		{"agent rules file bare dot", ProjectConfig{AgentRulesFile: "."}, true},
		{"good verify command", ProjectConfig{VerifyCommand: "cd backend && go test ./..."}, false},
		{"verify command newline", ProjectConfig{VerifyCommand: "go test ./...\nrm -rf /"}, true},
		{"verify command control char", ProjectConfig{VerifyCommand: "go test\x00"}, true},
		{"verify command too long", ProjectConfig{VerifyCommand: strings.Repeat("a", MaxVerifyCommandLen+1)}, true},
		{"good reviewers", ProjectConfig{Reviewers: []ReviewerConfig{{Harness: ReviewerClaudeCode}}}, false},
		{"good codex reviewer", ProjectConfig{Reviewers: []ReviewerConfig{{Harness: ReviewerCodex}}}, false},
		{"good kiro reviewer", ProjectConfig{Reviewers: []ReviewerConfig{{Harness: ReviewerKiro}}}, false},
		{"removed opencode reviewer is normalized", ProjectConfig{Reviewers: []ReviewerConfig{{Harness: "opencode"}}}, false},
		{"unknown reviewer harness", ProjectConfig{Reviewers: []ReviewerConfig{{Harness: "claud-code"}}}, true},
		{"removed aider reviewer is normalized", ProjectConfig{Reviewers: []ReviewerConfig{{Harness: "aider"}}}, false},
		{"empty reviewer harness", ProjectConfig{Reviewers: []ReviewerConfig{{Harness: ""}}}, true},
		{"tracker intake assignee rule", ProjectConfig{TrackerIntake: TrackerIntakeConfig{Enabled: true, Assignee: "alice"}}, false},
		{"tracker intake explicit github", ProjectConfig{TrackerIntake: TrackerIntakeConfig{Enabled: true, Provider: TrackerProviderGitHub, Assignee: "alice"}}, false},
		{"tracker intake no rule", ProjectConfig{TrackerIntake: TrackerIntakeConfig{Enabled: true}}, true},
		{"tracker intake unknown provider", ProjectConfig{TrackerIntake: TrackerIntakeConfig{Enabled: true, Provider: "linear", Assignee: "alice"}}, true},
		{"tracker intake repo with whitespace", ProjectConfig{TrackerIntake: TrackerIntakeConfig{Enabled: true, Repo: " acme/demo", Assignee: "alice"}}, true},
		{"tracker intake assignee with whitespace", ProjectConfig{TrackerIntake: TrackerIntakeConfig{Enabled: true, Assignee: " alice"}}, true},
		{"auto review enabled", ProjectConfig{AutoReview: BoolPtr(true)}, false},
		{"auto review disabled", ProjectConfig{AutoReview: BoolPtr(false)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.cfg.Validate(); (err != nil) != tt.wantErr {
				t.Fatalf("Validate() err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestProjectConfigValidateNormalizesRemovedHarnesses(t *testing.T) {
	stale := ProjectConfig{
		Worker:       RoleOverride{Harness: "opencode", AgentConfig: AgentConfig{Model: "opencode/gpt", Effort: "bogus", Permissions: PermissionModeAuto}},
		Orchestrator: RoleOverride{Harness: "aider"},
		Reviewers:    []ReviewerConfig{{Harness: "opencode", AgentConfig: AgentConfig{Model: "opencode/gpt"}}, {Harness: ReviewerCodex}},
	}
	if err := stale.Validate(); err != nil {
		t.Fatalf("Validate() rejected a stored removed harness: %v", err)
	}
	got := stale.NormalizeHarnesses()
	if got.Worker.Harness != FallbackHarness || got.Orchestrator.Harness != FallbackHarness {
		t.Fatalf("roles = %q/%q, want %q", got.Worker.Harness, got.Orchestrator.Harness, FallbackHarness)
	}
	if got.Worker.AgentConfig.Model != "" || got.Worker.AgentConfig.Effort != "" || got.Worker.AgentConfig.Permissions != PermissionModeAuto {
		t.Fatalf("worker agent config = %#v, want model/effort dropped and permissions kept", got.Worker.AgentConfig)
	}
	if got.Reviewers[0].Harness != FallbackReviewerHarness || got.Reviewers[0].AgentConfig.Model != "" || got.Reviewers[1].Harness != ReviewerCodex {
		t.Fatalf("reviewers = %#v", got.Reviewers)
	}
	// The receiver (including the caller's reviewers slice) is left untouched.
	if stale.Worker.Harness != "opencode" || stale.Reviewers[0].Harness != "opencode" {
		t.Fatalf("NormalizeHarnesses mutated its receiver: %#v", stale)
	}
}

func TestDefaultProjectConfig(t *testing.T) {
	def := DefaultProjectConfig()

	// The one documented non-empty default.
	if def.DefaultBranch != DefaultBranchAuto {
		t.Fatalf("default DefaultBranch = %q, want %q", def.DefaultBranch, DefaultBranchAuto)
	}

	// Every other field defaults to its zero value: clearing the documented
	// default must leave the config completely empty.
	def.DefaultBranch = ""
	if !def.IsZero() {
		t.Fatalf("default config has unexpected non-zero fields: %#v", def)
	}
}

func TestProjectConfigWithDefaults(t *testing.T) {
	// An unset config gets the documented defaults.
	got := (ProjectConfig{}).WithDefaults()
	if got.DefaultBranch != DefaultBranchAuto {
		t.Fatalf("WithDefaults = %#v, want branch=%s", got, DefaultBranchAuto)
	}

	// Set fields are preserved, not overwritten.
	got = (ProjectConfig{
		DefaultBranch: "develop",
		AgentConfig:   AgentConfig{Model: "m"},
	}).WithDefaults()
	if got.DefaultBranch != "develop" {
		t.Fatalf("WithDefaults overwrote set fields: %#v", got)
	}
	if got.AgentConfig.Model != "m" {
		t.Fatalf("WithDefaults dropped a set field: %#v", got.AgentConfig)
	}
	if got.WorktreeBaseBranch() != "develop" {
		t.Fatalf("WorktreeBaseBranch = %q, want develop", got.WorktreeBaseBranch())
	}
	if got := (ProjectConfig{}).WorktreeBaseBranch(); got != "" {
		t.Fatalf("automatic WorktreeBaseBranch = %q, want empty for adapter inference", got)
	}
	if got := (ProjectConfig{DefaultBranch: DefaultBranchAuto}).WorktreeBaseBranch(); got != "" {
		t.Fatalf("explicit auto WorktreeBaseBranch = %q, want empty for adapter inference", got)
	}

	got = (ProjectConfig{TrackerIntake: TrackerIntakeConfig{Enabled: true, Assignee: "alice"}}).WithDefaults()
	if got.TrackerIntake.Provider != "" {
		t.Fatalf("TrackerIntake.Provider = %q, want empty (inferred at use time)", got.TrackerIntake.Provider)
	}

	got = (ProjectConfig{}).WithDefaults()
	if got.TrackerIntake.Provider != "" {
		t.Fatalf("disabled TrackerIntake.Provider = %q, want empty", got.TrackerIntake.Provider)
	}
}

func TestInferTrackerProvider(t *testing.T) {
	tests := []struct {
		name    string
		repoURL string
		want    TrackerProvider
	}{
		{"empty", "", TrackerProviderGitHub},
		{"https github", "https://github.com/acme/demo.git", TrackerProviderGitHub},
		{"ssh github", "git@github.com:acme/demo.git", TrackerProviderGitHub},
		{"ghe.io is no longer github", "https://ghe.corp.ghe.io/acme/demo.git", TrackerProviderGitLab},
		{"github with port", "https://github.com:443/org/repo.git", TrackerProviderGitHub},
		{"ssh github with port", "ssh://git@github.com:2222/org/repo.git", TrackerProviderGitHub},
		{"https gitlab.com", "https://gitlab.com/group/repo.git", TrackerProviderGitLab},
		{"ssh gitlab.com", "git@gitlab.com:group/repo.git", TrackerProviderGitLab},
		{"self-managed gitlab", "https://gitlab.internal/group/repo.git", TrackerProviderGitLab},
		{"ssh self-managed", "git@gitlab.internal:group/repo.git", TrackerProviderGitLab},
		{"self-managed with port", "https://gitlab.local:8443/group/repo.git", TrackerProviderGitLab},
		{"non-gitlab custom host", "https://dev.company.com/group/repo.git", TrackerProviderGitLab},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := InferTrackerProvider(tt.repoURL)
			if got != tt.want {
				t.Errorf("InferTrackerProvider(%q) = %q, want %q", tt.repoURL, got, tt.want)
			}
		})
	}
}

func TestResolveReviewerHarness(t *testing.T) {
	// A configured reviewer always wins, regardless of the worker harness.
	cfg := ProjectConfig{Reviewers: []ReviewerConfig{{Harness: ReviewerClaudeCode}}}
	if got := cfg.ResolveReviewerHarness(HarnessKiro); got != ReviewerClaudeCode {
		t.Fatalf("configured reviewer = %q, want claude-code", got)
	}

	// No reviewer configured: preserve automatic inheritance only for the
	// original unattended-safe reviewer set.
	if got := (ProjectConfig{}).ResolveReviewerHarness(HarnessClaudeCode); got != ReviewerClaudeCode {
		t.Fatalf("claude-code worker = %q, want reviewer claude-code", got)
	}
	if got := (ProjectConfig{}).ResolveReviewerHarness(HarnessCodex); got != ReviewerCodex {
		t.Fatalf("codex worker = %q, want reviewer codex", got)
	}

	// A worker harness that is not itself a reviewer (kiro) or that was removed
	// falls back to claude-code, so reviewing never starts on an experimental
	// reviewer without explicit selection.
	for _, worker := range []AgentHarness{HarnessKiro, "opencode", "aider"} {
		if got := (ProjectConfig{}).ResolveReviewerHarness(worker); got != FallbackReviewerHarness {
			t.Errorf("%s worker = %q, want explicit-selection fallback %q", worker, got, FallbackReviewerHarness)
		}
	}
}

func TestProjectConfigIsZero(t *testing.T) {
	if !(ProjectConfig{}).IsZero() {
		t.Fatal("empty config should be zero")
	}
	if (ProjectConfig{DefaultBranch: "main"}).IsZero() {
		t.Fatal("populated config should not be zero")
	}
	if (ProjectConfig{Env: map[string]string{"A": "b"}}).IsZero() {
		t.Fatal("config with env should not be zero")
	}
	if (ProjectConfig{AutoReview: BoolPtr(true)}).IsZero() {
		t.Fatal("config with autoReview enabled should not be zero")
	}
}

func TestProjectConfigValidateExplainsSessionModeMisuse(t *testing.T) {
	err := ProjectConfig{AgentConfig: AgentConfig{Mode: "tui"}}.Validate()
	if err == nil || !strings.Contains(err.Error(), "reasoning effort") || !strings.Contains(err.Error(), "defaultSessionMode") {
		t.Fatalf("error = %v, want pointer to defaultSessionMode", err)
	}
	err = ProjectConfig{DefaultSessionMode: "ultra"}.Validate()
	if err == nil || !strings.Contains(err.Error(), "defaultSessionMode") || !strings.Contains(err.Error(), "chat") {
		t.Fatalf("error = %v, want defaultSessionMode vocabulary", err)
	}
}

func TestProjectConfigValidateAgentRulesCap(t *testing.T) {
	at := ProjectConfig{AgentRules: strings.Repeat("x", MaxAgentRulesBytes)}
	if err := at.ValidateAgentRulesCap(""); err != nil {
		t.Fatalf("rules at the cap rejected: %v", err)
	}
	over := ProjectConfig{AgentRules: strings.Repeat("x", MaxAgentRulesBytes+1)}
	if err := over.ValidateAgentRulesCap(""); err == nil || !strings.Contains(err.Error(), "agentRulesFile") {
		t.Fatalf("new over-cap rules error = %v, want a hint to use agentRulesFile", err)
	}
	if err := over.ValidateAgentRulesCap(over.AgentRules); err != nil {
		t.Fatalf("unchanged legacy over-cap rules rejected: %v", err)
	}
	if err := over.ValidateAgentRulesCap(strings.Repeat("x", MaxAgentRulesBytes)); err == nil {
		t.Fatal("grown over-cap rules accepted")
	}
}
