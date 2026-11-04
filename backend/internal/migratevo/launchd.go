package migratevo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/brand"
)

// BootoutLegacyLaunchd is step 6: it boots out every launchd agent registered
// under the legacy label prefix (ee.justdev.vo.*) and removes its plist. The
// new agents are registered by the new app on its first start. agentsDir is
// ~/Library/LaunchAgents; uid selects the gui/<uid> domain. A failed bootout
// (agent not loaded) is not an error.
func (e *Env) BootoutLegacyLaunchd(ctx context.Context, agentsDir string, uid int) error {
	plists, err := filepath.Glob(filepath.Join(agentsDir, brand.LegacyLaunchdLabelPrefix+".*.plist"))
	if err != nil {
		return err
	}
	for _, p := range plists {
		label := strings.TrimSuffix(filepath.Base(p), ".plist")
		if _, err := e.Run(ctx, "", "launchctl", "bootout", fmt.Sprintf("gui/%d/%s", uid, label)); err != nil {
			e.printf("launchd: bootout %s: %v (continuing)\n", label, err)
		}
		if err := os.Remove(p); err != nil {
			return fmt.Errorf("remove %s: %w", p, err)
		}
		e.printf("launchd: removed %s\n", label)
	}
	return nil
}
