package claudecode

import (
	"log/slog"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/protectstate"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/securitymode"
	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/daemonsock"
	"tallpine.eu/tallpine/backend/internal/statefence"
)

// protectStateApplies reports whether the session runs in the protectState
// sandbox: the policy is on and the host is macOS. The settings below rely on
// macOS-only sandbox keys (the allowUnixSockets path list, allowMachLookup):
// on Linux the bubblewrap sandbox blocks the daemon socket unless every Unix
// socket is opened, and Windows has no sandbox, so failIfUnavailable would stop
// every command. Elsewhere the session launches unchanged, as Codex does.
func protectStateApplies(decision agentpolicy.Decision, goos string) bool {
	return FencesState(goos) && protectstate.Of(decision).Enabled()
}

// ProtectStateApplies is protectStateApplies for the Claude chat driver, which
// sends the same fence as the terminal launch.
func ProtectStateApplies(decision agentpolicy.Decision, goos string) bool {
	return protectStateApplies(decision, goos)
}

// FencesState reports whether a Claude Code session on goos can run inside
// the protectState fence (terminal and chat alike): macOS only.
func FencesState(goos string) bool { return goos == "darwin" }

// FencesState implements ports.StateFencer for the terminal adapter.
func (p *Plugin) FencesState(goos string) bool { return FencesState(goos) }

// ProtectStateSettings is the protectState part of a Claude Code launch's
// settings: the sandbox and the Read deny rules for Tallpine state. Terminal
// launches merge it into their --settings flag and Claude chat sends it as the
// SDK's settings option, which claude-agent-acp loads into the same flag layer,
// so both fence alike. The caller decides whether protectState applies. No run
// file in env means the state cannot be located and the result is empty.
func ProtectStateSettings(env map[string]string) map[string]any {
	return ProtectStateSettingsFor(env, agentpolicy.Decision{})
}

// ProtectStateSettingsFor is ProtectStateSettings plus what decision adds: under
// the hardened profile, the developer credential homes are denied too.
func ProtectStateSettingsFor(env map[string]string, decision agentpolicy.Decision) map[string]any {
	settings := map[string]any{}
	denyDaemonTokenRead(settings, env)
	protectStateSandbox(settings, env, decision)
	if _, fenced := settings["sandbox"]; fenced {
		applyCredentials(statefence.CredentialsFor(decision, env), settings)
	}
	return settings
}

// mergeProtectState adds ProtectStateSettings to a launch's settings, keeping
// any permission keys the settings already carry.
func mergeProtectState(settings map[string]any, env map[string]string, decision agentpolicy.Decision) {
	for key, value := range ProtectStateSettingsFor(env, decision) {
		if key != "permissions" {
			settings[key] = value
			continue
		}
		perms, _ := settings["permissions"].(map[string]any)
		if perms == nil {
			perms = map[string]any{}
			settings["permissions"] = perms
		}
		fence, _ := value.(map[string]any)
		for k, v := range fence {
			perms[k] = v
		}
	}
}

// trustdService is the macOS certificate trust daemon. Go programs (gh) verify
// TLS through it; Claude's sandbox blocks the lookup unless it is allowed, and
// gh then fails with "x509: OSStatus -26276".
const trustdService = "com.apple.trustd.agent"

// coreDeviceService is the one Mach service `xcrun devicectl` needs. Measured:
// without it devicectl list devices times out under the fence; with only this
// entry (trustd retained) it passes.
const coreDeviceService = "com.apple.CoreDevice.CoreDeviceService"

// protectStateSandbox turns on Claude Code's sandbox for the session's shell
// with reads of Tallpine state denied (the protectState policy). It is a file
// fence only: every network domain stays allowed. The rest is what normal work
// needs, found by running git push, gh, npm install, go build and the tallpine
// CLI under the sandbox:
//   - the daemon socket directory, so the CLI reaches the daemon with the
//     session's scoped token (it never needs the token file);
//   - the trust daemon, so gh can verify TLS;
//   - writes to the session's temp dir, its artifacts and the usual build and
//     package caches (the worktree, including a linked worktree's git dir, is
//     writable by default).
//
// allowUnsandboxedCommands is off so the agent cannot retry a denied command
// outside the sandbox, and failIfUnavailable makes a host without a working
// sandbox refuse to run commands instead of running them unfenced. No run file
// means the state cannot be located and nothing is added.
func protectStateSandbox(settings map[string]any, env map[string]string, decision agentpolicy.Decision) {
	runFile := strings.TrimSpace(env[brand.Env("RUN_FILE")])
	if runFile == "" {
		slog.Warn("protectState is on but the launch has no run file; the session runs without the sandbox")
		return
	}
	state := statefence.StatePaths(runFile, env[brand.Env("DATA_DIR")])
	denyRead := append(append([]string{}, state.Files...), state.Dirs...)
	devTooling := protectstate.Of(decision).DevTooling()
	sockets := []string{filepath.Dir(daemonsock.Path(runFile))}
	// devTooling: go test servers and the merge gate bind unix sockets in the
	// session's own temp dir; bind is denied anywhere not listed. Only that one
	// dir (already writable), never the data dir, /tmp or the home.
	if devTooling {
		sockets = append(sockets, statefence.DevToolingSocketDirs(env, statefence.DataDir(env))...)
	}
	if securitymode.Of(decision).AllowDocker() {
		sockets = append(sockets, statefence.DockerSocketPaths(env)...)
	}
	settings["sandbox"] = map[string]any{
		"enabled":                  true,
		"allowUnsandboxedCommands": false,
		"failIfUnavailable":        true,
		"filesystem": map[string]any{
			"denyRead":   denyRead,
			"allowWrite": statefence.WritePaths(env, devTooling),
		},
		"network": map[string]any{
			"allowedDomains": []string{"*"},
			// Dev servers, test servers (httptest, vitest) and port probes bind
			// loopback; the sandbox blocks that unless allowed. The Docker socket
			// is listed only when the project set securityMode.allowDocker.
			"allowLocalBinding": true,
			"allowUnixSockets":  sockets,
			"allowMachLookup":   []string{trustdService, coreDeviceService},
		},
	}
}

// protectedStateReadRules lists the Read deny rules for the state rooted at the
// run file's directory. dataDir is the launch's TALLPINE_DATA_DIR; empty means
// the default "data" directory under that root.
func protectedStateReadRules(runFile, dataDir string) []string {
	state := statefence.StatePaths(runFile, dataDir)
	rules := make([]string, 0, len(state.Files)+len(state.Dirs))
	for _, f := range state.Files {
		rules = append(rules, claudeReadRule(f))
	}
	for _, d := range state.Dirs {
		rules = append(rules, claudeReadRule(filepath.Join(d, "**")))
	}
	return rules
}
