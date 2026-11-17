package gitblob

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/adapters/gitcli"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/testenv"
)

func TestMain(m *testing.M) {
	ports.SetDefaultGit(gitcli.New())
	testenv.Main(m)
}

func repoWith(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "f.md"}, {"-c", "user.name=t", "-c", "user.email=t@e", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "x"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return dir
}

func TestReadAt(t *testing.T) {
	dir := repoWith(t, "0123456789")
	ctx := context.Background()
	if data, found, err := ReadAt(ctx, dir, "main", "f.md", 100); err != nil || !found || string(data) != "0123456789" {
		t.Fatalf("ReadAt = %q %v %v", data, found, err)
	}
	if data, found, err := ReadAt(ctx, dir, "main", "f.md", 4); err != nil || !found || string(data) != "0123" {
		t.Fatalf("capped ReadAt = %q %v %v", data, found, err)
	}
	for _, c := range [][2]string{{"main", "missing.md"}, {"nope", "f.md"}} {
		if data, found, err := ReadAt(ctx, dir, c[0], c[1], 100); err != nil || found || data != nil {
			t.Fatalf("ReadAt(%v) = %q %v %v, want absent", c, data, found, err)
		}
	}
}

func TestRead(t *testing.T) {
	dir := repoWith(t, "0123456789")
	ctx := context.Background()
	if b, err := Read(ctx, dir, "main:f.md", 100); err != nil || !b.Exists || b.Truncated || string(b.Data) != "0123456789" || b.Size != 10 {
		t.Fatalf("Read = %+v %v", b, err)
	}
	if b, err := Read(ctx, dir, "main:f.md", 4); err != nil || !b.Exists || !b.Truncated || b.Data != nil || b.Size != 10 {
		t.Fatalf("over-limit Read = %+v %v", b, err)
	}
	if b, err := Read(ctx, dir, "main:missing", 100); err != nil || b.Exists {
		t.Fatalf("missing Read = %+v %v", b, err)
	}
}

func TestListTreeAt(t *testing.T) {
	dir := repoWith(t, "x")
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "d", "s"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "d", "s", "a.md"), []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "d", "run.sh"), []byte("#!"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("s/a.md", filepath.Join(dir, "d", "link.md")); err != nil {
		t.Fatal(err)
	}
	run("add", "d")
	run("-c", "user.name=t", "-c", "user.email=t@e", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "d")
	entries, err := ListTreeAt(context.Background(), dir, "main", "d")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Entry{}
	for _, e := range entries {
		got[e.Path] = e
	}
	if e := got["d/s/a.md"]; e.Mode != ModeRegular || e.Size != 3 {
		t.Fatalf("a.md = %+v", e)
	}
	if got["d/run.sh"].Mode != ModeExecutable || got["d/link.md"].Mode != ModeSymlink {
		t.Fatalf("modes = %+v", got)
	}
	if _, ok := got["f.md"]; ok || len(got) != 3 {
		t.Fatalf("listing outside path: %+v", got)
	}
	if entries, err := ListTreeAt(context.Background(), dir, "nope", "d"); err != nil || entries != nil {
		t.Fatalf("missing ref = %v %v", entries, err)
	}
}
