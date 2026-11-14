package statefence

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/sessionartifacts"
)

// A session created before the per-project layout keeps writing to its flat
// dir; the seatbelt checks resolved paths, so WritePaths grants it while it exists.
func TestWritePathsGrantsALegacyFlatArtifactDir(t *testing.T) {
	data := t.TempDir()
	env := map[string]string{brand.Env("DATA_DIR"): data, brand.Env("SESSION_ID"): "tw-2"}
	legacy := sessionartifacts.LegacyDir(data, "tw-2")
	if slices.Contains(WritePaths(env, false), legacy) {
		t.Fatal("legacy path granted although it does not exist")
	}
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	got := WritePaths(env, false)
	if !slices.Contains(got, legacy) || !slices.Contains(got, sessionartifacts.Dir(data, "tw-2")) {
		t.Fatalf("WritePaths = %v, want the flat and the canonical dir", got)
	}
}

// A flat dir that is another project's folder (project "tw-2" beside session
// "tw-2") must not be granted: it holds other sessions' artifacts.
func TestWritePathsDoesNotGrantAProjectFolder(t *testing.T) {
	data := t.TempDir()
	env := map[string]string{brand.Env("DATA_DIR"): data, brand.Env("SESSION_ID"): "tw-2"}
	folder := sessionartifacts.LegacyDir(data, "tw-2")
	if err := os.MkdirAll(filepath.Join(folder, "tw-2-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(WritePaths(env, false), folder) {
		t.Fatal("a project folder was granted as the session's artifact dir")
	}
}
