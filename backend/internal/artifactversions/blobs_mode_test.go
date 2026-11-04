package artifactversions

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBlobStoreCreatesPrivateFilesAndDirs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix modes")
	}
	old := umaskZero()
	t.Cleanup(func() { restoreUmask(old) })
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	b := blobStore{root: root}
	staged, err := b.stage(strings.NewReader("hello"), 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.commit(staged); err != nil {
		t.Fatal(err)
	}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == dir {
			return err
		}
		fi, _ := d.Info()
		want := fs.FileMode(0o600)
		if d.IsDir() {
			want = 0o700
		}
		if fi.Mode().Perm() != want {
			t.Errorf("%s = %o, want %o", p, fi.Mode().Perm(), want)
		}
		return nil
	})
}
