package mobilebridge

import "time"

// LegacyGrace is how long a phone paired with the old connection password
// (sent as the bearer on every request) keeps working after this daemon first
// migrates its config. During it the phone app upgrades itself to a per-device
// token. The window only ends the old password when State.RequireDevicePairing
// is on (the default); when the user turns it off a legacy pairing keeps working
// so older mobile apps are not locked out. When on, the old password is refused
// afterwards and the phone is asked to re-pair once.
const LegacyGrace = 14 * 24 * time.Hour

// migrateLegacyPassword moves a plaintext Password written by an older daemon
// into LegacyPasswordHash, starts the grace window once, and clears the
// plaintext. It reports whether s changed. A state with no Password is left
// alone, so the window never restarts.
func migrateLegacyPassword(s *State, now time.Time) bool {
	if s.Password == "" {
		return false
	}
	if s.LegacyPasswordHash == "" {
		s.LegacyPasswordHash = HashPassword(s.Password)
		if s.LegacyUntil.IsZero() {
			s.LegacyUntil = now.Add(LegacyGrace)
		}
	}
	s.Password = ""
	return true
}

// LegacyPasswordAccepted reports whether the old connection password still
// authenticates a phone: a hash exists and its window is open or not binding.
func (s State) LegacyPasswordAccepted(now time.Time) bool {
	return s.LegacyPasswordHash != "" && !s.LegacyRevoked && (!s.RequireDevicePairing || now.Before(s.LegacyUntil))
}
