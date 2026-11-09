package cleanup

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// hasContent reports whether a snapshot directory holds anything besides
// dot-prefixed entries; a salvage builds in a ".<name>-XXXX" temp directory
// that a crash leaves behind, which is not a snapshot.
func hasContent(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			return true
		}
	}
	return false
}

// newestStamp is the latest snapshot directory that holds content (stamps
// sort by time).
func newestStamp(dir string, entries []os.DirEntry) string {
	newest := ""
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && e.Name() > newest && hasContent(filepath.Join(dir, e.Name())) {
			newest = e.Name()
		}
	}
	return newest
}

// sameWork reports whether every artifact of snapshot old exists, byte for
// byte, in snapshot newer, so deleting old loses nothing. README.md carries a
// timestamp and is ignored; dot-prefixed temp entries are not snapshot content.
// An old snapshot with no content is left to the age-out.
func sameWork(old, newer string) bool {
	if !hasContent(old) {
		return false
	}
	root, err := os.OpenRoot(old)
	if err != nil {
		return false
	}
	defer func() { _ = root.Close() }()
	same := true
	err = filepath.WalkDir(old, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(old, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || d.Name() == "README.md" {
			return nil
		}
		a, err := root.ReadFile(rel)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(filepath.Join(newer, rel))
		if err != nil || !bytes.Equal(a, b) {
			same = false
			return fs.SkipAll
		}
		return nil
	})
	return err == nil && same
}
