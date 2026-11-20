package agentlaunch

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// EnvNodeCapOptOut set to "0", "off" or "false" (project or daemon env) leaves
// NODE_OPTIONS and VITEST_MAX_WORKERS untouched.
const EnvNodeCapOptOut = "TALLPINE_NODE_CAP"

const (
	envNodeOptions     = "NODE_OPTIONS"
	envVitestMaxWorker = "VITEST_MAX_WORKERS"
)

//go:embed node_cap.cjs
var nodeCapSource []byte

// nodeCapPreload writes the preload under dataDir (content-addressed, so
// concurrent launches and upgrades are safe) and returns its path.
func nodeCapPreload(dataDir string) (string, error) {
	sum := sha256.Sum256(nodeCapSource)
	dir := filepath.Join(dataDir, "node-cap")
	path := filepath.Join(dir, "cap-"+hex.EncodeToString(sum[:6])+".cjs")
	if b, err := os.ReadFile(path); err == nil && bytes.Equal(b, nodeCapSource) {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, "cap-*.tmp")
	if err != nil {
		return "", err
	}
	_, werr := tmp.Write(nodeCapSource)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp.Name())
		if werr != nil {
			return "", werr
		}
		return "", cerr
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	return path, nil
}

// nodeOptionQuote quotes a NODE_OPTIONS value the way Node parses it
// (double quotes, backslash escapes), so paths with spaces survive.
func nodeOptionQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// applyNodeCaps caps Node-based test runners to par: VITEST_MAX_WORKERS (unless
// already set) and a --require preload appended to any existing NODE_OPTIONS.
func applyNodeCaps(env, projectEnv map[string]string, getenv func(string) string, par, dataDir string, find func(map[string]string, string) (string, string, bool)) {
	if n, err := strconv.Atoi(par); err != nil || n < 1 {
		return
	}
	optOut := func() bool {
		_, v, ok := find(env, EnvNodeCapOptOut)
		if !ok {
			_, v, ok = find(projectEnv, EnvNodeCapOptOut)
		}
		if !ok {
			v = getenv(EnvNodeCapOptOut)
		}
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "0", "off", "false":
			return true
		}
		return false
	}
	if optOut() {
		return
	}
	// A filtered launch must not get daemon values back through the fallback.
	unset := UnsetNames(env)
	inherit := func(k string) string {
		if Scrubbed(env) || slices.Contains(unset, k) {
			return ""
		}
		return getenv(k)
	}
	if _, _, ok := find(env, envVitestMaxWorker); !ok {
		if _, v, ok := find(projectEnv, envVitestMaxWorker); ok {
			env[envVitestMaxWorker] = v
		} else if inherit(envVitestMaxWorker) == "" {
			env[envVitestMaxWorker] = par
		}
	}
	if dataDir == "" {
		return
	}
	preload, err := nodeCapPreload(dataDir)
	if err != nil {
		return
	}
	key, cur, ok := find(env, envNodeOptions)
	if !ok {
		key, cur, ok = find(projectEnv, envNodeOptions)
	}
	if !ok {
		key, cur = envNodeOptions, inherit(envNodeOptions)
	}
	opt := "--require " + nodeOptionQuote(preload)
	if strings.Contains(cur, opt) {
		env[key] = cur
		return
	}
	env[key] = strings.TrimSpace(cur + " " + opt)
}
