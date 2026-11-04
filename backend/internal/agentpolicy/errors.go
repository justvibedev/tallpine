package agentpolicy

import (
	"errors"
	"strings"
)

// LockedError reports fields ("rule.field") a lower layer cannot change because
// managed config or the environment sets them.
type LockedError struct{ Fields []string }

func (e *LockedError) Error() string {
	return "agent policy field locked: " + strings.Join(e.Fields, ", ")
}

// ValidationError reports a rejected update.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

// ErrLoosens is wrapped by a ValidationError when a project override would make
// agents less restricted than the user's global setting.
var ErrLoosens = errors.New("a project may only tighten this setting")

// ErrModeConflict is returned by Update when a write would leave Security mode
// and Dev mode both effectively on. Stored both-on combinations are not touched
// by unrelated writes.
var ErrModeConflict = &ValidationError{Message: "Security mode and Dev mode cannot both be on; pick one"}

// CodeRequiresFence is the API error code for a launch the managed hardened
// profile refuses because the session cannot be fenced off Tallpine state.
const CodeRequiresFence = "POLICY_REQUIRES_FENCE"

// FenceRequiredError refuses a launch under the hardened profile: the harness
// (in this interface, on this OS) cannot run inside the protectState fence.
type FenceRequiredError struct {
	Harness string
	GOOS    string
	Reason  string
}

func (e *FenceRequiredError) Error() string {
	msg := "your organization's hardened agent policy requires a fenced session; " + e.Harness + " on " + e.GOOS + " cannot be fenced"
	if e.Reason != "" {
		msg += " (" + e.Reason + ")"
	}
	return msg + ". Use Claude Code on macOS, or ask your administrator"
}

// ErrRequiresFence is matched by errors.Is for every FenceRequiredError.
var ErrRequiresFence = errors.New("agent policy requires a fenced session")

// Is lets errors.Is(err, ErrRequiresFence) match.
func (e *FenceRequiredError) Is(target error) bool { return target == ErrRequiresFence }

// IsFenceRequired reports whether err is (or wraps) a FenceRequiredError.
func IsFenceRequired(err error) bool {
	var target *FenceRequiredError
	return errors.As(err, &target)
}
