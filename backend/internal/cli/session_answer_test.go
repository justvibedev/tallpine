package cli

import (
	"strings"
	"testing"
)

func TestSessionApprovalsListShowsQuestionWithAnswerHint(t *testing.T) {
	var posted string
	out, _, err := executeCLI(t, approvalsServer(t, &posted), "session", "approvals", "w-1")
	if err != nil || !strings.Contains(out, "question in-1") || !strings.Contains(out, "Switch to Opus 4.8?") ||
		!strings.Contains(out, "session answer w-1 in-1 --action") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestSessionAnswerPostsActionAndContent(t *testing.T) {
	var posted string
	deps := approvalsServer(t, &posted)
	if _, _, err := executeCLI(t, deps, "session", "answer", "w-1", "in-1", "--action", "accept", "--content", `{"choice":"stay"}`); err != nil {
		t.Fatal(err)
	}
	if posted != `/api/v1/sessions/w-1/conversation/inputs/in-1/resolve {"action":"accept","content":{"choice":"stay"}}` {
		t.Fatalf("posted %q", posted)
	}
}

func TestSessionAnswerRejectsBadFlags(t *testing.T) {
	var posted string
	deps := approvalsServer(t, &posted)
	if _, _, err := executeCLI(t, deps, "session", "answer", "w-1", "in-1", "--action", "maybe"); err == nil {
		t.Fatal("invalid action accepted")
	}
	if _, _, err := executeCLI(t, deps, "session", "answer", "w-1", "in-1", "--action", "accept", "--content", `["x"]`); err == nil {
		t.Fatal("non-object content accepted")
	}
	if posted != "" {
		t.Fatalf("a rejected call posted %q", posted)
	}
}
