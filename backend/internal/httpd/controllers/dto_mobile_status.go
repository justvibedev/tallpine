package controllers

import bridgesvc "tallpine.eu/tallpine/backend/internal/mobilebridge/service"

// MobileStatusResponse is the Connect Mobile status; it lives with the bridge
// service that fills it in.
type MobileStatusResponse = bridgesvc.MobileStatusResponse

// KeepAwakeStatus is the keep-awake part of the Connect Mobile status.
type KeepAwakeStatus = bridgesvc.KeepAwakeStatus

// SecurePairingStatus is the secure-pairing part of the Connect Mobile status.
type SecurePairingStatus = bridgesvc.SecurePairingStatus
