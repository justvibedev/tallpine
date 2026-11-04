//go:build !windows

package hostsock

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

// useAliasRoot redirects /tmp aliases to a private short dir.
func useAliasRoot(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp(testenv.ShortBaseDir(), "hs")
	if err != nil {
		t.Fatal(err)
	}
	old := aliasRoot
	aliasRoot = root
	t.Cleanup(func() { aliasRoot = old; _ = os.RemoveAll(root) })
	return root
}

func echoOnce(t *testing.T, ln net.Listener) {
	t.Helper()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 4)
		n, _ := c.Read(buf)
		_, _ = c.Write(buf[:n])
	}()
}

func roundTrip(t *testing.T, addr string) error {
	t.Helper()
	conn, err := Dial(context.Background(), addr, time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, _ = conn.Write([]byte("ping"))
	buf := make([]byte, 4)
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := conn.Read(buf); err != nil {
		return err
	}
	return nil
}

func TestListenDialAndCleanup(t *testing.T) {
	useAliasRoot(t)
	dir := filepath.Join(testenv.ShortTempDir(t), "hostsock")
	ln, addr, err := Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !IsUnix(addr) {
		t.Fatalf("addr %q not unix", addr)
	}
	info, _ := os.Stat(dir)
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", info.Mode().Perm())
	}
	si, _ := os.Lstat(Path(addr))
	if si.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode %v", si.Mode().Perm())
	}
	echoOnce(t, ln)
	if err := roundTrip(t, addr); err != nil {
		t.Fatal(err)
	}
	// A "restarted daemon" only has the registry address string: dial again.
	echoOnce(t, ln)
	if err := roundTrip(t, string([]byte(addr))); err != nil {
		t.Fatalf("reattach: %v", err)
	}
	_ = ln.Close()
	if _, err := os.Lstat(Path(addr)); !os.IsNotExist(err) {
		t.Fatalf("socket left behind: %v", err)
	}
}

func TestLongDirUsesShortAliasAndIsRemoved(t *testing.T) {
	root := useAliasRoot(t)
	dir := filepath.Join(t.TempDir(), strings.Repeat("d", 90), "hostsock")
	ln, addr, err := Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(Path(addr)) > maxPathBytes || !strings.HasPrefix(Path(addr), root) {
		t.Fatalf("addr not via short alias: %q", addr)
	}
	echoOnce(t, ln)
	if err := roundTrip(t, addr); err != nil {
		t.Fatal(err)
	}
	_ = ln.Close()
	if m, _ := filepath.Glob(filepath.Join(root, aliasPrefix()+"*")); len(m) != 0 {
		t.Fatalf("alias left behind: %v", m)
	}
}

func TestDialRefusesWrongOwner(t *testing.T) {
	useAliasRoot(t)
	ln, addr, err := Listen(filepath.Join(testenv.ShortTempDir(t), "hostsock"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	old := currentUID
	currentUID = func() int { return os.Getuid() + 1 }
	defer func() { currentUID = old }()
	if _, err := Dial(context.Background(), addr, time.Second); err == nil || !strings.Contains(err.Error(), "owned by") {
		t.Fatalf("want owner refusal, got %v", err)
	}
}

func TestDialRefusesSquattedAlias(t *testing.T) {
	root := useAliasRoot(t)
	dir := filepath.Join(testenv.ShortTempDir(t), "hostsock")
	ln, addr, err := Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	alias := filepath.Join(root, "squat")
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	old := currentUID
	currentUID = func() int { return os.Getuid() + 1 }
	defer func() { currentUID = old }()
	if err := verifyOwner(filepath.Join(alias, filepath.Base(Path(addr)))); err == nil {
		t.Fatal("expected refusal for foreign-owned symlink/socket")
	}
}

func TestListenSweepsDeadSocket(t *testing.T) {
	useAliasRoot(t)
	dir := filepath.Join(testenv.ShortTempDir(t), "hostsock")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	dead := filepath.Join(dir, "dead.sock")
	raw, err := net.ListenUnix("unix", &net.UnixAddr{Name: dead, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	raw.SetUnlinkOnClose(false)
	_ = raw.Close() // a killed host leaves its socket file behind
	ln, _, err := Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if _, err := os.Lstat(dead); !os.IsNotExist(err) {
		t.Fatalf("dead socket not swept: %v", err)
	}
}

func TestDefaultDirIsAbsoluteForRelativeOverride(t *testing.T) {
	SetDir("")
	registered = ""
	t.Setenv(DirEnv(), "")
	t.Setenv(brand.Env("DATA_DIR"), "rel-data")
	got, err := DefaultDir()
	if err != nil || !filepath.IsAbs(got) {
		t.Fatalf("DefaultDir = %q, %v; want absolute", got, err)
	}
}

func TestDefaultDirPrefersDaemonRegisteredDir(t *testing.T) {
	t.Setenv(brand.Env("DATA_DIR"), "rel-data")
	SetDir("/abs/data")
	defer func() { registered = "" }()
	if got, _ := DefaultDir(); got != "/abs/data/"+DirName {
		t.Fatalf("got %q", got)
	}
}

// Old builds named aliases <prefix><uid>-<hex>. The pid-based sweep must not
// mistake the uid for a dead pid and remove a live host's alias; stale ones
// (no socket) are still swept.
func TestLegacyUIDAliasesSweptByLivenessNotPid(t *testing.T) {
	root := useAliasRoot(t)
	dir := filepath.Join(t.TempDir(), "hostsock")
	if err := ensurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	oldAge := legacyAliasMinAge
	legacyAliasMinAge = 0
	t.Cleanup(func() { legacyAliasMinAge = oldAge })
	mk := func(name string) string {
		a := filepath.Join(root, name)
		if err := os.Symlink(dir, a); err != nil {
			t.Fatal(err)
		}
		return a
	}
	live := mk(legacyAliasPrefix() + "999999-0123456789abcdef") // uid 999999 reads as a dead pid
	stale := mk(legacyAliasPrefix() + "999998-0123456789abcdef")
	// A live socket behind the first alias only: bind it directly in dir.
	ln, err := net.Listen("unix", filepath.Join(dir, "h.sock"))
	if err != nil {
		t.Skipf("bind: %v", err)
	}
	defer ln.Close()
	sweepStale(dir)
	if _, err := os.Lstat(live); err != nil {
		t.Fatalf("live legacy alias removed by pid sweep: %v", err)
	}
	_ = ln.Close()
	_ = os.Remove(filepath.Join(dir, "h.sock"))
	sweepStale(dir)
	if _, err := os.Lstat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale legacy alias survived: %v", err)
	}
}
