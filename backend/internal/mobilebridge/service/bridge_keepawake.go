package service

import (
	"errors"
	"os"

	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

func (b *BridgeService) keepAwakeStatus(on, lidClosed bool) KeepAwakeStatus {
	if b.KeepAwake == nil {
		return KeepAwakeStatus{}
	}
	res := KeepAwakeStatus{
		Supported:  true,
		Enabled:    on,
		Active:     b.KeepAwake.Active(),
		HasBattery: b.KeepAwake.HasBattery(),
	}
	res.LidClosed = lidClosed
	res.LidSupported, res.LidActive, res.LidReason = b.KeepAwake.LidStatus()
	return res
}

// applyKeepAwakeLocked holds the machine awake only while the user opted in
// and the bridge is actually serving: there is no phone to stay reachable for
// otherwise. The caller owns transitionMu.
func (b *BridgeService) applyKeepAwakeLocked(on, lidClosed bool) error {
	if b.KeepAwake == nil {
		return nil
	}
	if on && b.LAN.Running() {
		if err := b.KeepAwake.Start(); err != nil {
			return err
		}
		// Idle wake succeeded. Native lid failures are reported in LidReason,
		// and must not turn that success into an HTTP error.
		_ = b.KeepAwake.ConfigureLid(lidClosed, false)
		return nil
	}
	b.KeepAwake.Stop()
	return nil
}

// SetKeepAwake persists the keep-awake choice and applies it immediately.
func (b *BridgeService) SetKeepAwake(on bool) (MobileStatusResponse, error) {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()

	if b.KeepAwake == nil {
		return MobileStatusResponse{}, errors.New("keeping this machine awake is only supported on macOS")
	}
	st, err := mobilebridge.Load(b.ConfigPath)
	if err != nil && !os.IsNotExist(err) {
		return MobileStatusResponse{}, err
	}
	st.KeepAwake = on
	if err := mobilebridge.Save(b.ConfigPath, st); err != nil {
		return MobileStatusResponse{}, err
	}
	if err := b.applyKeepAwakeLocked(on && st.Enabled, st.KeepAwakeLid); err != nil {
		return MobileStatusResponse{}, err
	}
	return b.Status(), nil
}

// SetKeepAwakeLid is desktop-local like the existing mobile control endpoint.
// Older requests omit lidClosed and retain the separately saved lid preference.
func (b *BridgeService) SetKeepAwakeLid(on, lidClosed bool) (MobileStatusResponse, error) {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()
	if b.KeepAwake == nil {
		return MobileStatusResponse{}, errors.New("keeping this machine awake is only supported on macOS")
	}
	if lidClosed {
		supported, _, reason := b.KeepAwake.LidStatus()
		if !supported {
			if reason == "isolated_data_dir" {
				return MobileStatusResponse{}, errors.New("closed-lid wakefulness is disabled for custom or isolated TALLPINE_DATA_DIR directories; use the default ~/.tallpine/data directory to enable it")
			}
			return MobileStatusResponse{}, errors.New("closed-lid wakefulness requires a supported Apple Silicon MacBook (macOS 13–27) and the desktop helper")
		}
	}
	st, err := mobilebridge.Load(b.ConfigPath)
	if err != nil && !os.IsNotExist(err) {
		return MobileStatusResponse{}, err
	}
	// Register before committing a new opt-in; failure preserves both preferences.
	if lidClosed && !st.KeepAwakeLid {
		if err := b.KeepAwake.ConfigureLid(false, true); err != nil {
			return MobileStatusResponse{}, err
		}
	}
	st.KeepAwake, st.KeepAwakeLid = on, lidClosed
	if err := mobilebridge.Save(b.ConfigPath, st); err != nil {
		return MobileStatusResponse{}, err
	}
	if err := b.applyKeepAwakeLocked(on && st.Enabled, st.KeepAwakeLid); err != nil {
		return MobileStatusResponse{}, err
	}
	return b.Status(), nil
}

// ShutdownKeepAwake releases the sleep assertion on the way out. caffeinate
// also exits on its own when the daemon does (-w), so this only makes a clean
// quit release it without waiting on that. The preference stays set.
func (b *BridgeService) ShutdownKeepAwake() {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()

	if b.KeepAwake != nil {
		b.KeepAwake.Stop()
	}
}
