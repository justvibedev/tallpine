package daemon

import (
	"context"
	"log/slog"
	"time"

	"tallpine.eu/tallpine/backend/internal/peeradmit"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
)

// peerCheckStore persists the owner's peer check switch (app_settings).
type peerCheckStore interface {
	SetPeerCheck(ctx context.Context, value string, now time.Time) error
}

// peerCheckSwitch is settingswrite's owner of app.daemon.peerCheck: it
// stores the switch and applies it to the running checker.
type peerCheckSwitch struct {
	checker *peeradmit.Checker
	store   peerCheckStore
	now     func() time.Time
}

func (s peerCheckSwitch) DesktopCaller(ctx context.Context) bool {
	return s.checker.DesktopCaller(ctx)
}

func (s peerCheckSwitch) Set(ctx context.Context, value string) error {
	if err := s.store.SetPeerCheck(ctx, value, s.now()); err != nil {
		return err
	}
	s.checker.SetDisabled(value == settingssvc.PeerCheckOff)
	return nil
}

// applyStoredPeerCheck applies the stored switch before the daemon serves. A
// switch turned off on disk, perhaps with sqlite3 while the daemon was
// stopped, is logged loudly. An unreadable row keeps the build's mode: a
// failure must not turn the check off.
func applyStoredPeerCheck(ctx context.Context, checker *peeradmit.Checker, read func(context.Context) (string, error), log *slog.Logger) {
	value, err := read(ctx)
	if err != nil {
		log.Error("peer check: could not read the off switch; keeping the build's mode", "err", err)
		return
	}
	off := settingssvc.PeerCheckOrDefault(value) == settingssvc.PeerCheckOff
	checker.SetDisabled(off)
	if off && checker.Active() {
		log.Warn("peer check: OFF by the owner's switch; any process holding a daemon token is let in",
			"setting", "app.daemon.peerCheck")
	}
}
