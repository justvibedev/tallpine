package mobilebridge

import "time"

// PairingCodeTTL is how long a pairing code stays redeemable. The LAN listener
// enforces it at redeem time; the desktop re-mints on the same schedule.
const PairingCodeTTL = 10 * time.Minute
