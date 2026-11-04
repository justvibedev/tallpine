package service

import (
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

// pairingCodeTTL is how long the displayed pairing code stays redeemable.
const pairingCodeTTL = mobilebridge.PairingCodeTTL

// pairingCodeState holds the pairing code in memory only. It is shown on the
// desktop, redeemed once at the LAN pair-exchange route, and never written to
// disk (only its hash lives in the LAN listener's auth state).
type pairingCodeState struct {
	mu     sync.Mutex
	code   string
	minted time.Time
}

// lanCredentials is the part of the LAN listener that holds device-auth state.
// Test fakes of LANController need not implement it.
type lanCredentials interface {
	SetLegacyPassword(hash string, until time.Time)
	SetLegacyKey(key []byte)
	SetRequireDevicePairing(on bool)
	SetPairedHook(fn func())
}

// armPairingCode arms code's hash on the listener and records it as the
// displayed code in one critical section, so the two cannot drift when Status,
// the paired hook and an enable or Regenerate interleave.
func (b *BridgeService) armPairingCode(code string) {
	b.pairing.mu.Lock()
	defer b.pairing.mu.Unlock()
	b.LAN.SetPasswordHash(mobilebridge.HashPassword(code))
	b.pairing.code, b.pairing.minted = code, time.Now()
}

// restorePairing undoes a failed enable: the previous code is armed and shown
// again, or just the previous hash when no code was ever shown.
func (b *BridgeService) restorePairing(prevCode, prevHash string) {
	if prevCode != "" {
		b.armPairingCode(prevCode)
		return
	}
	b.LAN.SetPasswordHash(prevHash)
}

func (b *BridgeService) shownPairingCode() string {
	b.pairing.mu.Lock()
	defer b.pairing.mu.Unlock()
	return b.pairing.code
}

// mintPairingCode generates a fresh code and arms its hash on the listener.
func (b *BridgeService) mintPairingCode() error {
	code, err := mobilebridge.GeneratePassword()
	if err != nil {
		return err
	}
	b.armPairingCode(code)
	return nil
}

// displayPairingCode returns the code to show, replacing one that expired or
// was redeemed. It is empty only if minting fails.
func (b *BridgeService) displayPairingCode() string {
	b.pairing.mu.Lock()
	stale := b.pairing.code == "" || time.Since(b.pairing.minted) >= pairingCodeTTL
	b.pairing.mu.Unlock()
	if stale && b.LAN != nil {
		if err := b.mintPairingCode(); err != nil {
			return ""
		}
	}
	b.pairing.mu.Lock()
	defer b.pairing.mu.Unlock()
	return b.pairing.code
}

// OnPaired is the pair-exchange hook: the redeemed code is gone, so show a new
// one without waiting for the next status poll to notice.
func (b *BridgeService) OnPaired() { _ = b.mintPairingCode() }

// armLANCredentials applies the persisted legacy-grace and transport settings
// to the listener, and registers the redemption hook.
func (b *BridgeService) armLANCredentials(st mobilebridge.State) {
	lc, ok := b.LAN.(lanCredentials)
	if !ok {
		return
	}
	if b.ConfigPath != "" && mobilebridge.IsSealedLegacyHash(st.LegacyPasswordHash) {
		if key, err := mobilebridge.LoadLegacyKey(b.ConfigPath); err == nil {
			lc.SetLegacyKey(key)
		}
	}
	lc.SetLegacyPassword(st.LegacyPasswordHash, st.LegacyUntil)
	lc.SetRequireDevicePairing(st.RequireDevicePairing)
	lc.SetPairedHook(b.OnPaired)
}
