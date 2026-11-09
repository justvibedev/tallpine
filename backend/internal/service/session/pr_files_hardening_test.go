package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/testenv"
)

// B629: the review fetch is unfenced network git. Run in the worktree it would
// start the uploadpack program named in the agent's config.worktree.
func TestPRFetchRunsNoAgentUploadPack(t *testing.T) {
	w := testenv.NewAgentWorktree(t)
	root := t.TempDir()
	upstream := filepath.Join(root, "upstream.git")
	w.Git(t, root, "init", "-q", "--bare", upstream)
	base := strings.TrimSpace(w.Git(t, w.Repo, "rev-parse", "HEAD"))
	w.Git(t, w.Repo, "push", "-q", upstream, base+":refs/heads/main")
	clone := filepath.Join(root, "clone")
	w.Git(t, root, "clone", "-q", "--branch", "main", upstream, clone)
	w.Git(t, clone, "commit", "-q", "--allow-empty", "-m", "pr head")
	head := strings.TrimSpace(w.Git(t, clone, "rev-parse", "HEAD"))
	w.Git(t, clone, "push", "-q", "origin", head+":refs/pull/7/head")
	w.Git(t, w.Repo, "remote", "add", "up", upstream)

	// Unlike the fixture's script this one exits at once: git waits on its stdout.
	script := filepath.Join(root, "uploadpack.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho ran >> "+w.Marker+"\nexit 1\n"), 0o700); err != nil { //nolint:gosec // the test's script must be executable
		t.Fatal(err)
	}
	w.Git(t, w.Path, "config", "--worktree", "remote.up.uploadpack", script)
	w.TryGit(w.Path, "fetch", "--no-tags", "up", "refs/pull/7/head")
	if !w.Ran() {
		t.Fatal("payload never ran: this test no longer exercises the attack")
	}
	pr := domain.PullRequest{Number: 7, Provider: "github", BaseSHA: base, HeadSHA: head}
	if err := ensurePRRevisionObjects(context.Background(), w.Path, "up", pr); err != nil {
		t.Fatalf("the fetch must still bring the revision in: %v", err)
	}
	if w.Ran() {
		t.Fatal("the daemon's fetch ran the agent's uploadpack")
	}
}

// An option-shaped remote name set only in the agent's config.worktree must not
// be selected, or reach git fetch as a flag if it ever is.
func TestPRFetchIgnoresAnOptionShapedRemoteFromTheWorktree(t *testing.T) {
	w := testenv.NewAgentWorktree(t)
	root := t.TempDir()
	script := filepath.Join(root, "uploadpack.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho ran >> "+w.Marker+"\nexit 1\n"), 0o700); err != nil { //nolint:gosec // the test's script must be executable
		t.Fatal(err)
	}
	const url = "https://github.com/acme/repo.git"
	planted := "--upload-pack=" + script
	w.Git(t, w.Path, "config", "--worktree", "remote."+planted+".url", url)
	w.TryGit(w.Path, "fetch", "--no-tags", planted, "refs/heads/main")
	if !w.Ran() {
		t.Fatal("payload never ran: this test no longer exercises the attack")
	}
	w.Git(t, w.Repo, "remote", "add", "up", url)

	st := newFakeStore()
	rec := domain.SessionRecord{ID: "tp-1", Metadata: domain.SessionMetadata{WorkspacePath: w.Path}}
	pr := domain.PullRequest{Number: 7, Provider: "github", Host: "github.com", Repo: "acme/repo", HeadSHA: strings.Repeat("a", 40), BaseSHA: strings.Repeat("b", 40)}
	_, remote, err := (&Service{store: st}).prWorkspaceRoot(context.Background(), rec, pr)
	if err != nil {
		t.Fatal(err)
	}
	if remote != "up" {
		t.Fatalf("selected remote %q, want the repository's own", remote)
	}
	_ = ensurePRRevisionObjects(context.Background(), w.Path, planted, pr)
	if w.Ran() {
		t.Fatal("an option-shaped remote reached git fetch as a flag")
	}
}
