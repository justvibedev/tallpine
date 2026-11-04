package testenv

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// fencedShortBaseDir is ShortBaseDir inside the Claude security-mode fence (B463). There /tmp may
// be written only in the dir named by TALLPINE_SOCKET_ALIAS_ROOT, and sockets may bind only under
// the session's own $TMPDIR (a bind through a symlink lands on the resolved path). So the base is a
// short symlink in that root (named per target) onto a real dir under $TMPDIR. Empty outside the fence.
func fencedShortBaseDir() string {
	root := os.Getenv("TALLPINE_SOCKET_ALIAS_ROOT")
	if root == "" {
		return ""
	}
	target := filepath.Join(os.TempDir(), "tp-short")
	if err := os.MkdirAll(target, 0o700); err != nil {
		return ""
	}
	// The name is keyed by the target: every fenced session shares the alias root but has its own
	// $TMPDIR, so one fixed name would be repointed under a concurrent session's live paths.
	sum := sha256.Sum256([]byte(target))
	link := filepath.Join(root, fmt.Sprintf("tp-short-%d-%x", os.Getuid(), sum[:6]))
	if cur, err := os.Readlink(link); err != nil || cur != target {
		_ = os.Remove(link)
		if err := os.Symlink(target, link); err != nil && !os.IsExist(err) {
			return ""
		}
	}
	return link
}
