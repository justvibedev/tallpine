package skillbridge

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"

	"tallpine.eu/tallpine/backend/internal/pathx"
)

// ErrNotBridged means a path is not something Tallpine created: removing it could
// delete a skill's only copy.
var ErrNotBridged = errors.New("not a link or copy created by the bridge")

var bridgePath = regexp.MustCompile(`^\.(claude|agents|codex)/skills/([^/.][^/]*)$`)

var bridgeDirs = []string{".claude", ".agents", ".codex"}

// Removable reports whether rel (a skill folder relative to the checkout) is a
// link or copy the bridge made, so RemoveBridged would accept it.
func Removable(workspace, rel string) bool { return checkBridged(workspace, rel) == nil }

// checkBridged accepts only: a symlink whose target is another skill folder
// inside the checkout, or a real folder whose content is identical to the
// same-named skill of another convention in the checkout (what a copy fallback
// leaves). A skill that exists once is never accepted.
func checkBridged(workspace, rel string) error {
	m := bridgePath.FindStringSubmatch(rel)
	if m == nil || !destSafe(workspace, rel) {
		return fmt.Errorf("%w: %s", ErrNotBridged, rel)
	}
	dest := filepath.Join(workspace, filepath.FromSlash(rel))
	st, err := os.Lstat(dest)
	if err != nil {
		return err
	}
	root, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(dest)
		if err != nil || !pathx.Within(root, resolved) || resolved == filepath.Join(root, filepath.FromSlash(rel)) || !isFile(filepath.Join(resolved, "SKILL.md")) {
			return fmt.Errorf("%w: %s", ErrNotBridged, rel)
		}
		return nil
	}
	if !st.IsDir() {
		return fmt.Errorf("%w: %s", ErrNotBridged, rel)
	}
	mine, err := treeDigest(dest, 0)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrNotBridged, rel)
	}
	myReal, _ := filepath.EvalSymlinks(dest)
	for _, d := range bridgeDirs {
		if d == "."+m[1] {
			continue
		}
		twin := filepath.Join(workspace, d, "skills", m[2])
		if resolved, err := filepath.EvalSymlinks(twin); err != nil || resolved == myReal || !pathx.Within(root, resolved) {
			continue
		}
		if theirs, err := treeDigest(twin, 0); err == nil && theirs == mine {
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrNotBridged, rel)
}

// RemoveBridged deletes a link or copy the bridge created, nothing else. For a
// link only the link goes; the skill it points to is never opened for writing.
func RemoveBridged(workspace, rel string) error {
	if err := checkBridged(workspace, rel); err != nil {
		return err
	}
	dest := filepath.Join(workspace, filepath.FromSlash(rel))
	if st, err := os.Lstat(dest); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return os.Remove(dest)
	}
	return os.RemoveAll(dest)
}

// treeDigest hashes a folder's relative paths and file contents, following
// symlinks the way the copy fallback does.
func treeDigest(root string, depth int) (string, error) {
	if depth > maxCopyDepth {
		return "", errors.New("nested symlinks too deep")
	}
	h := sha256.New()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.Type()&fs.ModeSymlink != 0 && p != root {
			resolved, err := filepath.EvalSymlinks(p)
			if err != nil {
				return err
			}
			if st, err := os.Stat(resolved); err == nil && st.IsDir() {
				sub, err := treeDigest(resolved, depth+1)
				_, _ = fmt.Fprintf(h, "d %s %s\n", rel, sub)
				return err
			}
			p = resolved
		} else if d.IsDir() {
			_, _ = fmt.Fprintf(h, "d %s\n", rel)
			return nil
		}
		b, err := os.ReadFile(p) //nolint:gosec // comparing files inside the user's own checkout
		_, _ = fmt.Fprintf(h, "f %s %x\n", filepath.ToSlash(rel), sha256.Sum256(b))
		return err
	})
	return fmt.Sprintf("%x", h.Sum(nil)), err
}
