package hookutil

import "testing"

// Workspaces created before the rebrand hold files stamped with the old
// sentinels. They must still be recognised as managed.
func TestSentinelsRecogniseCurrentAndPreRenameMarkers(t *testing.T) {
	for _, marker := range []string{GitignoreSentinel, "# managed by agent-orchestrator: AO hook files stay out of git status"} {
		if !HasGitignoreSentinel(marker + "\n/.gitignore\n") {
			t.Errorf("gitignore marker %q not recognised", marker)
		}
	}
	for _, marker := range []string{CopilotAgentProfileSentinel, "<!-- managed by agent-orchestrator: copilot agent profile -->"} {
		if !HasCopilotAgentProfileSentinel("---\nname: x\n---\n" + marker + "\n") {
			t.Errorf("profile marker %q not recognised", marker)
		}
	}
	if HasGitignoreSentinel("node_modules\n") || HasCopilotAgentProfileSentinel("# my profile\n") {
		t.Error("an unmarked file was treated as managed")
	}
}
