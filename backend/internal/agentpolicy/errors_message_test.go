package agentpolicy

import (
	"strings"
	"testing"
)

// B401: Security mode can be the user's own setting, so the empty-fence refusal
// must say what happened and what to do, not blame an organization's profile.
func TestEmptyFenceRefusalSaysWhatToDo(t *testing.T) {
	msg := ErrFenceNoRunFile.Error()
	for _, want := range []string{"refused", "run file", "Restart the Tallpine daemon"} {
		if !strings.Contains(msg, want) {
			t.Errorf("%q does not say %q", msg, want)
		}
	}
	if strings.Contains(msg, "organization") {
		t.Errorf("%q blames an organization's policy", msg)
	}
}
