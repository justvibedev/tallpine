package migratevo

import (
	"errors"
	"fmt"
	"os"
)

// ErrLegacyInstall is returned by CheckLegacyInstall.
var ErrLegacyInstall = errors.New("legacy install not migrated")

// CheckLegacyInstall refuses a start that would silently begin a fresh install
// next to a legacy one: it fails when the old state root still holds the data
// DB and the new state root has no data DB yet (the app may already have
// created a bare new home). The message points at the
// migrator. Any other combination (nothing legacy, already migrated, or both
// DBs present) returns nil.
func CheckLegacyInstall(home string) error {
	r := NewRoots(home)
	// An unfinished migration comes first: after StepRename the legacy DB is
	// gone, so only the plan tells that the tree is not fully rewritten yet.
	if p, err := r.LoadPlan(); err == nil && p != nil && !p.migrationComplete() {
		return fmt.Errorf("%w: a migration is unfinished (%s); quit Tallpine and run `tallpine migrate-from-vo` to resume it",
			ErrLegacyInstall, r.FindPlanFile())
	}
	if _, err := os.Lstat(r.SourceDB()); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // no legacy DB: nothing to protect
		}
		return fmt.Errorf("check legacy install: %w", err)
	}
	if _, err := os.Lstat(r.TargetDB()); err == nil {
		return nil
	}
	return fmt.Errorf("%w: found %s but %s does not exist; quit Tallpine and the old app, then run `tallpine migrate-from-vo` (add --dry-run to preview)",
		ErrLegacyInstall, r.SourceDB(), r.TargetDB())
}
