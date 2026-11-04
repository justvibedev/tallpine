package daemon

import (
	"context"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// A moved planned file must show up under its old path, or the stale check
// would miss a plan that names the file it started from.
func TestGitChangedFiles_ListsBothSidesOfARename(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("init", "-q")
	if err := exec.Command("sh", "-c", "printf 'package a\\n// some content that is long enough to be detected as a rename\\n' > "+filepath.Join(dir, "a.go")).Run(); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "base")
	base := run("rev-parse", "HEAD")
	base = base[:len(base)-1]
	run("mv", "a.go", "b.go")
	run("commit", "-qam", "move")
	got, err := gitChangedFiles(context.Background(), dir, base, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got, "a.go") || !slices.Contains(got, "b.go") {
		t.Fatalf("changed = %v, want both a.go and b.go", got)
	}
}
