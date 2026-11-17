package domain

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRepoRelativePath(t *testing.T) {
	for _, bad := range []string{"/etc/passwd", "../x", "a/../../x", "..", ".", `\x`} {
		if _, err := RepoRelativePath("/repo", bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if got, err := RepoRelativePath("", " docs//review.md "); err != nil || got != "docs/review.md" {
		t.Fatalf("repo spec form = %q, %v", got, err)
	}
	if got, err := RepoRelativePath("/repo", "docs/review.md"); err != nil || got != filepath.Join("/repo", "docs", "review.md") {
		t.Fatalf("joined = %q, %v", got, err)
	}
	if err := (ProjectConfig{ReviewerRulesFile: "../x.md"}).Validate(); err == nil || !strings.Contains(err.Error(), "reviewerRulesFile") {
		t.Fatalf("escaping reviewerRulesFile validated: %v", err)
	}
}

func TestCapRules(t *testing.T) {
	if got, capped := CapRules("short", "x"); got != "short" || capped {
		t.Fatalf("short = %q %v", got, capped)
	}
	got, capped := CapRules(strings.Repeat("é", MaxAgentRulesBytes), "reviewerRules/reviewerRulesFile")
	if !capped || !strings.HasSuffix(got, "shorten reviewerRules/reviewerRulesFile]") || strings.ContainsRune(got, '�') {
		t.Fatalf("capped = %v tail %q", capped, got[len(got)-60:])
	}
}
