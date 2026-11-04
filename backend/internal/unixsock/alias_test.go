//go:build !windows

package unixsock

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/testenv"
)

func shortRoot(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp(testenv.ShortBaseDir(), "ts-alias-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

func TestListenAliasedSocketIsPrivateAtBind(t *testing.T) {
	root, data := shortRoot(t), filepath.Join(t.TempDir(), strings.Repeat("long-dir-", 12))
	var mode os.FileMode
	afterBind = func(p string) { fi, _ := os.Stat(p); mode = fi.Mode().Perm() }
	t.Cleanup(func() { afterBind = nil })
	ln, path, err := ListenAliasedAt(root, data, "p-", "a.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if mode&0o077 != 0 {
		t.Fatalf("mode at bind = %o, want no group/other bits", mode)
	}
	if len(path) > MaxPathBytes || strings.Contains(path, data) {
		t.Fatalf("path not short: %q", path)
	}
	if tgt, _ := os.Readlink(filepath.Dir(path)); tgt != data {
		t.Fatalf("alias target %q, want %q", tgt, data)
	}
	if fi, _ := os.Stat(data); fi.Mode().Perm() != 0o700 {
		t.Fatalf("data dir mode %o", fi.Mode().Perm())
	}
	_ = ln.Close()
	if _, err := os.Lstat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("alias not removed: %v", err)
	}
}

func TestListenAliasedDropsOtherUIDPeer(t *testing.T) {
	root := shortRoot(t)
	ln, path, err := ListenAliasedAt(root, t.TempDir(), "p-", "a.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	// Rewrap with a UID that is not ours: the real listener must have the
	// owner filter installed, so check it rejects when expecting another UID.
	inner := ln.(*aliasListener).Listener
	if _, ok := inner.(*ownerOnlyListener); !ok {
		t.Fatalf("listener is %T, want owner-only wrapper", inner)
	}
	other := NewOwnerOnly(inner.(*ownerOnlyListener).Listener, os.Getuid()+1)
	type res struct {
		c   net.Conn
		err error
	}
	done := make(chan res, 1)
	go func() { c, err := other.Accept(); done <- res{c, err} }()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	select {
	case r := <-done:
		t.Fatalf("peer of another UID was accepted: conn=%v err=%v", r.c, r.err)
	case <-time.After(300 * time.Millisecond): // dropped: Accept still waiting
	}
	_ = other.Close()
	<-done
}

func TestListenAliasedRetriesOnCollision(t *testing.T) {
	root := shortRoot(t)
	seq := []string{"0000000000000001", "0000000000000001", "0000000000000002"}
	randomSuffix = func() (string, error) { s := seq[0]; seq = seq[1:]; return s, nil }
	t.Cleanup(func() { randomSuffix = defaultRandomSuffix })
	first, p1, err := ListenAliasedAt(root, t.TempDir(), "p-", "a.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, p2, err := ListenAliasedAt(root, t.TempDir(), "p-", "a.sock")
	if err != nil {
		t.Fatalf("collision not retried: %v", err)
	}
	defer second.Close()
	if filepath.Dir(p1) == filepath.Dir(p2) {
		t.Fatal("aliases collided")
	}
}

func TestCleanupStaleAliasesByPID(t *testing.T) {
	root := shortRoot(t)
	for name := range map[string]bool{
		"p-101-aaaaaaaaaaaaaaaa": true, "p-202-bbbbbbbbbbbbbbbb": true, "q-101-aaaaaaaaaaaaaaaa": true, "p-101-short": true,
	} {
		if err := os.Symlink("/nonexistent", filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := cleanupStaleAliases(root, "p-", func(pid int) bool { return pid == 202 }); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(root)
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if strings.Join(got, ",") != "p-101-short,p-202-bbbbbbbbbbbbbbbb,q-101-aaaaaaaaaaaaaaaa" {
		t.Fatalf("remaining = %v", got)
	}
}

func TestListenAliasedRejectsOverlongSockPath(t *testing.T) {
	root := shortRoot(t)
	_, _, err := ListenAliasedAt(root, t.TempDir(), "p-", strings.Repeat("x", 100)+".sock")
	if err == nil || !strings.Contains(err.Error(), "maximum") {
		t.Fatalf("err = %v, want sun_path cap error", err)
	}
	if m, _ := filepath.Glob(filepath.Join(root, "p-*")); len(m) != 0 {
		t.Fatalf("alias leaked: %v", m)
	}
}

func TestIsOwnerOnly(t *testing.T) {
	ln, _, err := ListenAliasedAt(shortRoot(t), t.TempDir(), "p-", "a.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if !IsOwnerOnly(ln) {
		t.Fatal("aliased listener lacks owner filter")
	}
	plain, _ := net.Listen("unix", filepath.Join(shortRoot(t), "x.sock"))
	defer plain.Close()
	if IsOwnerOnly(plain) {
		t.Fatal("plain listener reported owner-only")
	}
}
