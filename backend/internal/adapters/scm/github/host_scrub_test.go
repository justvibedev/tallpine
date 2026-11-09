package github

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestDefaultGHTokenSourceScrubsAmbientToken runs the real default source
// against a fake gh that echoes $GH_TOKEN.
func TestDefaultGHTokenSourceScrubsAmbientToken(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a shell-script gh")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '%s' \"$GH_TOKEN\"\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GH_TOKEN", "ambient")
	h := newHostClients(ProviderOptions{}, nil)

	if _, err := h.ghToken("stranger.ghe.com", true).Token(ctx()); !errors.Is(err, ErrNoToken) {
		t.Fatalf("stored-login-only err = %v, want ErrNoToken", err)
	}
	if got, err := h.ghToken("corp.ghe.com", false).Token(ctx()); err != nil || got != "ambient" {
		t.Fatalf("listed = %q, %v; want ambient", got, err)
	}
}
