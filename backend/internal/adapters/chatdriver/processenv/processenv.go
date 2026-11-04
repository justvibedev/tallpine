// Package processenv builds child-process environments shared by Chat drivers.
package processenv

import (
	"os"
	"runtime"
	"sort"
	"strings"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
)

// Merge overlays session-specific values on the daemon environment and returns
// the KEY=VALUE form expected by os/exec. Sorting makes launches deterministic
// enough to inspect and compare in tests and process diagnostics.
//
// The reserved keys agentlaunch.EnvUnset and agentlaunch.EnvScrub in overlay
// name daemon variables to drop from that base, or make the overlay the whole
// environment (the agent policy's GitHub helper mode and env allowlist); the
// keys themselves are never passed on.
func Merge(overlay map[string]string) []string {
	var base []string
	if !agentlaunch.Scrubbed(overlay) {
		base = agentlaunch.FilterUnset(os.Environ(), agentlaunch.UnsetNames(overlay))
	}
	return merge(base, agentlaunch.WithoutControlKeys(overlay), runtime.GOOS == "windows")
}

func merge(environ []string, overlay map[string]string, caseInsensitive bool) []string {
	merged := make(map[string]string, len(environ)+len(overlay))
	for _, entry := range environ {
		if key, _, ok := strings.Cut(entry, "="); ok {
			if caseInsensitive {
				key = strings.ToUpper(key)
			}
			merged[key] = entry
		}
	}
	keys := make([]string, 0, len(overlay))
	for key := range overlay {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if caseInsensitive && strings.EqualFold(keys[i], "PATH") != strings.EqualFold(keys[j], "PATH") {
			return !strings.EqualFold(keys[i], "PATH")
		}
		if caseInsensitive && (keys[i] == "PATH") != (keys[j] == "PATH") {
			return keys[j] == "PATH"
		}
		return keys[i] < keys[j]
	})
	for _, key := range keys {
		value := overlay[key]
		entry := key + "=" + value
		if caseInsensitive {
			key = strings.ToUpper(key)
		}
		merged[key] = entry
	}

	out := make([]string, 0, len(merged))
	for _, entry := range merged {
		out = append(out, entry)
	}
	sort.Strings(out)
	return out
}
