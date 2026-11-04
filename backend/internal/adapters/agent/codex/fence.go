package codex

import (
	"fmt"
	"runtime"
	"slices"
	"strings"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/statefence"
	"tallpine.eu/tallpine/backend/pkg/agentruntime"
)

// fenceGOOS is the host OS the fence checks; a var so tests can pin it.
var fenceGOOS = runtime.GOOS

// harnessPaths is Codex's own state the Security mode fence leaves open, whole
// and read-write, and re-allowed under the hardened profile too: ~/.codex (the
// device CODEX_HOME), ~/.aws and ~/.config (provider sign-ins and tool config),
// and the user caches. The fence protects Tallpine state, not the harness:
// Codex keeps the access it has unfenced, so a fenced session never breaks on a
// Codex path a measurement missed.
var harnessPaths = statefence.HarnessPaths{
	Write:    []string{"~/.codex", "~/.aws", "~/.config", "~/.cache", "~/Library/Caches"},
	AuthRead: []string{"~/.codex", "~/.aws", "~/.config"},
}

// HarnessPaths is harnessPaths plus the launch's CODEX_HOME (an agent-account
// home), which Codex reads and writes like ~/.codex.
func HarnessPaths(env map[string]string) statefence.HarnessPaths {
	out := statefence.HarnessPaths{
		Write:    append([]string(nil), harnessPaths.Write...),
		AuthRead: append([]string(nil), harnessPaths.AuthRead...),
	}
	if home := strings.TrimSpace(env["CODEX_HOME"]); home != "" {
		out.Write = append(out.Write, home)
		out.AuthRead = append(out.AuthRead, home)
	}
	return out
}

// restoreFenceEnv is the env a restore is fenced against: the rebuilt
// statefence restore env plus the launch's account CODEX_HOME, if any.
func restoreFenceEnv(cfg ports.RestoreConfig) map[string]string {
	env := statefence.RestoreEnv(cfg.RunFilePath, cfg.DataDir, cfg.Session)
	if home := cfg.Env["CODEX_HOME"]; home != "" {
		env["CODEX_HOME"] = home
	}
	return env
}

// FencesLaunch reports whether a Codex launch in permissions on goos runs
// inside the seatbelt fence: only a launch with no Codex sandbox does. Accept
// edits and auto run Codex's own seatbelt for commands, and macOS refuses a
// sandbox inside a sandbox, so those stay unfenced (a gap, refused under the
// hardened profile).
func FencesLaunch(goos string, permissions ports.PermissionMode) bool {
	return RunsUnsandboxed(permissions) && statefence.Available(goos)
}

// FencesLaunch implements ports.LaunchStateFencer for the terminal adapter.
func (p *Plugin) FencesLaunch(goos string, permissions ports.PermissionMode) bool {
	return FencesLaunch(goos, permissions)
}

// RunsUnsandboxed reports whether Codex launched in permissions runs with no
// sandbox of its own. It reads the flags the launch actually gets, so default,
// empty and unknown modes (Codex's full-access baseline) count with bypass.
func RunsUnsandboxed(permissions ports.PermissionMode) bool {
	return slices.Contains(agentruntime.CodexPermissionArgs(agentruntime.PermissionPolicy(permissions)), "--dangerously-bypass-approvals-and-sandbox")
}

// Fence wraps argv in the Security mode seatbelt when decision turns
// protectState on, permissions (the mode Codex launches in, after the cap) runs
// no Codex sandbox and the host can fence; otherwise argv is returned unchanged. Codex's
// own sandbox and approval flags are never changed. env is the launch env and
// workspace the directory the agent may write. A launch whose fence cannot be
// built fails.
func Fence(argv []string, env map[string]string, decision agentpolicy.Decision, permissions ports.PermissionMode, workspace string) ([]string, error) {
	if !RunsUnsandboxed(permissions) {
		return argv, nil
	}
	return FenceUnsandboxed(argv, env, decision, workspace)
}

// FenceUnsandboxed wraps argv, a Codex process that runs no Codex sandbox of
// its own (callers decide that from their mode mapping), in the seatbelt when
// decision turns protectState on and the host can fence.
func FenceUnsandboxed(argv []string, env map[string]string, decision agentpolicy.Decision, workspace string) ([]string, error) {
	if !statefence.Applies(decision, fenceGOOS) {
		return argv, nil
	}
	profile, err := statefence.Profile(env, decision, workspace, HarnessPaths(env))
	if err != nil {
		return nil, fmt.Errorf("codex: %w", err)
	}
	return statefence.Wrap(argv, profile), nil
}
