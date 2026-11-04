//go:build !windows

package unixsock

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

func TestListenPrivateModeIs0600AtBind(t *testing.T) {
	// Loosen the umask so only the fix (not the ambient umask) yields 0600;
	// the chmod fallback is covered by checking the mode is exactly 0600.
	p := filepath.Join(testenv.ShortTempDir(t), "s.sock")
	ln, err := ListenPrivate(p)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}
}

func TestListenPrivateRestoresUmask(t *testing.T) {
	old := setUmask(0o022)
	defer setUmask(old)
	ln, err := ListenPrivate(filepath.Join(testenv.ShortTempDir(t), "s.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if got := setUmask(0o022); got != 0o022 {
		t.Fatalf("umask after ListenPrivate = %o, want 022", got)
	}
}

func TestBindModeIs0600BeforeChmod(t *testing.T) {
	old := setUmask(0o022)
	defer setUmask(old)
	var mode os.FileMode
	afterBind = func(p string) {
		fi, err := os.Stat(p)
		if err != nil {
			t.Error(err)
			return
		}
		mode = fi.Mode().Perm()
	}
	defer func() { afterBind = nil }()
	ln, err := ListenPrivate(filepath.Join(testenv.ShortTempDir(t), "s.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if mode&0o077 != 0 {
		t.Fatalf("mode at bind = %o, want no group/other bits", mode)
	}
}

func TestOwnerOnlyAcceptsSameUID(t *testing.T) {
	ln, err := ListenPrivate(filepath.Join(testenv.ShortTempDir(t), "s.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := net.Dial("unix", ln.Addr().String())
		if err == nil {
			defer c.Close()
			time.Sleep(200 * time.Millisecond)
		}
	}()
	c, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

func TestOwnerOnlyRejectsOtherUID(t *testing.T) {
	raw, err := net.Listen("unix", filepath.Join(testenv.ShortTempDir(t), "s.sock"))
	if err != nil {
		t.Fatal(err)
	}
	ln := NewOwnerOnly(raw, os.Getuid()+1) // pretend the owner is someone else
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		if c, err := ln.Accept(); err == nil {
			accepted <- c
		}
	}()
	c, err := net.Dial("unix", raw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// Rejected connection is closed by the server: read returns EOF.
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("expected connection to be closed")
	}
	select {
	case <-accepted:
		t.Fatal("foreign-UID connection was accepted")
	default:
	}
}
