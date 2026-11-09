package sessionmanager

import (
	"os/exec"
	"strings"
	"testing"
)

// TestWorkerBranchGuidanceMatchesGit keeps the prompt honest against real Git
// (B456): a recommended shape must be creatable while its base branch exists,
// and a shape the prompt warns about must really be refused.
func TestWorkerBranchGuidanceMatchesGit(t *testing.T) {
	prompt := workerMultiPRPrompt()
	for _, tc := range []struct {
		name, base, template, branch string
		wantOK                       bool
	}{
		{"root sibling", "tp/s-1/root", "<namespace>/<topic>", "tp/s-1/fix", true},
		{"root stack", "tp/s-1/fix", "<parent-branch>-<topic>", "tp/s-1/fix-more", true},
		{"root child refused", "tp/s-1/root", "<namespace>/root/<topic>", "tp/s-1/root/fix", false},
		{"bare namespace refused", "tp/s-1/root", "<namespace>", "tp/s-1", false},
		{"workspace sibling", "tp/ws-1", "<session-branch>-<topic>", "tp/ws-1-fix", true},
		{"custom additional PR", "feature/a", "<current-branch>-<topic>", "feature/a-fix", true},
		{"child of existing branch refused", "feature/a", "<existing-branch>/<topic>", "feature/a/fix", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(prompt, "`"+tc.template+"`") {
				t.Fatalf("prompt does not mention `%s`:\n%s", tc.template, prompt)
			}
			repo := t.TempDir()
			runManagerGit(t, repo, "init", "-q", "-b", "main", ".")
			runManagerGit(t, repo, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init")
			runManagerGit(t, repo, "branch", "tp/s-1/root")
			if tc.base != "tp/s-1/root" {
				runManagerGit(t, repo, "branch", tc.base)
			}
			out, err := exec.Command("git", "-C", repo, "switch", "-c", tc.branch, "--no-track", tc.base).CombinedOutput()
			if tc.wantOK && err != nil {
				t.Fatalf("git refused recommended branch %q: %v\n%s", tc.branch, err, out)
			}
			if !tc.wantOK && (err == nil || !strings.Contains(string(out), "cannot lock ref")) {
				t.Fatalf("git on warned branch %q: err=%v\n%s", tc.branch, err, out)
			}
		})
	}
}

// TestWorkerBranchGuidanceDropsImpossibleChildren pins the removed advice: a
// slash child of a checked-out session or parent branch can never be created.
func TestWorkerBranchGuidanceDropsImpossibleChildren(t *testing.T) {
	prompt := workerMultiPRPrompt()
	for _, stale := range []string{"`<current-branch>/<topic>`", "`<parent-branch>/<topic>`"} {
		if strings.Contains(prompt, stale) {
			t.Fatalf("prompt still recommends %s:\n%s", stale, prompt)
		}
	}
}

// TestWorkerBranchGuidanceClaimsCustomBranchForms pins the explicit claim step:
// names outside the session namespace are not auto-attributed in every project
// (workspace projects have no worktree history to read).
func TestWorkerBranchGuidanceClaimsCustomBranchForms(t *testing.T) {
	prompt := workerMultiPRPrompt()
	claim := "`tallpine session claim-pr <full-pr-url>`"
	for _, line := range strings.Split(prompt, "\n") {
		if strings.HasPrefix(line, "Tallpine automatically attributes") || strings.HasPrefix(line, "- Otherwise,") || strings.HasPrefix(line, "- To stack") {
			if !strings.Contains(line, claim) {
				t.Fatalf("line has no claim step: %s", line)
			}
		}
	}
	for _, stale := range []string{"worktree's branch history", "Keep branch names inside this session namespace"} {
		if strings.Contains(prompt, stale) {
			t.Fatalf("prompt still says %q:\n%s", stale, prompt)
		}
	}
}
