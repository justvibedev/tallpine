package cli

import (
	"bytes"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

func TestPrintKeptWorktreesListsOnlyAttentionWorktrees(t *testing.T) {
	r := apitypes.CleanupRunReport{Categories: []apitypes.CleanupCategoryReport{{Category: "worktrees", Items: []apitypes.CleanupItem{
		{SessionID: "a", What: "worktree", Path: "/w/a", Status: "kept", Reason: "uncommitted changes; salvaged work: /s/a", Attention: true},
		{SessionID: "b", What: "worktree", Path: "/w/b", Status: "kept", Reason: "ended less than 7 day(s) ago"},
	}}}}
	var out bytes.Buffer
	if err := printKeptWorktrees(&out, r); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "Worktrees kept by cleanup (1)") || !strings.Contains(got, "/s/a") || strings.Contains(got, "/w/b") {
		t.Fatalf("output = %q", got)
	}
	out.Reset()
	if err := printKeptWorktrees(&out, apitypes.CleanupRunReport{}); err != nil || out.Len() != 0 {
		t.Fatalf("empty report printed %q, %v", out.String(), err)
	}
}
