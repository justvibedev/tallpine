package review

import (
	stdctx "context"

	"tallpine.eu/tallpine/backend/internal/contextwindow"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// CompactDefaults reads the compaction choice new sessions of an agent start
// with: the Settings default, "" (Auto) when none is saved.
type CompactDefaults interface {
	AutoCompactDefault(ctx stdctx.Context, harness domain.AgentHarness) string
}

// reviewerCompactAgent is the agent whose compaction setting a reviewer
// harness launches: Claude Code reads it from --settings and Codex from its
// token-limit flag. Other reviewers have no window Tallpine can set.
func reviewerCompactAgent(harness domain.ReviewerHarness) (domain.AgentHarness, bool) {
	switch harness {
	case domain.ReviewerClaudeCode:
		return domain.HarnessClaudeCode, true
	case domain.ReviewerCodex:
		return domain.HarnessCodex, true
	default:
		return "", false
	}
}

// withReviewerCompaction gives a Claude Code or Codex reviewer the compaction
// window a worker of the same agent starts with: the Settings default for that
// agent (domain.DefaultAutoCompactChoice), plus the reviewer model's window so
// the adapter resolves it with domain.ResolveAutoCompactWindow. A window the
// config already carries is kept; with no default the agent's own applies.
func (e *Engine) withReviewerCompaction(ctx stdctx.Context, project domain.ProjectID, harness domain.ReviewerHarness, config domain.AgentConfig) domain.AgentConfig {
	agent, ok := reviewerCompactAgent(harness)
	if !ok {
		return config
	}
	if config.Launch.AutoCompactWindow == "" && e.compactDefaults != nil {
		config.Launch.AutoCompactWindow = domain.DefaultAutoCompactChoice(domain.KindWorker, domain.ProjectConfig{}, e.compactDefaults.AutoCompactDefault(ctx, agent), false)
	}
	if config.Launch.AutoCompactWindow != "" {
		config.Launch.ModelContextWindow, _ = contextwindow.Lookup(ctx, e.modelCatalog, string(agent), string(project), config.Model)
		config.Launch.AutoCompactBaseline = e.reviewerBaseline(ctx, project, agent)
	}
	return config
}

// reviewerBaseline is the context a reviewer is measured to start with: a
// worker's of the same project and agent (domain.AutoCompactBaselineKind),
// read at each launch so its window keeps room above it (B735). 0 for a store
// that does not measure baselines; the default on a failed read.
func (e *Engine) reviewerBaseline(ctx stdctx.Context, project domain.ProjectID, agent domain.AgentHarness) int64 {
	reader, ok := e.store.(ports.AutoCompactBaselineReader)
	if !ok {
		return 0
	}
	baseline, err := reader.Baseline(ctx, project, domain.KindWorker, agent)
	if err != nil {
		return domain.AutoCompactBaselineDefault
	}
	return baseline
}
