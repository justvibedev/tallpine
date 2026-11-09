package claudecode

import (
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

// RequireFence refuses a launch protectState must fence on goos when env cannot
// locate the state to deny (statefence.ErrNoRunFile). The settings builders
// below have no error path, so every Claude launch (terminal, restore, chat)
// runs this first instead of starting with an empty fence.
func RequireFence(decision agentpolicy.Decision, env map[string]string, goos string) error {
	if !protectStateApplies(decision, goos) {
		return nil
	}
	return statefence.RequireRunFile(env)
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
// means the state cannot be located and nothing is added; RequireFence has
// already refused such a launch.
func protectStateSandbox(settings map[string]any, env map[string]string, decision agentpolicy.Decision) {
	runFile := strings.TrimSpace(env[brand.Env("RUN_FILE")])
	if runFile == "" {
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

// denyDaemonTokenRead adds Claude Code permission deny rules for Tallpine's
// secrets and store: the install token (daemontoken.Path of the launch's run
// file), the session key that scoped tokens are MACed under, the database, the
// GitHub PAT, the browser runtime key and the Electron profile (cookies). The
// agent holds a scoped session token and talks to the daemon over its socket, so
// it never needs any of these, and all sit readable by the same OS user. This is
// a guard for Claude's own tools, not a sandbox: a shell command can still cat
// the files. No run file in the launch env means no rule.
func denyDaemonTokenRead(settings map[string]any, env map[string]string) {
	runFile := strings.TrimSpace(env[brand.Env("RUN_FILE")])
	if runFile == "" {
		return
	}
	perms, _ := settings["permissions"].(map[string]any)
	if perms == nil {
		perms = map[string]any{}
		settings["permissions"] = perms
	}
	perms["deny"] = protectedStateReadRules(runFile, env[brand.Env("DATA_DIR")])
}

// claudeReadRule is a Read permission rule for an absolute path. Claude Code
// treats "/x" as relative to the settings root; an absolute filesystem path
// needs "//x", and a Windows drive path is written "//c/Users/...".
func claudeReadRule(path string) string {
	p := filepath.ToSlash(path)
	if len(p) >= 2 && p[1] == ':' {
		p = "/" + strings.ToLower(p[:1]) + p[2:]
	}
	return "Read(/" + p + ")"
}
