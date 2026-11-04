package testenv

import (
	"net"
	"path/filepath"
	"strings"
	"testing"
)

// A socket under ShortTempDir binds even when $TMPDIR is far past sun_path.
func TestShortTempDirBindsWithLongTMPDIR(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), strings.Repeat("d", 120)))
	p := filepath.Join(ShortTempDir(t), "supervise.sock")
	ln, err := net.Listen("unix", p)
	if err != nil {
		t.Fatalf("listen %s (%d bytes): %v", p, len(p), err)
	}
	_ = ln.Close()
}
