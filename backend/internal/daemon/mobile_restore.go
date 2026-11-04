package daemon

import (
	"fmt"

	"tallpine.eu/tallpine/backend/internal/mobilebridge"
	bridgesvc "tallpine.eu/tallpine/backend/internal/mobilebridge/service"
)

// restoreMobileOnBoot re-arms the Connect Mobile LAN listener across daemon
// restarts. If the persisted state says the bridge was enabled, it delegates to
// BridgeService.RestoreOnBoot, which reuses the existing password (no rotation
// — the paired phone keeps working), derives the auth hash in memory, restarts
// the listener on its last bound port, and — critically — re-applies the
// secure-pairing `tailscale serve` proxy against whatever port Start actually
// returned, not the persisted port. That distinction matters: Start falls back
// to an ephemeral port when the persisted one is taken, and a stale `serve`
// config would otherwise keep proxying the tailnet at a dead (or, worse,
// someone else's) port. A non-nil return means the listener failed to
// (re)bind; the caller logs it as a warning and continues booting regardless —
// Connect Mobile is best-effort, not load-bearing.
func restoreMobileOnBoot(path string, bs *bridgesvc.BridgeService) error {
	// Managed policy first: a listener left running by an earlier boot is torn
	// down and the feature flag persisted off before anything is restored.
	if err := bs.EnforceManagedOff(); err != nil {
		return fmt.Errorf("apply managed mobile policy: %w", err)
	}
	state, err := mobilebridge.Load(path)
	if err != nil {
		return fmt.Errorf("load mobile bridge state: %w", err)
	}
	// The feature flag is off by default: an install that predates it (or a user
	// who switched mobile off) gets no listener, tunnel or connector at boot.
	if !state.FeatureEnabled || !state.Enabled {
		return nil
	}
	if err := bs.RestoreOnBoot(state); err != nil {
		return fmt.Errorf("restart mobile LAN listener: %w", err)
	}
	return nil
}
