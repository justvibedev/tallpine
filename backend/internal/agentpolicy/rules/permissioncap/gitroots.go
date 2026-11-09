package permissioncap

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// GitWritableRoots are the git directories a confined agent in a linked
// worktree needs to write for fetch, commit and push: the worktree's own git
// dir (FETCH_HEAD, HEAD, index), and the shared objects/, refs/ and logs/
// directories. A packed-refs rewrite (gc, deleting a packed ref) needs the
// shared git dir itself and stays denied. The shared config and hooks/ are
// deliberately not included, so a confined agent cannot plant a hook that later
// unconfined runs execute. A normal checkout (.git is a directory) lives inside
// the workspace already and yields nothing. repo is the recorded project repo;
// see RecordedLinkedGit.
func GitWritableRoots(repo, workspace string) []string {
	common, private := RecordedLinkedGit(repo, workspace)
	if common == "" {
		return nil
	}
	return []string{private, filepath.Join(common, "objects"), filepath.Join(common, "refs"), filepath.Join(common, "logs")}
}

// RecordedLinkedGit returns the git common dir of repo, the project repository
// the daemon has on record, and workspace's private git dir under it, when
// workspace is a linked worktree of repo. Otherwise both are empty.
//
// Everything in the checkout, including the .git file and the private dir's
// commondir and gitdir files, is writable by the confined agent, so it may only
// answer whether workspace is a worktree of repo. It never picks the granted
// path: common comes from repo, and private must be exactly
// <common>/worktrees/<name> and name this workspace back.
func RecordedLinkedGit(repo, workspace string) (common, private string) {
	common, canonical := repoCommonDir(repo)
	if common == "" {
		return "", ""
	}
	dotGit := filepath.Join(workspace, ".git")
	name, ok := linkedName(canonical, workspace, dotGit)
	if !ok {
		return "", ""
	}
	return common, filepath.Join(common, "worktrees", name)
}

// repoCommonDir is repo's git dir, as given and symlink-resolved. Only a real
// .git directory qualifies: a registered repo that is itself a linked worktree
// or a bare repo keeps its layout in files a confined agent could rewrite once
// granted, so it gets no grant. A relative repo never matches the absolute
// claim linkedName compares it with.
func repoCommonDir(repo string) (common, canonical string) {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return "", ""
	}
	common = filepath.Join(filepath.Clean(repo), ".git")
	if st, err := os.Lstat(common); err != nil || !st.IsDir() {
		return "", ""
	}
	canonical, err := filepath.EvalSymlinks(common)
	if err != nil {
		return "", ""
	}
	return common, canonical
}

// linkedName is the worktree name the .git file claims under canonical, when
// the claim resolves to exactly <canonical>/worktrees/<name> and that dir's
// gitdir file names this workspace.
func linkedName(canonical, workspace, dotGit string) (string, bool) {
	claim, ok := gitFileTarget(dotGit, workspace)
	if !ok {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(claim)
	if err != nil || filepath.Dir(resolved) != filepath.Join(canonical, "worktrees") {
		return "", false
	}
	back, ok := gitFileTarget(filepath.Join(resolved, "gitdir"), resolved)
	if !ok {
		return "", false
	}
	backResolved, err := filepath.EvalSymlinks(back)
	if err != nil {
		return "", false
	}
	self, err := filepath.EvalSymlinks(dotGit)
	if err != nil || backResolved != self {
		return "", false
	}
	return filepath.Base(resolved), true
}

// maxPointerFile bounds a git pointer file, which holds one path line.
const maxPointerFile = 8 << 10

// readPointerFile reads a pointer file the confined agent can replace, in the
// unfenced daemon. It must not follow a link, block on a FIFO or read without
// end, and it checks the descriptor it opened so nothing can be swapped between
// the check and the read. Any failure means the layout is not linked.
func readPointerFile(path string) ([]byte, bool) {
	f, err := openPointerFile(path)
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(f, maxPointerFile+1))
	if err != nil || len(data) > maxPointerFile {
		return nil, false
	}
	return data, true
}

// gitFileTarget reads a git pointer file: a .git file ("gitdir: <path>") or a
// private git dir's gitdir file (a bare path). A relative path is taken from
// base.
func gitFileTarget(file, base string) (string, bool) {
	data, ok := readPointerFile(file)
	if !ok {
		return "", false
	}
	target := strings.TrimSpace(string(data))
	if rest, ok := strings.CutPrefix(target, "gitdir:"); ok {
		target = strings.TrimSpace(rest)
	}
	if target == "" {
		return "", false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(base, target)
	}
	return target, true
}
