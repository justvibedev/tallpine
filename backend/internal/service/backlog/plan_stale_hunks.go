package backlog

import (
	"regexp"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
)

var (
	identRe    = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	pathLikeRe = regexp.MustCompile(`/|\.[A-Za-z]{1,5}$`)
	backtickRe = regexp.MustCompile("`([^`]+)`")
)

// coveredPaths are the changed paths that a planned path covers.
func coveredPaths(split []domain.PlanPR, changed []string) []string {
	var out []string
	for _, c := range changed {
		if plannedPathsChanged(split, []string{c}) {
			out = append(out, c)
		}
	}
	return out
}

// hunksTouchPlan reports whether any covered path's changed lines mention a
// symbol the plan names. Added, deleted and binary files (no line data) count
// as touching; comment-only and blank lines do not.
func hunksTouchPlan(plan domain.BacklogPlan, covered []string, lines map[string][]string) bool {
	if len(planNamed(plan.PRSplit)) == 0 {
		return true
	}
	syms := planSymbols(plan)
	if len(syms) == 0 {
		return true // no symbols to compare: keep the file-level rule
	}
	for _, c := range covered {
		ls, ok := lines[c]
		if !ok {
			return true
		}
		for _, l := range ls {
			t := strings.TrimSpace(l)
			if t == "" || strings.HasPrefix(t, "//") || strings.HasPrefix(t, "#") || t == "*" || strings.HasPrefix(t, "* ") || strings.HasPrefix(t, "*/") || strings.HasPrefix(t, "/*") {
				continue
			}
			for _, id := range identRe.FindAllString(t, -1) {
				if syms[id] {
					return true
				}
			}
		}
	}
	return false
}

func planNamed(split []domain.PlanPR) []string {
	var n []string
	for _, pr := range split {
		n = append(n, pr.Files...)
	}
	return n
}

// planSymbols are the code-like identifiers in the plan text: camelCase or
// snake_case words, and anything the plan puts in backticks.
func planSymbols(plan domain.BacklogPlan) map[string]bool {
	syms := map[string]bool{}
	texts := make([]string, 0, 2+len(plan.PRSplit))
	texts = append(texts, plan.PlanMarkdown, plan.DecisionsMD)
	for _, pr := range plan.PRSplit {
		texts = append(texts, strings.Join(append([]string{pr.Title, pr.Summary, pr.Tests}, append(append([]string{}, pr.Consumes...), pr.Produces...)...), "\n"))
	}
	for _, text := range texts {
		for _, m := range backtickRe.FindAllStringSubmatch(text, -1) {
			if pathLikeRe.MatchString(m[1]) {
				continue // a file path names no symbol
			}
			for _, id := range identRe.FindAllString(m[1], -1) {
				syms[id] = true
			}
		}
		for _, id := range identRe.FindAllString(text, -1) {
			if len(id) >= 4 && (strings.Contains(id, "_") || hasInnerUpper(id)) {
				syms[id] = true
			}
		}
	}
	return syms
}

func hasInnerUpper(s string) bool {
	for _, r := range s[1:] {
		if r >= 'A' && r <= 'Z' {
			return true
		}
	}
	return false
}
