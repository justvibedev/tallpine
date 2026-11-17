package skillbridge

import (
	"path/filepath"
	"reflect"
	"testing"
)

// Plugins come from the account's Claude config: user installs and installs
// for this workspace, with the account's enabledPlugins value; installs for
// another project are left out.
func TestDiscoverPluginsReadsClaudeAccountConfig(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()
	claude := filepath.Join(home, ".claude")
	userPath := filepath.Join(claude, "plugins", "cache", "m", "design", "1.0.0")
	write(t, filepath.Join(userPath, ".claude-plugin", "plugin.json"), `{"name":"design","description":"  Design\n help  "}`)
	write(t, filepath.Join(claude, "plugins", "installed_plugins.json"), `{"version":2,"plugins":{
		"design@m":[{"scope":"user","installPath":"`+userPath+`","version":"1.0.0"}],
		"lsp@m":[{"scope":"project","projectPath":"`+ws+`","installPath":"/x/lsp","version":"0.2"}],
		"other@m":[{"scope":"local","projectPath":"/elsewhere","installPath":"/x/other","version":"9"}]
	}}`)
	write(t, filepath.Join(claude, "settings.json"), `{"enabledPlugins":{"design@m":true,"lsp@m":false,"gone@n":true}}`)

	got := DiscoverPlugins(Options{Workspace: ws, Home: home})
	want := []Plugin{
		{ID: "design@m", Name: "design", Marketplace: "m", Description: "Design help", Version: "1.0.0", Path: userPath, Installed: true, AccountEnabled: true},
		{ID: "gone@n", Name: "gone", Marketplace: "n", AccountEnabled: true},
		{ID: "lsp@m", Name: "lsp", Marketplace: "m", Version: "0.2", Path: "/x/lsp", Installed: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plugins:\n got %+v\nwant %+v", got, want)
	}
	// A project CLAUDE_CONFIG_DIR replaces ~/.claude, as for skills.
	if got := DiscoverPlugins(Options{Workspace: ws, Home: home, ClaudeConfigDir: t.TempDir()}); len(got) != 0 {
		t.Fatalf("CLAUDE_CONFIG_DIR without plugins listed %+v", got)
	}
	if inv := Discover(Options{Workspace: ws, Home: home}); len(inv.Plugins) != 3 {
		t.Fatalf("Discover plugins = %+v", inv.Plugins)
	}
}

// A malformed index or settings file lists nothing rather than failing.
func TestDiscoverPluginsMalformedConfig(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), `{not json`)
	write(t, filepath.Join(home, ".claude", "settings.json"), `[]`)
	if got := DiscoverPlugins(Options{Home: home}); len(got) != 0 {
		t.Fatalf("malformed config listed %+v", got)
	}
}
