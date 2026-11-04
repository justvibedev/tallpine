//go:build !windows

package browserstream

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"tallpine.eu/tallpine/backend/internal/unixsock"
)

func TestListenIsPrivateAndAliased(t *testing.T) {
	data := t.TempDir()
	ln, path, err := Listen(filepath.Join(data, "running.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if !unixsock.IsOwnerOnly(ln) {
		t.Fatal("no owner-UID filter")
	}
	sfi, err := os.Stat(path)
	if err != nil || sfi.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %v err=%v, want 0600", sfi, err)
	}
	dataReal, _ := filepath.EvalSymlinks(data)
	realDir, _ := filepath.EvalSymlinks(filepath.Dir(path))
	if realDir != dataReal {
		t.Fatalf("socket dir %s, want data root %s", realDir, dataReal)
	}
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
}

func TestListenCloseRemovesSocketAndAlias(t *testing.T) {
	data := t.TempDir()
	ln, path, err := Listen(filepath.Join(data, "running.json"))
	if err != nil {
		t.Fatal(err)
	}
	_ = ln.Close()
	if _, err := os.Lstat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("alias remains: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(data, "s.sock")); !os.IsNotExist(err) {
		t.Fatalf("socket remains: %v", err)
	}
}

func TestListenSweepsLegacyBstreamDirsOfDeadPids(t *testing.T) {
	data := t.TempDir()
	dead := filepath.Join(data, "bstream-999999-x")
	live := filepath.Join(data, "bstream-"+strconv.Itoa(os.Getpid())+"-x")
	for _, d := range []string{dead, live} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ln, _, err := Listen(filepath.Join(data, "running.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if _, err := os.Lstat(dead); !os.IsNotExist(err) {
		t.Fatalf("dead legacy dir survived: %v", err)
	}
	if _, err := os.Lstat(live); err != nil {
		t.Fatalf("live legacy dir removed: %v", err)
	}
}
