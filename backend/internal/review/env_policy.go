package review

import (
	"context"
	"os"
	"runtime"
	"strings"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/envfilter"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/githubcreds"
)

// applyEnvFilter gives a reviewer the same environment policy as a worker: a
// reviewer reads untrusted PR content just as a worker does. Under the env
// allowlist it adds the daemon variables the allowlist keeps to env (existing
// entries win) and marks env as the whole environment; with the default policy
// it does nothing.
func (l *agentLauncher) applyEnvFilter(ctx context.Context, spec LaunchSpec, env map[string]string) {
	decision := l.decide(ctx, spec)
	out := envfilter.Of(decision)
	if !out.Allowlist() {
		return
	}
	windows := runtime.GOOS == "windows"
	res := out.ForHarness(string(spec.Harness)).Filter(os.Environ(), windows, githubcreds.Of(decision).WithheldVariables())
	for _, entry := range res.Keep {
		if key, value, ok := strings.Cut(entry, "="); ok && !hasEnvKey(env, key, windows) {
			env[key] = value
		}
	}
	env[agentlaunch.EnvScrub] = "1"
}

func hasEnvKey(env map[string]string, key string, fold bool) bool {
	if _, ok := env[key]; ok {
		return true
	}
	if fold {
		for existing := range env {
			if strings.EqualFold(existing, key) {
				return true
			}
		}
	}
	return false
}
