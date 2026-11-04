package deepseekharness

import (
	"errors"
	"fmt"
	"runtime"
	"strings"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/statefence"
)

// fenceGOOS is the host OS the fence checks; a var so tests can pin it.
var fenceGOOS = runtime.GOOS

// FullAccessMode is the one DSH_PERMISSION_MODE value that runs dsh with no
// sandbox of its own. dsh's sandbox-policy plugin accepts only read-only,
// workspace-write and danger-full-access; anything else fails its config.
const FullAccessMode = "danger-full-access"

// harnessPaths is dsh's own state the Security mode fence leaves open, whole
// and read-write, and re-allowed under the hardened profile too: ~/.dsh (the
// default DSH_HOME: sessions, credentials), ~/.config and the user caches. The
// fence protects Tallpine state, not the harness: dsh keeps the access it has
// unfenced. Tallpine's overlays under <dataDir>/dsh are not denied state, so
// dsh still reads them.
var harnessPaths = statefence.HarnessPaths{
	Write:    []string{"~/.dsh", "~/.config", "~/.cache", "~/Library/Caches"},
	AuthRead: []string{"~/.dsh", "~/.config"},
}

// HarnessPaths is harnessPaths plus the launch's DSH_HOME, if set.
func HarnessPaths(env map[string]string) statefence.HarnessPaths {
	out := statefence.HarnessPaths{
		Write:    append([]string(nil), harnessPaths.Write...),
		AuthRead: append([]string(nil), harnessPaths.AuthRead...),
	}
	if home := strings.TrimSpace(env["DSH_HOME"]); home != "" {
		out.Write = append(out.Write, home)
		out.AuthRead = append(out.AuthRead, home)
	}
	return out
}

// isBypass reports whether permissions (post-cap) is bypass, the one mode in
// which Tallpine launches dsh with its sandbox off.
func isBypass(permissions ports.PermissionMode) bool {
	return ports.NormalizePermissionMode(permissions) == ports.PermissionModeBypassPermissions
}

// EnvLiftsSandbox reports whether env sets DSH_PERMISSION_MODE to the one value
// that runs dsh unsandboxed. A Chat launch carries its whole sandbox choice in
// this env.
func EnvLiftsSandbox(env map[string]string) bool {
	return env[PermissionModeEnv] == FullAccessMode
}

// FencesLaunch is the one fence rule for the terminal adapter and the Chat
// driver: a dsh launch in permissions (post-cap) on goos runs inside the
// seatbelt only in bypass. Confined modes run dsh's own seatbelt for commands,
// and macOS refuses a sandbox inside a sandbox, so they keep the gap (refused
// under the hardened profile). Admission, the gap and the fence git config all
// read this answer, and Fence wraps exactly when it is true.
func FencesLaunch(goos string, permissions ports.PermissionMode) bool {
	return isBypass(permissions) && statefence.Available(goos)
}

// FencesLaunch implements ports.LaunchStateFencer for the terminal adapter.
func (p *Plugin) FencesLaunch(goos string, permissions ports.PermissionMode) bool {
	return FencesLaunch(goos, permissions)
}

// Fence wraps argv in the Security mode seatbelt when decision turns
// protectState on and FencesLaunch is true for permissions (post-cap);
// otherwise argv is returned unchanged. sandboxOff is whether this launch
// really runs dsh without its sandbox (the terminal's full-access patch or the
// final env's DSH_PERMISSION_MODE). When the fence applies and sandboxOff
// disagrees with the mode, the launch is refused: a bypass launch whose env
// keeps dsh's sandbox cannot be wrapped (nested sandbox) yet would be reported
// fenced, and a confined-mode launch whose env lifts the sandbox would run
// unsandboxed outside the fence while reported as a gap.
func Fence(argv []string, env map[string]string, decision agentpolicy.Decision, permissions ports.PermissionMode, sandboxOff bool, workspace string) ([]string, error) {
	if !statefence.Applies(decision, fenceGOOS) {
		return argv, nil
	}
	bypass := isBypass(permissions)
	if bypass != sandboxOff {
		return nil, fmt.Errorf("%w: %s=%q does not match the %s permission mode; unset it to launch under Security mode", ErrFenceMismatch, PermissionModeEnv, env[PermissionModeEnv], ports.NormalizePermissionMode(permissions))
	}
	if !bypass {
		return argv, nil
	}
	return FenceUnsandboxed(argv, env, decision, workspace)
}

// ErrFenceMismatch refuses a Security mode launch whose DSH_PERMISSION_MODE
// env contradicts its permission mode, so it cannot run as reported.
var ErrFenceMismatch = errors.New(adapterID + ": DSH_PERMISSION_MODE conflicts with the fence")

// FenceUnsandboxed wraps argv, a dsh process that runs no dsh sandbox, in the seatbelt when
// decision turns protectState on and the host can fence. dsh's patches, flags
// and env are never changed. A launch whose fence cannot be built fails.
func FenceUnsandboxed(argv []string, env map[string]string, decision agentpolicy.Decision, workspace string) ([]string, error) {
	if !statefence.Applies(decision, fenceGOOS) {
		return argv, nil
	}
	profile, err := statefence.Profile(env, decision, workspace, HarnessPaths(env))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", adapterID, err)
	}
	return statefence.Wrap(argv, profile), nil
}

// restoreFenceEnv is the env a restore is fenced against: the rebuilt
// statefence restore env plus the launch's DSH_HOME and DSH_PERMISSION_MODE.
func restoreFenceEnv(cfg ports.RestoreConfig) map[string]string {
	env := statefence.RestoreEnv(cfg.RunFilePath, cfg.DataDir, cfg.Session)
	for _, key := range []string{"DSH_HOME", PermissionModeEnv} {
		if v, ok := cfg.Env[key]; ok {
			env[key] = v
		}
	}
	return env
}

// terminalSandboxOff is whether a terminal launch runs dsh unsandboxed: bypass
// adds the full-access patch, which outranks the env; otherwise the env decides.
func terminalSandboxOff(permissions ports.PermissionMode, env map[string]string) bool {
	return isBypass(permissions) || EnvLiftsSandbox(env)
}
