package skillbridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ScopePlugin is a Claude Code plugin from the account's Claude config. A
// plugin is switched per role through the project's claudePlugins setting,
// not bridged.
const ScopePlugin Scope = "plugin"

// maxPluginFileBytes bounds the Claude config files read for plugins; the
// installed-plugins index of a busy account is tens of KiB.
const maxPluginFileBytes = 4 << 20

// Plugin is one Claude Code plugin the account offers a project. ID is the
// enabledPlugins key ("name@marketplace"). AccountEnabled is the account's own
// enabledPlugins value, which a role without a claudePlugins override keeps.
type Plugin struct {
	ID             string
	Name           string
	Marketplace    string
	Description    string
	Version        string
	Path           string
	Installed      bool
	AccountEnabled bool
}

// installedPlugins is <claude>/plugins/installed_plugins.json (version 2).
type installedPlugins struct {
	Plugins map[string][]struct {
		Scope       string `json:"scope"`
		ProjectPath string `json:"projectPath"`
		InstallPath string `json:"installPath"`
		Version     string `json:"version"`
	} `json:"plugins"`
}

// claudeDir is the Claude config directory sessions of o see.
func (o Options) claudeDir() string {
	if o.ClaudeConfigDir != "" {
		return o.ClaudeConfigDir
	}
	if o.Home != "" {
		return filepath.Join(o.Home, ".claude")
	}
	return ""
}

// DiscoverPlugins lists the Claude Code plugins the account config offers the
// workspace: user-scope installs, project or local installs for this
// workspace, and any plugin the account's enabledPlugins names. It only reads;
// missing or malformed files yield no plugins. Sorted by ID.
func DiscoverPlugins(o Options) []Plugin {
	dir := o.claudeDir()
	if dir == "" {
		return nil
	}
	enabled := map[string]bool{}
	var settings struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	if readJSON(filepath.Join(dir, "settings.json"), &settings) {
		enabled = settings.EnabledPlugins
	}
	byID := map[string]*Plugin{}
	get := func(id string) *Plugin {
		if p, ok := byID[id]; ok {
			return p
		}
		name, market, _ := strings.Cut(id, "@")
		p := &Plugin{ID: id, Name: name, Marketplace: market, AccountEnabled: enabled[id]}
		byID[id] = p
		return p
	}
	var index installedPlugins
	if readJSON(filepath.Join(dir, "plugins", "installed_plugins.json"), &index) {
		for id, installs := range index.Plugins {
			if strings.TrimSpace(id) == "" {
				continue
			}
			for _, in := range installs {
				if in.Scope != "user" && (o.Workspace == "" || in.ProjectPath == "" || !samePath(in.ProjectPath, o.Workspace)) {
					continue
				}
				p := get(id)
				if p.Installed && in.Scope != "user" {
					continue // the user install wins over a project copy
				}
				p.Installed, p.Version, p.Path = true, in.Version, in.InstallPath
				p.Description = pluginDescription(in.InstallPath)
			}
		}
	}
	for id := range enabled {
		if strings.TrimSpace(id) != "" {
			get(id)
		}
	}
	out := make([]Plugin, 0, len(byID))
	for _, p := range byID {
		out = append(out, *p)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID < out[b].ID })
	return out
}

// pluginDescription reads the description of an installed plugin's manifest.
func pluginDescription(installPath string) string {
	if installPath == "" {
		return ""
	}
	var manifest struct {
		Description string `json:"description"`
	}
	if !readJSON(filepath.Join(installPath, ".claude-plugin", "plugin.json"), &manifest) {
		return ""
	}
	return strings.Join(strings.Fields(manifest.Description), " ")
}

func readJSON(path string, v any) bool {
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() > maxPluginFileBytes {
		return false
	}
	b, err := os.ReadFile(path) //nolint:gosec // a fixed file under the account's Claude config dir
	return err == nil && json.Unmarshal(b, v) == nil
}
