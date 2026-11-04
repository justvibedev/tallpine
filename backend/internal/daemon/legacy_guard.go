package daemon

import (
	"fmt"
	"os"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/migratevo"
)

// guardLegacyInstall refuses to start on the default state root while a legacy
// (the legacy home) install still holds the data DB and the new root does not exist, so a
// packaged start never begins an empty install next to the real one. An
// explicit TALLPINE_DATA_DIR (tests, scratch daemons) bypasses it.
func guardLegacyInstall() error {
	if dir, ok := brand.LookupEnv("DATA_DIR"); ok && dir != "" {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home dir: %w", err)
	}
	return migratevo.CheckLegacyInstall(home)
}
