package daemontoken

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"tallpine.eu/tallpine/backend/internal/buildinfo"
)

func TestPathSitsNextToRunFile(t *testing.T) {
	got := Path(filepath.Join("/home/u/.tallpine", "running.json"))
	if want := filepath.Join("/home/u/.tallpine", "daemon.token"); got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
}

func TestEnsureCreatesOwnerOnlyTokenAndReusesIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", FileName)
	first, err := Ensure(path)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if !wellFormed(first) {
		t.Fatalf("token %q is not 32 base64url bytes", first)
	}
	assertOwnerOnly(t, path)

	second, err := Ensure(path)
	if err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if second != first {
		t.Fatal("Ensure regenerated a valid token; it must persist across restarts")
	}
	read, err := Read(path)
	if err != nil || read != first {
		t.Fatalf("Read = %q, %v; want the ensured token", read, err)
	}
}

func TestEnsureRegeneratesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	first, err := Ensure(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	second, err := Ensure(path)
	if err != nil {
		t.Fatal(err)
	}
	if second == first || !wellFormed(second) {
		t.Fatalf("expected a fresh token after deletion, got %q", second)
	}
	assertOwnerOnly(t, path)
}

func TestEnsureReplacesMalformedToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte("not-a-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	token, err := Ensure(path)
	if err != nil {
		t.Fatal(err)
	}
	if !wellFormed(token) {
		t.Fatalf("malformed token was kept: %q", token)
	}
}

func TestEnsureReplacesGroupReadableToken(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	path := filepath.Join(t.TempDir(), FileName)
	leaked, err := Ensure(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	token, err := Ensure(path)
	if err != nil {
		t.Fatal(err)
	}
	if token == leaked {
		t.Fatal("a token that was readable by others must be rotated")
	}
	assertOwnerOnly(t, path)
}

func TestEnsureConcurrentCallersConverge(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	const n = 8
	tokens := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tok, err := Ensure(path)
			if err != nil {
				t.Errorf("Ensure: %v", err)
			}
			tokens[i] = tok
		}()
	}
	wg.Wait()
	for _, tok := range tokens[1:] {
		if tok != tokens[0] {
			t.Fatalf("racing Ensure calls produced different tokens: %q vs %q", tok, tokens[0])
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}

func TestReadMissing(t *testing.T) {
	if _, err := Read(filepath.Join(t.TempDir(), FileName)); !errors.Is(err, ErrMissing) {
		t.Fatalf("Read missing = %v, want ErrMissing", err)
	}
}

func TestEqual(t *testing.T) {
	if !Equal("abc", "abc") {
		t.Fatal("equal tokens must match")
	}
	for _, presented := range []string{"", "abd", "abcd", "ab"} {
		if Equal(presented, "abc") {
			t.Fatalf("Equal(%q, abc) = true", presented)
		}
	}
	if Equal("", "") {
		t.Fatal("an empty expected token must never match")
	}
}

func TestAuthDisabled(t *testing.T) {
	for value, want := range map[string]bool{"off": true, " OFF ": true, "": false, "enforce": false, "on": false, "0": false} {
		if got := AuthDisabled(value); got != want {
			t.Errorf("AuthDisabled(%q) = %v, want %v", value, got, want)
		}
	}
}

func assertOwnerOnly(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("token file mode = %o, want 600", perm)
	}
}

func TestRestoreRepublishesWithoutOverwriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := Restore(path, "kept"); err != nil {
		t.Fatal(err)
	}
	assertOwnerOnly(t, path)
	if err := Restore(path, "other"); err != nil {
		t.Fatal(err)
	}
	if got, _ := Read(path); got != "kept" {
		t.Fatalf("Restore overwrote an existing token: %q", got)
	}
}

func TestAuthOffHonoredNeedsBothFlags(t *testing.T) {
	if AuthOffHonored("off", "") || AuthOffHonored("off", "0") || AuthOffHonored("", "1") {
		t.Fatal("auth off must need both TALLPINE_DAEMON_AUTH=off and TALLPINE_DAEMON_AUTH_UNSAFE_DEV=1")
	}
	if !AuthOffHonored("off", "1") {
		t.Fatal("both flags set must disable auth")
	}
}

func TestAuthOffIgnoredInReleaseBuild(t *testing.T) {
	saved := buildinfo.Version
	t.Cleanup(func() { buildinfo.Version = saved })
	buildinfo.Version = "0.13.0"
	if AuthOffHonored("off", "1") {
		t.Fatal("a stamped release build must ignore TALLPINE_DAEMON_AUTH=off even with the second flag")
	}
}
