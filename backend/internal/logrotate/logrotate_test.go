package logrotate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRotatesAndCapsBackups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "daemon.log")
	w, err := Open(path, 100, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	line := strings.Repeat("x", 59) + "\n" // 60 bytes: two lines exceed the 100 byte cap
	for i := 0; i < 10; i++ {
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"daemon.log", "daemon.log.1", "daemon.log.2"} {
		info, err := os.Stat(filepath.Join(filepath.Dir(path), name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if info.Size() > 100 || info.Size()%60 != 0 {
			t.Fatalf("%s size = %d, want whole lines within the cap", name, info.Size())
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v, want 0600", name, info.Mode().Perm())
		}
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("daemon.log.3 should not exist (keep=2): %v", err)
	}
}

func TestReopenAppendsToExistingSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	w, _ := Open(path, 100, 1)
	_, _ = w.Write([]byte(strings.Repeat("a", 80)))
	_ = w.Close()
	w, _ = Open(path, 100, 1)
	defer w.Close()
	_, _ = w.Write([]byte(strings.Repeat("b", 30))) // 80+30 > 100: must rotate, not grow
	if info, _ := os.Stat(path); info.Size() != 30 {
		t.Fatalf("active size = %d, want 30 after rotation", info.Size())
	}
	if info, _ := os.Stat(path + ".1"); info == nil || info.Size() != 80 {
		t.Fatal("previous contents should be in daemon.log.1")
	}
}

func TestWriteAfterCloseFails(t *testing.T) {
	w, _ := Open(filepath.Join(t.TempDir(), "d.log"), 100, 1)
	_ = w.Close()
	if _, err := w.Write([]byte("x")); err == nil {
		t.Fatal("write after close should fail")
	}
}

func TestRotationReopenFailureKeepsLogging(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	w, err := Open(path, 100, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	clock := time.Unix(1000, 0)
	w.now = func() time.Time { return clock }
	realOpen := w.openFile
	failOpen := true
	w.openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		if failOpen {
			return nil, errors.New("simulated EMFILE")
		}
		return realOpen(name, flag, perm)
	}

	line := strings.Repeat("x", 59) + "\n"
	for i := 0; i < 5; i++ {
		if n, err := w.Write([]byte(line)); err != nil || n != len(line) {
			t.Fatalf("write %d during reopen failure: n=%d err=%v", i, n, err)
		}
	}
	// Every line must have landed somewhere (the pre-rotation handle).
	var total int64
	for _, name := range []string{"", ".1", ".2"} {
		if info, err := os.Stat(path + name); err == nil {
			total += info.Size()
		}
	}
	if total != int64(5*len(line)) {
		t.Fatalf("persisted %d bytes, want %d: lines were dropped", total, 5*len(line))
	}

	// Once opening works again (after the retry delay) rotation resumes.
	failOpen = false
	clock = clock.Add(2 * rotateRetryDelay)
	if _, err := w.Write([]byte(line)); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Size() != int64(len(line)) {
		t.Fatalf("active file after recovery: %v %v, want one fresh line", info, err)
	}
}
