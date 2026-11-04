package domain

import (
	"fmt"
	"path"
	"strings"
)

// The stronger reviewer a risky change gets unless the project names another.
const (
	ReviewRiskHarnessDefault = ReviewerClaudeCode
	ReviewRiskModelDefault   = "claude-opus-5-5"
	ReviewRiskEffortDefault  = "high"
)

// DefaultReviewRiskPaths are the globs whose changes warrant the stronger
// reviewer when a project names none: schema migrations, which every project
// can have. A project lists its own critical paths on top of this generic start.
var DefaultReviewRiskPaths = []string{"**/migrations/**"}

// ReviewRiskConfig picks the reviewer from a PR's changed paths when a review
// is triggered without an explicit reviewer. Zero value uses the defaults.
type ReviewRiskConfig struct {
	// Paths replaces DefaultReviewRiskPaths when non-empty. Globs use "/"
	// separators; "**" spans directories and "{a,b}" alternates.
	Paths []string `json:"paths,omitempty"`
	// Harness is the stronger reviewer's agent. Empty means ReviewRiskHarnessDefault.
	Harness ReviewerHarness `json:"harness,omitempty"`
	// Model is the stronger reviewer's model. Empty with no Harness means
	// ReviewRiskModelDefault; empty with an explicit agent means that agent's default.
	Model string `json:"model,omitempty"`
	// Effort is the stronger reviewer's effort. Empty with no Harness and no
	// Model means ReviewRiskEffortDefault; otherwise the agent's default.
	Effort string `json:"effort,omitempty"`
	// FirstReviewOnly applies the stronger reviewer to a PR's first review only;
	// later reviews of the same PR use the configured reviewer. Off by default.
	FirstReviewOnly bool `json:"firstReviewOnly,omitempty"`
}

// Validate bounds what a user may set: a known agent, a model name, an effort
// level and a short list of globs.
func (c ReviewRiskConfig) Validate() error {
	if len(strings.TrimSpace(c.Model)) > 100 {
		return fmt.Errorf("reviewRisk.model is too long")
	}
	if c.Harness != "" && !c.Harness.IsKnown() {
		return fmt.Errorf("reviewRisk.harness %q is not a known reviewer agent", c.Harness)
	}
	if c.Effort != "" && !ValidEffort(c.Effort) {
		return fmt.Errorf("reviewRisk.effort %q is not one of %s", c.Effort, effortList())
	}
	if len(c.Paths) > 50 {
		return fmt.Errorf("reviewRisk.paths has more than 50 entries")
	}
	for _, p := range c.Paths {
		if strings.TrimSpace(p) == "" || len(p) > 200 {
			return fmt.Errorf("reviewRisk.paths entries must be 1-200 characters")
		}
	}
	return nil
}

// Resolve returns the effective globs, agent, model and effort.
func (c ReviewRiskConfig) Resolve() (paths []string, harness ReviewerHarness, model, effort string) {
	paths, harness = c.Paths, c.Harness
	model, effort = strings.TrimSpace(c.Model), strings.TrimSpace(c.Effort)
	if len(paths) == 0 {
		paths = DefaultReviewRiskPaths
	}
	if harness == "" {
		harness = ReviewRiskHarnessDefault
		if model == "" {
			model = ReviewRiskModelDefault
			if effort == "" {
				effort = ReviewRiskEffortDefault
			}
		}
	} else if harness == ReviewRiskHarnessDefault && model == "" {
		model = ReviewRiskModelDefault
	}
	return paths, harness, model, effort
}

// MatchReviewRisk reports the first changed path matching any glob, and that glob.
func MatchReviewRisk(globs, changed []string) (file, glob string, ok bool) {
	for _, g := range globs {
		for _, alt := range expandBraces(strings.TrimSpace(g)) {
			for _, f := range changed {
				if matchSegments(strings.Split(alt, "/"), strings.Split(strings.TrimPrefix(f, "./"), "/")) {
					return f, g, true
				}
			}
		}
	}
	return "", "", false
}

func expandBraces(g string) []string {
	open := strings.IndexByte(g, '{')
	if open < 0 {
		return []string{g}
	}
	closeAt := strings.IndexByte(g[open:], '}')
	if closeAt < 0 {
		return []string{g}
	}
	closeAt += open
	var out []string
	for _, opt := range strings.Split(g[open+1:closeAt], ",") {
		out = append(out, expandBraces(g[:open]+opt+g[closeAt+1:])...)
	}
	return out
}

func matchSegments(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			for i := 0; i <= len(name); i++ {
				if matchSegments(pat[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, err := path.Match(pat[0], name[0]); err != nil || !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}
