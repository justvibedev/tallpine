package deepseekharness

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// HooksPluginVersion is the dsh release whose headless profile
// this overlay was loaded against with `dsh --dump-config`. Documentation only:
// nothing enforces it. The payload shapes were read from the upstream source at
// 0.2.2-alpha.2 (packages/experimental/hooks-claude-code), not from 0.2.1-rc.2.
//
// dsh does NOT ship the plugin: it is only a devDependency of @deepseek-ai/dsh,
// so mounting it by package name fails with ERR_MODULE_NOT_FOUND ("1 entry did
// not activate") and no hook ever fires. Tallpine therefore ships it, and
// dsh-hook-protocol (also dev-only), embedded in the binary and lays them out
// under dataDir/dsh/hooks-plugin (EnsureHooksPlugin) with no network. The layout
// records the dsh install it was built against and is rebuilt when that changes.
// The overlay names the absolute entry file; with no install it is skipped.
//
// EXPERIMENTAL. The plugin runs unmodified Claude Code command hooks and is not
// mounted by the headless profile, so Tallpine inserts it with a patch overlay.
//
// Known gaps (upstream): transcript_path is always empty; there is no
// permission-request event, so terminal approvals stay degraded (ACP chat
// approvals are unaffected); child agents' own hooks carry the child session_id,
// which lifecycle drops (manager.go), assuming the root id does not rotate
// within one launch.
const HooksPluginVersion = "0.2.1-rc.2"

// hooksPluginName is the cordis package the overlay mounts.
const hooksPluginName = "@deepseek-ai/dsh-hooks-claude-code"

// hooksPluginEntry is the installed plugin's module file under dataDir.
func hooksPluginEntry(dataDir string) string {
	return filepath.Join(dataDir, "dsh", "hooks-plugin", "node_modules", "@deepseek-ai", "dsh-hooks-claude-code", "lib", "index.js")
}

//go:embed assets/hooks-claude-code.index.js assets/hooks-claude-code.package.json assets/hooks-claude-code.LICENSE assets/hook-protocol.index.js assets/hook-protocol.package.json
var pluginAssets embed.FS

// dshScopeOf finds the @deepseek-ai directory of the dsh install behind binary.
func dshScopeOf(dshBinary string) (string, error) {
	if runtime.GOOS == "windows" {
		return "", errors.New("hooks plugin needs symlinks; unsupported on windows")
	}
	resolved, err := filepath.EvalSymlinks(dshBinary)
	if err != nil {
		return "", err
	}
	for d := filepath.Dir(resolved); d != filepath.Dir(d); d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "node_modules", "@deepseek-ai", "dsh-llm")); err == nil {
			return filepath.Join(d, "node_modules", "@deepseek-ai"), nil
		}
	}
	return "", errors.New("dsh's own node_modules not found")
}

// dshIdentity names the dsh install a layout is built against: its scope path and
// version. A variable so tests stub it.
var dshIdentity = func(dshBinary string) (string, error) {
	scope, err := dshScopeOf(dshBinary)
	if err != nil {
		return "", err
	}
	version := ""
	if data, err := os.ReadFile(filepath.Join(scope, "..", "..", "package.json")); err == nil { //nolint:gosec // dsh install path
		var pkg struct {
			Version string `json:"version"`
		}
		_ = json.Unmarshal(data, &pkg)
		version = pkg.Version
	}
	return scope + "\n" + version, nil
}

const hooksMarker = ".dsh-identity"

