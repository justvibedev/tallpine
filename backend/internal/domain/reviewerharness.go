package domain

// ReviewerHarness identifies a code-review agent. It is a separate vocabulary
// from AgentHarness on purpose: a reviewer-only tool (e.g. the Greptile CLI)
// must not become a valid worker, and a worker harness does not automatically
// become a valid reviewer. The two sets are maintained independently and only
// happen to share ids where the same tool serves both roles.
type ReviewerHarness string

// Supported reviewer harnesses. Add a reviewer-only tool here (and register its
// adapter) without widening the worker AgentHarness set.
const (
	ReviewerClaudeCode ReviewerHarness = "claude-code"
	ReviewerCodex      ReviewerHarness = "codex"
	ReviewerKiro       ReviewerHarness = "kiro"
	// ReviewerDeepSeek shares the worker harness id (HarnessDeepSeek), which
	// the reviewer registry relies on to invalidate the binary cache.
	ReviewerDeepSeek ReviewerHarness = "deepseek-harness"
)

// AllReviewerHarnesses is the canonical set used to validate a configured
// reviewer harness.
var AllReviewerHarnesses = []ReviewerHarness{
	ReviewerClaudeCode,
	ReviewerCodex,
	ReviewerKiro,
	ReviewerDeepSeek,
}

// IsKnown reports whether h is one of the supported reviewer harnesses.
func (h ReviewerHarness) IsKnown() bool {
	for _, k := range AllReviewerHarnesses {
		if h == k {
			return true
		}
	}
	return false
}

// IsRemoved reports whether h is the id of a reviewer that earlier builds
// shipped and this one no longer does; see AgentHarness.IsRemoved.
func (h ReviewerHarness) IsRemoved() bool { return removedHarnessNames[string(h)] }

// IsUnsupported reports whether h names a reviewer this build no longer ships.
// The empty reviewer ("delegate to project configuration") is not unsupported.
func (h ReviewerHarness) IsUnsupported() bool {
	return h != "" && !h.IsKnown()
}

// OrFallback resolves a reviewer harness read from stored configuration. An
// empty or supported reviewer is returned unchanged; an unsupported one
// resolves to FallbackReviewerHarness and replaced is true so the caller can
// log the substitution.
func (h ReviewerHarness) OrFallback() (resolved ReviewerHarness, replaced bool) {
	if h.IsUnsupported() {
		return FallbackReviewerHarness, true
	}
	return h, false
}
