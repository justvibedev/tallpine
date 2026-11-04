package daemontoken

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRotateOnStartChangesInstallTokenKeepsSessionKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", FileName)
	tok1, key1, err := RotateOnStart(path)
	if err != nil {
		t.Fatal(err)
	}
	tok2, key2, err := RotateOnStart(path)
	if err != nil {
		t.Fatal(err)
	}
	if tok1 == tok2 {
		t.Fatal("install token did not change across two daemon starts")
	}
	if key1 != key2 {
		t.Fatal("session key changed across starts; running agents would lose their tokens")
	}
	if got, _ := Read(path); got != tok2 {
		t.Fatalf("file holds %q, want the latest token", got)
	}
	if Equal(tok1, tok2) {
		t.Fatal("old token still matches the rotated one")
	}
	assertOwnerOnly(t, path)
	assertOwnerOnly(t, SessionKeyPath(path))
}

func TestRotateOnStartSeedsSessionKeyFromLegacyToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	legacy, err := Ensure(path)
	if err != nil {
		t.Fatal(err)
	}
	tok, key, err := RotateOnStart(path)
	if err != nil {
		t.Fatal(err)
	}
	if key != legacy {
		t.Fatal("existing install's session key must be its old install token so live agent tokens survive")
	}
	if tok == legacy {
		t.Fatal("install token was not rotated on first start after upgrade")
	}
}

func TestRotateReplacesWidenedFileAndSymlink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	target := filepath.Join(dir, "elsewhere")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := Rotate(path); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(target); string(b) != "keep" {
		t.Fatal("rotation wrote through the symlink")
	}
	assertOwnerOnly(t, path)
}
