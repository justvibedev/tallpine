package permissioncap

import (
	"os"
	"path/filepath"
	"strings"
)

// GitWritableRoots are the git directories a confined agent in a linked
// worktree needs to write for fetch, commit and push: the worktree's own git
// dir (FETCH_HEAD, HEAD, index), and the shared objects/, refs/ and logs/ directories. A packed-refs rewrite (gc, deleting a packed ref) needs the shared git dir itself and stays denied.
// The shared config and hooks/ are deliberately not included, so a confined
// agent cannot plant a hook that later unconfined runs execute. A normal
// checkout (.git is a directory) lives inside the workspace already and yields
// nothing.
func GitWritableRoots(workspace string) []string {
	data, err := os.ReadFile(filepath.Join(workspace, ".git"))
	if err != nil {
		return nil
	}
	dir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return nil
	}
	dir = strings.TrimSpace(dir)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(workspace, dir)
	}
	roots := []string{dir}
	if rel, err := os.ReadFile(filepath.Join(dir, "commondir")); err == nil {
		common := strings.TrimSpace(string(rel))
		if !filepath.IsAbs(common) {
			common = filepath.Join(dir, common)
		}
		common = filepath.Clean(common)
		roots = append(roots, filepath.Join(common, "objects"), filepath.Join(common, "refs"), filepath.Join(common, "logs"))
	}
	return roots
}
