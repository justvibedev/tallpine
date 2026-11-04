//go:build !windows

package browserruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/testenv"
	"tallpine.eu/tallpine/backend/internal/unixsock"
)

func TestListenUnixUsesShortAliasForLongDataPath(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), strings.Repeat("long-data-directory-", 6))
	runFilePath := filepath.Join(runtimeDir, "running.json")
	// t.TempDir inherits TMPDIR, which can itself exceed sockaddr_un's limit in
	// a normal Tallpine worktree. Use the same deliberately short spelling as Listen.
	aliasRoot, err := os.MkdirTemp(testenv.ShortBaseDir(), "tallpine-br-test-")
	if err != nil {
		t.Fatalf("create short alias root: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(aliasRoot) })

	ln, address, err := listenAt(aliasRoot, runFilePath)
	if err != nil {
		t.Fatalf("listenAt: %v", err)
	}
	aliasPath := filepath.Dir(address)
	if len([]byte(address)) > unixsock.MaxPathBytes {
		t.Fatalf("socket path = %d bytes, maximum %d: %q", len([]byte(address)), unixsock.MaxPathBytes, address)
	}
	if strings.Contains(address, runtimeDir) {
		t.Fatalf("socket address retained long runtime path: %q", address)
	}
	if target, err := os.Readlink(aliasPath); err != nil || target != runtimeDir {
		t.Fatalf("alias target = %q, err=%v; want %q", target, err, runtimeDir)
	}

	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	if _, err := os.Lstat(aliasPath); !os.IsNotExist(err) {
		t.Fatalf("runtime alias was not removed on close: %v", err)
	}
}

// A plain net.Listen + later chmod leaves the socket reachable by other UIDs
// and umask-dependent at bind; the shared listener must be 0600 and owner-only.
func TestListenSocketIsPrivate(t *testing.T) {
	ln, path, err := Listen(filepath.Join(t.TempDir(), "running.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %v err=%v, want 0600", info, err)
	}
	if !unixsock.IsOwnerOnly(ln) {
		t.Fatal("browser runtime listener has no owner-UID filter")
	}
}
