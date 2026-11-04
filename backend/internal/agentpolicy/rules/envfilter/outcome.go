package envfilter

import (
	"sort"
	"strings"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
)

// maxRecordedDropped bounds the dropped-name list in the decision record.
const maxRecordedDropped = 200

// Outcome is the environment decision for a session and the effect its seams
// apply. The zero Outcome is mode inherit: nothing changes.
type Outcome struct {
	mode        Mode
	passthrough []string
	harness     string
	strict      bool
}

// ForHarness narrows the outcome to one agent harness: only that harness's own
// credential variables are kept besides the shared base. An empty or unknown
// harness keeps every harness's list, except under the hardened profile, where it
// keeps none.
func (o Outcome) ForHarness(harness string) Outcome {
	o.harness = harness
	return o
}

// Of returns the environment outcome of a decision; the zero Decision yields
// mode inherit.
func Of(d agentpolicy.Decision) Outcome {
	if out, ok := d.Outcome(Name); ok {
		if typed, ok := out.(Outcome); ok {
			return typed
		}
	}
	return Outcome{mode: Inherit}
}

// Record implements agentpolicy.Outcome. The mode and its source are already in
// the decision record; the dropped names are added by the seam that saw the
// environment (see Facts).
func (Outcome) Record() map[string]any { return nil }

// Allowlist reports whether the agent must not inherit the daemon environment.
func (o Outcome) Allowlist() bool { return o.mode == Allowlist }

// Result is what the allowlist keeps of an environment.
type Result struct {
	// Keep holds the entries ("KEY=value") the agent may start with.
	Keep []string
	// Dropped lists the names withheld, sorted. Names only, never values.
	Dropped []string
}

// Filter applies the allowlist to environ ("KEY=value" entries): the curated
// lists plus the passthrough list are kept. withhold names variables that stay
// out even if listed (the GitHub tokens, when the githubCreds rule keeps them out
// of the environment). caseInsensitive is for Windows.
func (o Outcome) Filter(environ []string, caseInsensitive bool, withhold []string) Result {
	allowed := newMatcher(caseInsensitive, baseEnvNames, harnessNames(o.harness, o.strict), o.passthrough)
	out := newMatcher(caseInsensitive, withhold)
	var res Result
	seen := map[string]bool{}
	for _, entry := range environ {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			continue
		}
		if neverPassed(strings.ToUpper(key)) || out.has(key) || !allowed.has(key) || (!caseInsensitive && !shellName(key)) {
			if !seen[key] {
				seen[key] = true
				res.Dropped = append(res.Dropped, key)
			}
			continue
		}
		res.Keep = append(res.Keep, entry)
	}
	sort.Strings(res.Dropped)
	return res
}

// Facts is what the decision record says about the environment: counts and the
// dropped variable names (capped), so "why did my agent lose X" is answerable.
// Never values.
func (o Outcome) Facts(res Result) map[string]any {
	dropped := res.Dropped
	facts := map[string]any{"keptCount": len(res.Keep), "droppedCount": len(dropped)}
	if len(dropped) > maxRecordedDropped {
		dropped = dropped[:maxRecordedDropped]
	}
	facts["droppedNames"] = dropped
	return facts
}

// neverPassed are removed even if listed in the passthrough list: they name a
// session of the daemon itself, never of an agent.
func neverPassed(upper string) bool {
	return strings.HasPrefix(upper, "TALLPINE_SESSION_TOKEN") || strings.HasPrefix(upper, "TALLPINE_SESSION_AUTH")
}

// matcher answers whether a variable name is on a list of exact names and
// trailing-* prefixes.
type matcher struct {
	exact    map[string]bool
	prefixes []string
	fold     bool
}

func newMatcher(fold bool, lists ...[]string) matcher {
	m := matcher{exact: map[string]bool{}, fold: fold}
	for _, list := range lists {
		for _, name := range list {
			if fold {
				name = strings.ToUpper(name)
			}
			if prefix, ok := strings.CutSuffix(name, "*"); ok {
				m.prefixes = append(m.prefixes, prefix)
			} else {
				m.exact[name] = true
			}
		}
	}
	return m
}

func (m matcher) has(name string) bool {
	if m.fold {
		name = strings.ToUpper(name)
	}
	if m.exact[name] {
		return true
	}
	for _, prefix := range m.prefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// shellName reports whether key is a plain identifier. The tmux launch script
// exports variables by name and rejects anything else, so on POSIX hosts a
// listed but non-identifier name (dot, hyphen, parentheses) is dropped and
// reported instead of failing the launch.
func shellName(key string) bool {
	for i, r := range key {
		switch {
		case r == '_', r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return key != ""
}
