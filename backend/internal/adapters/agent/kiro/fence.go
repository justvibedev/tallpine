package kiro

import (
	"fmt"
	"runtime"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/statefence"
)

// fenceGOOS is the host OS the fence checks; a var so tests can pin it.
var fenceGOOS = runtime.GOOS

// HarnessPaths is kiro-cli's own state the Security mode fence leaves open:
// ~/.kiro, its Application Support dir (token store, run markers) and ~/.aws,
// whole and read-write, re-allowed under the hardened profile too. The fence
// protects Tallpine state, not the harness: Kiro keeps the access it has
// unfenced (sign-in incl. IAM Identity Center, settings, self-update), so a
// fenced session never breaks on a Kiro path the measurement missed.
var HarnessPaths = statefence.HarnessPaths{
	Write:    []string{"~/.kiro", kiroAppSupport, "~/.aws"},
	AuthRead: []string{"~/.aws"},
}

const kiroAppSupport = "~/Library/Application Support/kiro-cli"

// FencesState reports whether a Kiro session on goos runs inside the seatbelt
// fence: macOS with sandbox-exec present.
func FencesState(goos string) bool { return statefence.Available(goos) }

// FencesState implements ports.StateFencer for the terminal adapter.
func (p *Plugin) FencesState(goos string) bool { return FencesState(goos) }

// Fence wraps argv in the Security mode seatbelt when decision turns
// protectState on and the host can fence; otherwise argv is returned unchanged.
// env is the launch env (run file, data dir, TMPDIR, HOME) and workspace the
// directory the agent may write. A launch whose fence cannot be built fails.
func Fence(argv []string, env map[string]string, decision agentpolicy.Decision, workspace string) ([]string, error) {
	if !statefence.Applies(decision, fenceGOOS) {
		return argv, nil
	}
	profile, err := statefence.Profile(env, decision, workspace, HarnessPaths)
	if err != nil {
		return nil, fmt.Errorf("kiro: %w", err)
	}
	return statefence.Wrap(argv, profile), nil
}