// installHooksPlugin lays the embedded plugin files (no network) under dir and
// symlinks every other @deepseek-ai package from dsh's own install, so the plugin
// shares dsh's single instances of cordis and the session services. A variable
// so tests stub the dsh tree lookup.
var installHooksPlugin = func(_ context.Context, dir, dshBinary string) error {
	dshScope, err := dshScopeOf(dshBinary)
	if err != nil {
		return err
	}
	scope := filepath.Join(dir, "node_modules", "@deepseek-ai")
	if err := os.RemoveAll(filepath.Join(dir, "node_modules")); err != nil {
		return err
	}
	if err := os.MkdirAll(scope, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(dshScope)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == "dsh-hooks-claude-code" || e.Name() == "dsh-hook-protocol" {
			continue
		}
		if err := os.Symlink(filepath.Join(dshScope, e.Name()), filepath.Join(scope, e.Name())); err != nil {
			return err
		}
	}
	files := map[string]string{
		"dsh-hooks-claude-code/lib/index.js": "assets/hooks-claude-code.index.js",
		"dsh-hooks-claude-code/package.json": "assets/hooks-claude-code.package.json",
		"dsh-hooks-claude-code/LICENSE":      "assets/hooks-claude-code.LICENSE",
		"dsh-hook-protocol/lib/index.js":     "assets/hook-protocol.index.js",
		"dsh-hook-protocol/package.json":     "assets/hook-protocol.package.json",
	}
	for dst, src := range files {
		data, err := pluginAssets.ReadFile(src)
		if err != nil {
			return err
		}
		p := filepath.Join(scope, filepath.FromSlash(dst))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(p, data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

var (
	hooksInstallMu     sync.Mutex
	hooksInstallFailed = map[string]bool{} // plugin dirs whose install failed this process
)

// EnsureHooksPlugin lays out the bundled hooks plugin once. Calls are serialized,
// the install is built in a temp dir and renamed into place so a partial layout
// never counts as installed, and a failure is remembered for the process so later
// launches skip it. Best effort: without the plugin hooksArgs skips the overlay.
func EnsureHooksPlugin(ctx context.Context, dataDir, dshBinary string) {
	if dataDir == "" {
		return
	}
	dir := filepath.Join(dataDir, "dsh", "hooks-plugin")
	hooksInstallMu.Lock()
	defer hooksInstallMu.Unlock()
	if hooksInstallFailed[dir] {
		return
	}
	identity, err := dshIdentity(dshBinary)
	if err != nil {
		hooksInstallFailed[dir] = true
		return
	}
	if got, err := os.ReadFile(filepath.Join(dir, hooksMarker)); err == nil && string(got) == identity { //nolint:gosec // Tallpine-owned
		if _, err := os.Stat(hooksPluginEntry(dataDir)); err == nil {
			return
		}
	}
	tmp := dir + ".tmp"
	_ = os.RemoveAll(tmp)
	_ = os.RemoveAll(dir)
	if err := installHooksPlugin(ctx, tmp, dshBinary); err != nil ||
		os.WriteFile(filepath.Join(tmp, hooksMarker), []byte(identity), 0o600) != nil {
		_ = os.RemoveAll(tmp)
		hooksInstallFailed[dir] = true
		return
	}
	if err := os.Rename(tmp, dir); err != nil {
		_ = os.RemoveAll(tmp)
		hooksInstallFailed[dir] = true
	}
}

// hookCommandPrefix is the command every generated hook runs.
const hookCommandPrefix = "tallpine hooks " + string(domain.HarnessDeepSeek) + " "

// hookEvents maps the Claude Code hook names the plugin fires to the event
// tokens `tallpine hooks deepseek-harness <event>` receives.
var hookEvents = []struct{ Native, Event string }{
	{"SessionStart", "session-start"},
	{"UserPromptSubmit", "user-prompt-submit"},
	{"PreToolUse", "pre-tool-use"},
	{"PostToolUse", "post-tool-use"},
	{"Stop", "stop"},
	{"SubagentStart", "subagent-start"},
	{"SubagentStop", "subagent-stop"},
}

// hooksJSON renders the Claude-format hook config the plugin reads.
func hooksJSON() string {
	type command struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	type group struct {
		Hooks []command `json:"hooks"`
	}
	out := map[string][]group{}
	for _, e := range hookEvents {
		out[e.Native] = []group{{Hooks: []command{{Type: "command", Command: hookCommandPrefix + e.Event}}}}
	}
	b, _ := json.MarshalIndent(map[string]any{"hooks": out}, "", "  ")
	return string(b) + "\n"
}

// hooksPatch inserts the hooks plugin pointed at an absolute hooks.json path
// (a relative configPath resolves from dsh's launch directory).
func hooksPatch(configPath, pluginEntry string) string {
	return fmt.Sprintf(`# Written by Tallpine. Mounts dsh's Claude-compatible hooks plugin (experimental,
# checked against dsh %s).
- insert:
    - id: hooks-claude-code
      name: %q
      config:
        configPath: %q
`, HooksPluginVersion, pluginEntry, configPath)
}

// hooksArgs writes hooks.json and hooks.patch.yml under dataDir/dsh and returns
// the --patch flag for the overlay.
func hooksArgs(dataDir string) ([]string, error) {
	entry, err := filepath.Abs(hooksPluginEntry(dataDir))
	if err != nil {
		return nil, fmt.Errorf("deepseek-harness: resolve hooks plugin: %w", err)
	}
	if _, err := os.Stat(entry); err != nil {
		return nil, nil //nolint:nilerr // plugin not installed: dsh would only warn that the entry did not activate
	}
	configPath, err := writeOverlay(dataDir, "hooks.json", hooksJSON())
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return nil, fmt.Errorf("deepseek-harness: resolve hooks.json: %w", err)
	}
	path, err := writeOverlay(dataDir, "hooks.patch.yml", hooksPatch(abs, entry))
	if err != nil {
		return nil, err
	}
	return []string{"--patch", path}, nil
}
