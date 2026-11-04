package migratevo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetAsidePreexistingTarget(t *testing.T) {
	home := t.TempDir()
	e := &Env{Roots: NewRoots(home)}
	if err := os.MkdirAll(filepath.Join(e.Target, "electron"), 0o700); err != nil {
		t.Fatal(err)
	}
	aside, err := e.SetAsidePreexistingTarget()
	if err != nil || aside == "" {
		t.Fatalf("aside=%q err=%v", aside, err)
	}
	if _, err := os.Lstat(e.Target); !os.IsNotExist(err) {
		t.Fatalf("target should be gone: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(aside, "electron")); err != nil {
		t.Fatal(err)
	}
	// a target that holds a DB is never moved
	if err := os.MkdirAll(filepath.Dir(e.TargetDB()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.TargetDB(), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.SetAsidePreexistingTarget(); err == nil {
		t.Fatal("moved a target holding a DB")
	}
}
