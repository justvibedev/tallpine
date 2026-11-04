package sessionmanager

import (
	"context"
	"os"
	"runtime"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/envfilter"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/githubcreds"
	"tallpine.eu/tallpine/backend/internal/domain"
)

const envFilterDecideTimeout = 5 * time.Second

// envFilterFor is the environment outcome of a session's project. Like
// githubCredsFor, the env builder has only ids, so it asks the engine for the
// same project-scoped decision the launch made.
func (m *Manager) envFilterFor(id domain.SessionID, project domain.ProjectID) envfilter.Outcome {
	if m.policy == nil {
		return envfilter.Outcome{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), envFilterDecideTimeout)
	defer cancel()
	return envfilter.Of(m.policy.Decide(ctx, agentpolicy.SessionRef{ID: id, Project: project}))
}

// applyEnvFilter gives an agent the environment the policy allows. Under mode
// inherit it does nothing: the runtime lets the agent inherit the daemon's
// environment as it always did. Under allowlist it adds the daemon variables the
// allowlist keeps to the session's environment map (anything the map already
// holds wins: the project's environment, VO_* identity, account variables) and
// marks the map as the whole environment, so no launch path lets the daemon's
// other variables through. Variables the githubCreds rule keeps out of the
// environment stay out even if the allowlist would keep them.
func (m *Manager) applyEnvFilter(id domain.SessionID, project domain.ProjectID, harness domain.AgentHarness, env map[string]string, caseInsensitive bool) {
	out := m.envFilterFor(id, project)
	if !out.Allowlist() {
		return
	}
	res := out.ForHarness(m.launchHarness(id, harness)).Filter(os.Environ(), caseInsensitive, githubcreds.Of(m.decideStatic(project)).WithheldVariables())
	removed := agentlaunch.UnsetNames(env)
	for _, entry := range res.Keep {
		if key, value, ok := strings.Cut(entry, "="); ok && !envHasKey(env, key, caseInsensitive) && !nameListed(removed, key, caseInsensitive) {
			env[key] = value
		}
	}
	env[agentlaunch.EnvScrub] = "1"
}

// sessionHarness is the harness of a stored session, or "" when it cannot be
// read (a session not yet persisted, a store error): the filter then keeps every
// harness's variables, as it did before the lists were per harness.
func (m *Manager) sessionHarness(id domain.SessionID) string {
	if m.store == nil || id == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), envFilterDecideTimeout)
	defer cancel()
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		return ""
	}
	return string(rec.Harness)
}

// decideStatic is the project-scoped decision with no session.
func (m *Manager) decideStatic(project domain.ProjectID) agentpolicy.Decision {
	if m.policy == nil {
		return agentpolicy.Decision{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), envFilterDecideTimeout)
	defer cancel()
	return m.policy.Decide(ctx, agentpolicy.SessionRef{Project: project})
}

// daemonPassesEnv reports whether the daemon's own value of name reaches an
// agent of project: always without an allowlist, otherwise only if the allowlist
// keeps it. Token detection uses it so a daemon token the agent will never see
// does not count as "already configured".
func (m *Manager) daemonPassesEnv(project domain.ProjectID, name string, caseInsensitive bool) bool {
	out := m.envFilterFor("", project)
	if !out.Allowlist() {
		return true
	}
	return len(out.Filter([]string{name + "=x"}, caseInsensitive, nil).Keep) > 0
}

// envFacts is what the decision record says about the environment: counts and
// the dropped variable names (capped), never values. Computed from the daemon's
// environment as it is now.
func envFacts(decision agentpolicy.Decision) map[string]map[string]any {
	out := envfilter.Of(decision)
	if !out.Allowlist() {
		return nil
	}
	res := out.Filter(os.Environ(), runtime.GOOS == "windows", githubcreds.Of(decision).WithheldVariables())
	return map[string]map[string]any{envfilter.Name: out.Facts(res)}
}

// launchFacts are the decision-record facts of every rule that learns something
// at launch (GitHub credentials, the environment).
func launchFacts(decision agentpolicy.Decision, projectEnv map[string]string) map[string]map[string]any {
	facts := map[string]map[string]any{}
	for _, more := range []map[string]map[string]any{githubFacts(decision, projectEnv), envFacts(decision)} {
		for rule, entries := range more {
			facts[rule] = entries
		}
	}
	return facts
}

// launchHarness is the harness a launch runs: the one the caller names, else
// the stored session's.
func (m *Manager) launchHarness(id domain.SessionID, harness domain.AgentHarness) string {
	if harness != "" {
		return string(harness)
	}
	return m.sessionHarness(id)
}

// nameListed reports whether key is one of names.
func nameListed(names []string, key string, caseInsensitive bool) bool {
	for _, n := range names {
		if n == key || (caseInsensitive && strings.EqualFold(n, key)) {
			return true
		}
	}
	return false
}
