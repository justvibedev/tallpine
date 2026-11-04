package daemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/managedcfg"
	bridgesvc "tallpine.eu/tallpine/backend/internal/mobilebridge/service"
	"tallpine.eu/tallpine/backend/internal/settingsreg"
)

// newManagedLocks builds the locks from the managed file's "settings" dictionary
// for the registry's MDM-lockable keys (B212). Without a file it locks nothing.
func newManagedLocks() *managedcfg.Locks {
	return settingsreg.Default().ManagedLocks(agentpolicy.NewManagedSettingsReader().Read)
}

// mobileManagedOff reports whether the managed file sets app.mobile.enabled to
// false. A locked key whose value is unusable (unreadable file, wrong type)
// fails closed to off.
func (b *boot) mobileManagedOff() bool {
	raw, locked := b.managedLocks.Value("app.mobile.enabled")
	if !locked {
		return false
	}
	var on bool
	return raw == nil || json.Unmarshal(raw, &on) != nil || !on
}

// managedMobileInterval matches the managed-file reader's refresh period, so a
// policy pushed while the daemon runs takes effect within about one period.
const managedMobileInterval = 30 * time.Second

// enforceManagedMobile tears the mobile bridge down whenever the managed policy
// switches it off, until ctx ends. It is a no-op while the policy allows mobile.
func enforceManagedMobile(ctx context.Context, every time.Duration, bs *bridgesvc.BridgeService, log *slog.Logger) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := bs.EnforceManagedOff(); err != nil {
				log.Warn("apply managed mobile policy failed", "err", err)
			}
		}
	}
}
