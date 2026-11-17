package agentlaunch

import (
	"strconv"
	"strings"
)

// EnvTestParallelism is the test-parallelism hint agents pass to jest, vitest,
// pytest -n and go test -p. EnvGOMAXPROCS caps Go's scheduler to the same value.
const (
	EnvTestParallelism = "TALLPINE_TEST_PARALLELISM"
	EnvGOMAXPROCS      = "GOMAXPROCS"
)

// DefaultTestParallelism is max(2, cores/4): agents share the machine.
func DefaultTestParallelism(cores int) int {
	return max(2, cores/4)
}

// ApplyParallelismDefaults sets GOMAXPROCS and TALLPINE_TEST_PARALLELISM in env
// when neither the project env nor the daemon env (getenv) defines them. A
// project value is copied into env (the reserved-prefix merge drops it); a
// daemon value is inherited as is, and a daemon TALLPINE_TEST_PARALLELISM also
// feeds the GOMAXPROCS default.
func ApplyParallelismDefaults(env, projectEnv map[string]string, getenv func(string) string, cores int, caseInsensitive bool) {
	lookup := func(m map[string]string, key string) (string, bool) {
		if v, ok := m[key]; ok {
			return v, true
		}
		if caseInsensitive {
			for k, v := range m {
				if strings.EqualFold(k, key) {
					return v, true
				}
			}
		}
		return "", false
	}
	var par string
	if v, ok := lookup(env, EnvTestParallelism); ok {
		par = v
	} else if v, ok := lookup(projectEnv, EnvTestParallelism); ok {
		par = v
		env[EnvTestParallelism] = v
	} else if v := getenv(EnvTestParallelism); v != "" {
		par = v
	} else {
		par = strconv.Itoa(DefaultTestParallelism(cores))
		env[EnvTestParallelism] = par
	}
	if _, ok := lookup(env, EnvGOMAXPROCS); ok {
		return
	}
	if v, ok := lookup(projectEnv, EnvGOMAXPROCS); ok {
		env[EnvGOMAXPROCS] = v
		return
	}
	if getenv(EnvGOMAXPROCS) != "" {
		return
	}
	env[EnvGOMAXPROCS] = par
}
