package fsperm

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func mode(t *testing.T, p string) os.FileMode {
	t.Helper()
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func write(t *testing.T, p string, m os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), m); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, m); err != nil {
		t.Fatal(err)
	}
}

func TestHardenMigratesExistingInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix modes")
	}
	state := filepath.Join(t.TempDir(), "tallpine")
	data := filepath.Join(state, "data")
	for _, d := range []string{data, filepath.Join(state, "release-builds"), filepath.Join(state, "releases"), filepath.Join(data, "artifacts", "s1", ".versions")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		_ = os.Chmod(d, 0o755)
	}
	_ = os.Chmod(state, 0o755)
	for _, f := range []string{"ao.db", "ao.db-wal", "ao.db-shm"} {
		write(t, filepath.Join(data, f), 0o644)
	}
	blob := filepath.Join(data, "artifacts", "s1", ".versions", "b")
	write(t, blob, 0o644)
	wt := filepath.Join(data, "worktrees", "p", "src.go")
	write(t, wt, 0o644)
	_ = os.Chmod(filepath.Join(data, "worktrees"), 0o755)

	exe := filepath.Join(data, "artifacts", "s1", "run.sh")
	write(t, exe, 0o755)
	if w, err := Harden(state, data); err != nil || len(w) != 0 {
		t.Fatalf("Harden = %v, %v", w, err)
	}
	if m := mode(t, exe); m != 0o700 {
		t.Errorf("0755 file = %o, want 700", m)
	}
	for _, d := range []string{state, data, filepath.Join(state, "release-builds"), filepath.Join(state, "releases"), filepath.Join(data, "worktrees"), filepath.Join(data, "artifacts", "s1")} {
		if m := mode(t, d); m != 0o700 {
			t.Errorf("%s = %o, want 700", d, m)
		}
	}
	for _, f := range []string{"ao.db", "ao.db-wal", "ao.db-shm"} {
		if m := mode(t, filepath.Join(data, f)); m != 0o600 {
			t.Errorf("%s = %o, want 600", f, m)
		}
	}
	if m := mode(t, blob); m != 0o600 {
		t.Errorf("blob = %o, want 600", m)
	}
	for _, name := range []string{"runtime", "skills"} {
		if _, err := os.Lstat(filepath.Join(state, name)); err == nil {
			t.Errorf("%s created under state root", name)
		}
		if _, err := os.Lstat(filepath.Join(data, name)); err == nil {
			t.Errorf("%s created under data dir", name)
		}
	}
	if m := mode(t, wt); m != 0o644 {
		t.Errorf("worktree file = %o, must be untouched", m)
	}
}

func TestHardenSkipsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix modes")
	}
	base := t.TempDir()
	outside := filepath.Join(base, "outside.txt")
	write(t, outside, 0o644)
	data := filepath.Join(base, "data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(data, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := Harden(data, data); err != nil {
		t.Fatal(err)
	}
	if m := mode(t, outside); m != 0o644 {
		t.Errorf("outside file = %o, must be untouched", m)
	}
}

func TestCreatePrivateFileIsOwnerOnlyWithPermissiveUmask(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix modes")
	}
	old := syscallUmask()
	t.Cleanup(func() { restoreUmask(old) })
	setUmask(0)
	p := filepath.Join(t.TempDir(), "ao.db")
	if err := CreatePrivateFile(p); err != nil {
		t.Fatal(err)
	}
	if m := mode(t, p); m != 0o600 {
		t.Errorf("new file = %o, want 600", m)
	}
	// An existing file is left alone.
	if err := os.WriteFile(p, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CreatePrivateFile(p); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "data" {
		t.Errorf("existing file was truncated: %q", b)
	}
}

func TestHardenTightenFailureIsWarningNotError(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs posix modes and a non-root user")
	}
	data := filepath.Join(t.TempDir(), "data")
	locked := filepath.Join(data, "artifacts", "locked")
	write(t, filepath.Join(locked, "f"), 0o644)
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	ok := filepath.Join(data, "ao.db")
	write(t, ok, 0o644)
	w, err := Harden(data, data)
	if err != nil {
		t.Fatalf("Harden error = %v, want nil", err)
	}
	if len(w) == 0 {
		t.Error("want a warning for the unreadable dir")
	}
	if m := mode(t, ok); m != 0o600 {
		t.Errorf("ao.db = %o, want 600 despite the failure", m)
	}
}
