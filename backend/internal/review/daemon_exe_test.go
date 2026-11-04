package review

import (
	"os"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/brand"
)

// fakeDaemonExe returns a daemon binary path whose directory holds the tp
// alias and tallpine transition name as non-executable siblings, so PATH pinning
// uses the directory itself rather than a generated shim directory.
func fakeDaemonExe(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	for _, name := range []string{brand.CLIAlias} {
		if err := os.WriteFile(filepath.Join(d, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(d, brand.CLIName)
}
