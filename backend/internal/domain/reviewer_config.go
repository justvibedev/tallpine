package domain

// ReviewerConfig names one reviewer agent by harness. The harness is drawn from
// the reviewer vocabulary (ReviewerHarness), which is distinct from the worker
// AgentHarness set.
type ReviewerConfig struct {
	Harness     ReviewerHarness `json:"harness"`
	AgentConfig AgentConfig     `json:"agentConfig,omitempty"`
}

// FallbackReviewerHarness is the reviewer used when a project configures none
// and the worker's harness is not itself a supported reviewer.
const FallbackReviewerHarness = ReviewerClaudeCode

// ResolveReviewerHarness picks the reviewer harness for a worker. A configured
// reviewer wins. Otherwise only the original, unattended-safe reviewer set is
// inherited from the worker. Every other reviewer requires explicit selection,
// so adding an experimental adapter never silently changes an existing project.
func (c ProjectConfig) ResolveReviewerHarness(worker AgentHarness) ReviewerHarness {
	if len(c.Reviewers) > 0 {
		return c.Reviewers[0].Harness
	}
	switch worker {
	case HarnessClaudeCode:
		return ReviewerClaudeCode
	case HarnessCodex:
		return ReviewerCodex
	}
	return FallbackReviewerHarness
}

// DefaultWorkerReviewer is the reviewer a project gets when it configures no
// reviewer: the project's default worker agent, with that worker's model and
// effort. It returns an empty harness when the project pins no worker agent,
// or when that agent has no unattended-safe reviewer (see
// ResolveReviewerHarness), so callers keep their existing fallback.
//
// Only model and effort carry over. Permissions and mode are worker settings;
// a reviewer's sandbox is owned by its adapter and must never inherit a
// worker's bypass permissions.
func (c ProjectConfig) DefaultWorkerReviewer() (ReviewerHarness, AgentConfig) {
	worker := c.Worker.Harness
	if worker == "" {
		return "", AgentConfig{}
	}
	harness := c.ResolveReviewerHarness(worker)
	if string(harness) != string(worker) {
		return "", AgentConfig{}
	}
	config := AgentConfig{Model: c.AgentConfig.Model, Effort: c.AgentConfig.Effort}
	if c.Worker.AgentConfig.Model != "" {
		config.Model = c.Worker.AgentConfig.Model
	}
	if c.Worker.AgentConfig.Effort != "" {
		config.Effort = c.Worker.AgentConfig.Effort
	}
	return harness, config
}
