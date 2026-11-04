package codex

import "testing"

func TestCodexManagedHookAcceptsBothCLINames(t *testing.T) {
	for _, command := range []string{"vo hooks codex stop", "tallpine hooks codex stop"} {
		if !isCodexManagedHook(command) {
			t.Errorf("isCodexManagedHook(%q) = false", command)
		}
	}
	if isCodexManagedHook("ao hooks codex stop") {
		t.Error("official ao hooks must not be treated as ours")
	}
}
