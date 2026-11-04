package daemon

import (
	"context"
	"log/slog"
	"sync/atomic"

	"tallpine.eu/tallpine/backend/internal/httpd"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

// deviceRevoker ties the per-device LAN tokens to the paired-device roster:
// removing or unpairing a device in the roster also revokes its token and drops
// its live connections. The LAN listener is built after the router that holds
// the roster, so it is bound late.
type deviceRevoker struct {
	tokens *mobilebridge.DeviceTokens
	lan    atomic.Pointer[httpd.LANManager]
	log    *slog.Logger
}

// loadDeviceTokens loads the token store; a failure disables new pairings (the
// listener answers 503) but never blocks boot.
func loadDeviceTokens(dataDir string, log *slog.Logger) *deviceRevoker {
	tokens, err := mobilebridge.LoadDeviceTokens(mobilebridge.DeviceTokensPath(dataDir))
	if err != nil {
		log.Warn("load mobile device tokens failed; mobile pairing disabled", "err", err)
		return &deviceRevoker{log: log}
	}
	return &deviceRevoker{tokens: tokens, log: log}
}

// bind arms the listener with the token store and remembers it for revocation.
func (d *deviceRevoker) bind(lan *httpd.LANManager) {
	d.lan.Store(lan)
	if d.tokens != nil {
		lan.SetDeviceTokens(d.tokens)
	}
}

// revokeSource names who asked for a revoke in the daemon log.
const (
	revokeDesktopRemove = "desktop_remove"
	revokePhoneUnpair   = "phone_unpair"
)

// revoke deletes every token of installID (the desktop removing a device).
func (d *deviceRevoker) revoke(installID, source string) {
	if d.tokens == nil || installID == "" {
		return
	}
	hashes, err := d.tokens.RevokeInstall(installID)
	if err != nil {
		d.log.Warn("revoke mobile device token", "err", err, "source", source, "installId", installID)
		return
	}
	for _, h := range hashes {
		d.log.Info("mobile device token revoked", "source", source, "installId", installID, "tokenId", mobilebridge.TokenIDPrefix(h))
	}
	d.closeConnections(hashes...)
}

// revokeCredential deletes only the token hash that authenticated a phone's
// unpair, and only when it belongs to installID. A replayed unpair from an
// older pairing therefore never reaches the install's current token.
func (d *deviceRevoker) revokeCredential(installID, hash string) {
	if d.tokens == nil || installID == "" {
		return
	}
	if t, ok := d.tokens.Lookup(hash); !ok || t.InstallID != installID {
		return
	}
	ok, err := d.tokens.RevokeHash(hash)
	if err != nil {
		d.log.Warn("revoke mobile device token", "err", err, "source", revokePhoneUnpair, "installId", installID)
		return
	}
	if ok {
		d.log.Info("mobile device token revoked", "source", revokePhoneUnpair, "installId", installID, "tokenId", mobilebridge.TokenIDPrefix(hash))
		d.closeConnections(hash)
	}
}

func (d *deviceRevoker) closeConnections(hashes ...string) {
	if lan := d.lan.Load(); lan != nil && len(hashes) > 0 {
		lan.CloseTokenConnections(hashes...)
	}
}

type revokingRoster struct {
	controllers.DeviceRoster
	rev *deviceRevoker
}

func (r revokingRoster) Delete(installID string) error {
	r.rev.revoke(installID, revokeDesktopRemove)
	return r.DeviceRoster.Delete(installID)
}

// The push controller routes a phone unpair through UnpairFrom.
var _ controllers.CredentialUnpairer = revokingPush{}

type revokingPush struct {
	controllers.PushRegistry
	roster interface {
		List() []mobilebridge.PushDevice
	}
	rev *deviceRevoker
}

// installFor resolves an install id or an Expo push token to the install id.
func (r revokingPush) installFor(id string) string {
	for _, dev := range r.roster.List() {
		if dev.Token != "" && dev.Token == id {
			return dev.InstallID
		}
	}
	return id
}

// Unpair accepts an install id or an Expo push token (see DeviceRegistry.Unpair).
// It is the unscoped path (loopback callers); a phone's unpair goes through
// UnpairFrom.
func (r revokingPush) Unpair(id string) error {
	r.rev.revoke(r.installFor(id), revokePhoneUnpair)
	return r.PushRegistry.Unpair(id)
}

// UnpairFrom scopes a phone's unpair to the credential that authenticated it:
// a device token revokes only itself, and the legacy password cannot unpair an
// install that already holds a device token. Without a LAN credential (a
// loopback caller) it behaves like Unpair.
func (r revokingPush) UnpairFrom(ctx context.Context, id string) error {
	cred, ok := mobilebridge.CredentialFrom(ctx)
	if !ok {
		return r.Unpair(id)
	}
	installID := r.installFor(id)
	if cred == mobilebridge.LegacyCredential {
		if r.rev.tokens != nil && r.rev.tokens.HasInstall(installID) {
			r.rev.log.Info("mobile unpair ignored: legacy password cannot unpair a token pairing", "source", revokePhoneUnpair, "installId", installID)
			return nil
		}
		return r.PushRegistry.Unpair(id)
	}
	if t, found := r.rev.lookup(cred); !found || t.InstallID != installID {
		r.rev.log.Info("mobile unpair ignored: credential is not this install's token", "source", revokePhoneUnpair, "installId", installID, "tokenId", mobilebridge.TokenIDPrefix(cred))
		return nil
	}
	r.rev.revokeCredential(installID, cred)
	return r.PushRegistry.Unpair(id)
}

func (d *deviceRevoker) lookup(hash string) (mobilebridge.DeviceToken, bool) {
	if d.tokens == nil {
		return mobilebridge.DeviceToken{}, false
	}
	return d.tokens.Lookup(hash)
}
