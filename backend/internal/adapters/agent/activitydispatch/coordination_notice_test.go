package activitydispatch

import "testing"

// Turns recorded before the rename start with "AO transferred ..."; they stay coordination messages.
func TestCoordinationMessageRecognisesPreRenameNotice(t *testing.T) {
	for _, text := range []string{
		"AO transferred the previous agent's context in hidden system instructions. Continue.",
		"Tallpine transferred the previous agent's context in hidden system instructions. Continue.",
	} {
		if !isTallpineCoordinationMessage(text) {
			t.Errorf("not recognised: %q", text)
		}
	}
	if isTallpineCoordinationMessage("please transfer the context") {
		t.Error("ordinary text treated as coordination")
	}
}
