package migratevo

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/brand"
)

// InstallCLILinks makes `tallpine` and `tp` available next to the installed
// CLI by symlinking them to the binary that is running, never replacing a file that is
// already there. `vo` is linked too as the transition name. A CLI inside a
// signed .app bundle is never written next to: that would break its signature.
func (e *Env) InstallCLILinks() error {
	if e.CLIDir == "" {
		return nil
	}
	if e.CLIBinary == "" {
		return nil
	}
	if strings.Contains(filepath.ToSlash(e.CLIDir)+"/", ".app/") {
		return nil
	}
	target := filepath.Join(e.CLIDir, e.CLIBinary)
	if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
		return nil // nothing to link to (running from a dev build)
	} else if err != nil {
		return err
	}
	for _, name := range []string{brand.CLIName, brand.CLIAlias, brand.LegacyCLIName} {
		if name == e.CLIBinary {
			continue
		}
		link := filepath.Join(e.CLIDir, name)
		if _, err := os.Lstat(link); err == nil {
			continue
		}
		if err := os.Symlink(e.CLIBinary, link); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
	}
	return nil
}
