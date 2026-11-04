//go:build darwin && cgo

package secretkey

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// TestKeychainScratch exercises the real Security framework on a scratch
// keychain file only; it never touches the login keychain. Creating a keychain
// file edits the user's keychain search list, so the test is opt-in
// (TALLPINE_KEYCHAIN_TEST=1) and restores the list afterwards.
func TestKeychainScratch(t *testing.T) {
	if os.Getenv("TALLPINE_KEYCHAIN_TEST") != "1" {
		t.Skip("set TALLPINE_KEYCHAIN_TEST=1 to run against a scratch keychain")
	}
	list, err := exec.Command("security", "list-keychains", "-d", "user").Output()
	if err != nil {
		t.Fatalf("read search list: %v", err)
	}
	var saved []string
	for _, f := range strings.Fields(string(list)) {
		saved = append(saved, strings.Trim(f, `"`))
	}
	path := filepath.Join(t.TempDir(), "scratch.keychain-db")
	// The scratch keychain file itself; the key item is created by our code.
	if out, err := exec.Command("security", "create-keychain", "-p", "scratch", path).CombinedOutput(); err != nil {
		t.Fatalf("create scratch keychain: %v %s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("security", "delete-keychain", path).Run()
		_ = exec.Command("security", append([]string{"list-keychains", "-d", "user", "-s"}, saved...)...).Run()
	})

	ks := New(Options{Keychain: path, NoUI: true})
	first, err := ks.Key(context.Background())
	if err != nil || len(first) != 32 {
		t.Fatalf("first Key = %d bytes, %v", len(first), err)
	}
	again, err := New(Options{Keychain: path, NoUI: true}).Key(context.Background())
	if err != nil || !bytes.Equal(first, again) {
		t.Fatalf("second Key differs or failed: %v", err)
	}

	if out, err := exec.Command("security", "lock-keychain", path).CombinedOutput(); err != nil {
		t.Fatalf("lock: %v %s", err, out)
	}
	// A planted item (same service/account) that another process can read is
	// refused, never used as the key: any-app (-A) and /usr/bin/security ACLs.
	for name, extra := range map[string][]string{"any app": {"-A"}, "security tool": nil} {
		squat := filepath.Join(t.TempDir(), "squat.keychain-db")
		if out, err := exec.Command("security", "create-keychain", "-p", "scratch", squat).CombinedOutput(); err != nil {
			t.Fatalf("create squat keychain: %v %s", err, out)
		}
		t.Cleanup(func() { _ = exec.Command("security", "delete-keychain", squat).Run() })
		args := append([]string{"add-generic-password", "-s", Service, "-a", Account, "-w", strings.Repeat("k", 32)}, extra...)
		if out, err := exec.Command("security", append(args, squat)...).CombinedOutput(); err != nil {
			t.Fatalf("%s: plant item: %v %s", name, err, out)
		}
		if key, err := New(Options{Keychain: squat, NoUI: true}).Key(context.Background()); !errors.Is(err, ports.ErrSecretKeyUnavailable) {
			t.Fatalf("%s: planted key accepted (%d bytes), err = %v", name, len(key), err)
		}
	}

	// Planted with -T <this binary>: the ACL check cannot tell it apart (the
	// store's key record refuses it); Recreate must replace it with a fresh key.
	{
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		squat := filepath.Join(t.TempDir(), "squat-t.keychain-db")
		if out, err := exec.Command("security", "create-keychain", "-p", "scratch", squat).CombinedOutput(); err != nil {
			t.Fatalf("create squat keychain: %v %s", err, out)
		}
		t.Cleanup(func() { _ = exec.Command("security", "delete-keychain", squat).Run() })
		planted := strings.Repeat("t", 32)
		if out, err := exec.Command("security", "add-generic-password", "-s", Service, "-a", Account, "-w", planted, "-T", self, squat).CombinedOutput(); err != nil {
			t.Fatalf("plant -T item: %v %s", err, out)
		}
		ks := New(Options{Keychain: squat, NoUI: true})
		fresh, err := ks.(interface {
			Recreate(context.Context) ([]byte, error)
		}).Recreate(context.Background())
		if err != nil || len(fresh) != 32 || string(fresh) == planted {
			t.Fatalf("Recreate = %q, %v; want a fresh key", fresh, err)
		}
		if again, err := ks.Key(context.Background()); err != nil || !bytes.Equal(again, fresh) {
			t.Fatalf("Key after Recreate = %v, %v", again, err)
		}
	}

	// Locked with no UI to unlock it: must fail, never invent a fresh key.
	if _, err := New(Options{Keychain: path, NoUI: true}).Key(context.Background()); !errors.Is(err, ports.ErrSecretKeyUnavailable) {
		t.Fatalf("locked keychain: err = %v, want ErrSecretKeyUnavailable", err)
	}
	missing := New(Options{Keychain: filepath.Join(t.TempDir(), "absent.keychain-db"), NoUI: true})
	if _, err := missing.Key(context.Background()); !errors.Is(err, ports.ErrSecretKeyUnavailable) {
		t.Fatalf("absent keychain: err = %v, want ErrSecretKeyUnavailable", err)
	}
}
