package brand

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// bareOfficialLauncher matches the ways an embedded hook launcher or generated script
// can invoke the official `ao` binary instead of this product's CLI.
var bareOfficialLauncher = regexp.MustCompile(`(?:spawn|spawnSync|which)\(\s*["']ao["']|run\([^,)]+,\s*["']ao["']|FileName\s*=\s*['"]ao['"]|["']ao["']\s*,\s*\[\s*["'](?:hooks|review)["']|\bao (?:hooks|review)\b|appendSessionHookFlagsForExecutable\([^)]*,\s*"ao"\)`)

// TestNoBareOfficialLauncherInAgentAdapters keeps embedded plugin assets and
// generated hook scripts from drifting back to a bare `ao` invocation. With the
// official AO on PATH such a hook would report to the wrong daemon, and in a
// daemon-spawned session (PATH pinned to `vo`) it would silently do nothing.
func TestNoBareOfficialLauncherInAgentAdapters(t *testing.T) {
	root := filepath.Join("..", "adapters")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		name := d.Name()
		if strings.HasSuffix(name, "_test.go") || (!strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, ".ts") && !strings.HasSuffix(name, ".js")) {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if loc := bareOfficialLauncher.FindIndex(data); loc != nil {
			t.Errorf("%s invokes a bare `ao` launcher: %q; use brand.CLIName (%q)", path, data[loc[0]:loc[1]], CLIName)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
