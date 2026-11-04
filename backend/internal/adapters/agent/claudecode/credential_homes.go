package claudecode

import (
	"path/filepath"

	"tallpine.eu/tallpine/backend/internal/statefence"
)

// deny rule for Claude's own tools.
func applyCredentials(f *statefence.Credentials, settings map[string]any) {
	if f == nil {
		return
	}
	if sb, ok := settings["sandbox"].(map[string]any); ok {
		fs, _ := sb["filesystem"].(map[string]any)
		if fs != nil {
			cur, _ := fs["denyRead"].([]string)
			fs["denyRead"] = append(cur, f.Deny...)
			if len(f.ReAllow) > 0 {
				fs["allowRead"] = f.ReAllow
			}
		}
		if nw, _ := sb["network"].(map[string]any); nw != nil && len(f.Sockets) > 0 {
			cur, _ := nw["allowUnixSockets"].([]string)
			nw["allowUnixSockets"] = append(cur, f.Sockets...)
		}
	}
	perms, _ := settings["permissions"].(map[string]any)
	if perms == nil {
		perms = map[string]any{}
		settings["permissions"] = perms
	}
	deny, _ := perms["deny"].([]string)
	for _, p := range f.Deny {
		deny = append(deny, claudeReadRule(p), claudeReadRule(filepath.Join(p, "**")))
	}
	perms["deny"] = deny
}
