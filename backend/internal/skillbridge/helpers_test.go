package skillbridge

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func write(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func skill(t *testing.T, root, name, desc string) {
	t.Helper()
	write(t, filepath.Join(root, name, "SKILL.md"), "---\nname: "+name+"\ndescription: "+desc+"\n---\nbody\n")
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// repo builds a main checkout (with the given tracked files) and a linked
// worktree of it, like Tallpine sessions get.
func repo(t *testing.T, tracked map[string]string) (main, ws string) {
	t.Helper()
	base := t.TempDir()
	main = filepath.Join(base, "main")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, main, "init", "-q", "-b", "main")
	write(t, filepath.Join(main, "README.md"), "x")
	for p, body := range tracked {
		write(t, filepath.Join(main, p), body)
	}
	git(t, main, "add", "-A")
	git(t, main, "commit", "-q", "-m", "init")
	ws = filepath.Join(base, "ws")
	git(t, main, "worktree", "add", "-q", "-b", "s", ws)
	return main, ws
}

func status(t *testing.T, dir string) string {
	return git(t, dir, "status", "--porcelain", "--untracked-files=all")
}

func readable(t *testing.T, p string) bool {
	t.Helper()
	_, err := os.ReadFile(p)
	return err == nil
}

// digest hashes a tree so tests can prove the bridge never writes to sources.
func digest(t *testing.T, root string) string { return digestTree(t, root, true) }

func digestTree(t *testing.T, root string, mtime bool) string {
	t.Helper()
	h := sha256.New()
	var lines []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // vanished entries are not part of the digest
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil //nolint:nilerr // vanished entries are not part of the digest
		}
		b, _ := os.ReadFile(p)
		mt := int64(0)
		if mtime {
			mt = info.ModTime().UnixNano()
		}
		link, _ := os.Readlink(p)
		lines = append(lines, fmt.Sprintf("%s %v %d %s %x", p, d.Type(), mt, link, sha256.Sum256(b)))
		return nil
	})
	sort.Strings(lines)
	h.Write([]byte(strings.Join(lines, "\n")))
	return fmt.Sprintf("%x", h.Sum(nil))
}
