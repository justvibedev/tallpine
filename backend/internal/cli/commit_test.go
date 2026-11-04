package cli

import (
	"strings"
	"testing"
)

func TestCommitPostsTheMessageForThisSessionAndPrintsTheSha(t *testing.T) {
	s := &credServer{status: 200, body: `{"sha":"abc123"}`}
	out, _, err := credCLI(t, s, "", "commit", "-m", "fix: thing")
	if err != nil || strings.TrimSpace(out) != "abc123" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if len(s.paths) != 1 || s.paths[0] != "POST /api/v1/sessions/p-1/commit" {
		t.Fatalf("paths = %v", s.paths)
	}
}

func TestCommitRequiresAMessage(t *testing.T) {
	s := &credServer{status: 200, body: `{}`}
	if _, _, err := credCLI(t, s, "", "commit"); err == nil {
		t.Fatal("expected a usage error")
	}
	if len(s.paths) != 0 {
		t.Fatalf("contacted the daemon without a message: %v", s.paths)
	}
}
