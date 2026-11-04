package sessionreap

import "bytes"

var (
	markerPrefix    = []byte(MarkerEnv + "=")
	userShellPrefix = []byte(UserShellEnv + "=")
)

// markerFrom returns the MarkerEnv value among NUL-split env entries. An
// empty entry ends the environment.
func markerFrom(env [][]byte) string {
	marker, _ := envFacts(env)
	return marker
}

// envFacts returns the MarkerEnv value and whether UserShellEnv is set to a
// non-empty value.
func envFacts(env [][]byte) (marker string, userShell bool) {
	for _, kv := range env {
		if len(kv) == 0 {
			break
		}
		switch {
		case bytes.HasPrefix(kv, markerPrefix):
			marker = string(kv[len(markerPrefix):])
		case bytes.HasPrefix(kv, userShellPrefix):
			userShell = len(kv) > len(userShellPrefix)
		}
	}
	return marker, userShell
}
