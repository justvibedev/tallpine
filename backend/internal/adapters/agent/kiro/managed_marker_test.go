package kiro

import "testing"

func TestKiroManagedHookAcceptsBothCLINames(t *testing.T) {
	for _, command := range []string{"vo hooks kiro stop", "tallpine hooks kiro stop"} {
		if !isKiroManagedHook(command) {
			t.Errorf("isKiroManagedHook(%q) = false", command)
		}
	}
	if isKiroManagedHook("ao hooks kiro stop") {
		t.Error("official ao hooks must not be treated as ours")
	}
}
