package previewserver

import (
	"fmt"
	"os"
	"runtime"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/statefence"
)

// Fence is the Security mode input for one preview start. launch.json lives in
// the workspace the agent can edit, so a start the human clicks still runs
// agent-chosen argv: the server runs in the session's seatbelt whoever asks. The
// zero value runs unfenced, as with protectState off.
type Fence struct {
	Decision  agentpolicy.Decision
	RunFile   string
	DataDir   string
	SessionID domain.SessionID
}

// resolve returns the seatbelt profile and the env the fenced server needs, or
// an empty profile when the host or policy does not fence. A fence that cannot
// be built fails the start rather than running the server outside it.
func (f Fence) resolve(workspace string) (profile string, env []string, err error) {
	if !statefence.Applies(f.Decision, runtime.GOOS) {
		return "", nil, nil
	}
	fenceEnv := statefence.RestoreEnv(f.RunFile, f.DataDir, ports.SessionRef{ID: string(f.SessionID), DataDir: f.DataDir})
	profile, err = statefence.Profile(fenceEnv, f.Decision, workspace, statefence.HarnessPaths{})
	if err != nil {
		return "", nil, serviceError("PREVIEW_FENCE_UNAVAILABLE", fmt.Sprintf("build the Security mode fence: %v", err))
	}
	// The daemon's TMPDIR is outside the writable set; the scratch dir is inside.
	if tmp := fenceEnv["TMPDIR"]; tmp != "" {
		if err := os.MkdirAll(tmp, 0o700); err != nil {
			return "", nil, serviceError("PREVIEW_FENCE_UNAVAILABLE", fmt.Sprintf("create the session temp dir: %v", err))
		}
		env = append(env, "TMPDIR="+tmp)
	}
	return profile, env, nil
}
