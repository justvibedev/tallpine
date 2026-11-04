package scm

import "testing"

func TestWorkspaceHyphenBranchAcceptsBothPrefixes(t *testing.T) {
	sr := wsSession("p-1", "/a")
	sr.workspace = true
	for _, branch := range []string{"vo/p-1", "tp/p-1", "tp/p-1", "tp/p-1-2"} {
		if !workspaceHyphenBranch(sr, branch) {
			t.Errorf("workspaceHyphenBranch(%q) = false", branch)
		}
	}
	if workspaceHyphenBranch(sr, "xx/p-1") {
		t.Error("unknown prefix must not match")
	}
}
