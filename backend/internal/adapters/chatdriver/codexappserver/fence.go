package codexappserver

import (
	"runtime"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/codex"
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/protectstate"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/statefence"
)

// fenceGOOS is the host OS the driver fences for; tests swap it.
var fenceGOOS = runtime.GOOS

// FencesLaunch implements ports.LaunchStateFencer. A thread in mode (already
// capped by the caller) runs the app-server inside the Security mode seatbelt
// only when its Codex sandbox is danger-full-access: workspace-write nests
// Codex's own seatbelt, which macOS refuses inside another sandbox.
func (d *Driver) FencesLaunch(goos string, mode ports.PermissionMode) bool {
	return !launchSandboxed(agentpolicy.Decision{}, mode, false) && statefence.Available(goos)
}

// launchSandboxed reports whether a thread launched in mode under decision runs
// a Codex sandbox (read-only or workspace-write). The mode is capped as the
// thread params cap it, then judged by the one rule the terminal adapter uses.
func launchSandboxed(decision agentpolicy.Decision, mode ports.PermissionMode, readOnly bool) bool {
	return readOnly || !codex.RunsUnsandboxed(permissioncap.Of(decision).LowerLive(domain.HarnessCodex, mode))
}

// appServerFence returns the argv rewrite for one app-server launch: the
// seatbelt when Security mode fences it, unchanged otherwise. A launch the
// hardened profile requires fenced but whose mode runs a Codex sandbox is
// refused here too, so no caller can start it unfenced. Codex's sandbox and
// approval settings are not changed.
func appServerFence(decision agentpolicy.Decision, mode ports.PermissionMode, readOnly bool, workspace string) func([]string, map[string]string) ([]string, error) {
	sandboxed := launchSandboxed(decision, mode, readOnly)
	return func(argv []string, env map[string]string) ([]string, error) {
		if err := protectstate.Of(decision).Admit(!sandboxed && statefence.Available(fenceGOOS), string(domain.HarnessCodex), fenceGOOS); err != nil {
			return nil, err
		}
		if sandboxed {
			return argv, nil
		}
		return codex.FenceUnsandboxed(argv, env, decision, workspace)
	}
}
