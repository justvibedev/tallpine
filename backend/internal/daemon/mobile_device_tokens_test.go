package daemon

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

func revokerWithToken(t *testing.T, installID string) (*deviceRevoker, string, *mobilebridge.DeviceRegistry) {
	t.Helper()
	dir := t.TempDir()
	tokens, err := mobilebridge.LoadDeviceTokens(filepath.Join(dir, "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := tokens.Issue(installID, "")
	if err != nil {
		t.Fatal(err)
	}
	reg, err := mobilebridge.LoadRegistry(filepath.Join(dir, "devices.json"))
	if err != nil {
		t.Fatal(err)
	}
	return &deviceRevoker{tokens: tokens, log: slog.Default()}, tok, reg
}

func TestRemovingADeviceRevokesItsToken(t *testing.T) {
	rev, tok, reg := revokerWithToken(t, "install-1")
	if err := (revokingRoster{DeviceRoster: reg, rev: rev}).Delete("install-1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := rev.tokens.Verify(tok); ok {
		t.Fatal("token still valid after the device was removed from the roster")
	}
}

func TestUnpairByInstallIDOrPushTokenRevokesItsToken(t *testing.T) {
	for name, id := range map[string]string{"install id": "install-1", "push token": "ExponentPushToken[abc]"} {
		t.Run(name, func(t *testing.T) {
			rev, tok, reg := revokerWithToken(t, "install-1")
			if err := reg.Upsert(mobilebridge.PushDevice{InstallID: "install-1", Token: "ExponentPushToken[abc]"}); err != nil {
				t.Fatal(err)
			}
			if err := (revokingPush{PushRegistry: reg, roster: reg, rev: rev}).Unpair(id); err != nil {
				t.Fatal(err)
			}
			if _, ok := rev.tokens.Verify(tok); ok {
				t.Fatal("token still valid after unpair")
			}
		})
	}
}

func pushWithDevice(t *testing.T, rev *deviceRevoker, reg *mobilebridge.DeviceRegistry, installID string) revokingPush {
	t.Helper()
	if err := reg.Upsert(mobilebridge.PushDevice{InstallID: installID, Token: "ExponentPushToken[abc]"}); err != nil {
		t.Fatal(err)
	}
	return revokingPush{PushRegistry: reg, roster: reg, rev: rev}
}

func TestLegacyPasswordUnpairCannotRevokeATokenPairing(t *testing.T) {
	rev, tok, reg := revokerWithToken(t, "install-1")
	push := pushWithDevice(t, rev, reg, "install-1")
	ctx := mobilebridge.WithCredential(context.Background(), mobilebridge.LegacyCredential)
	if err := push.UnpairFrom(ctx, "install-1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := rev.tokens.Verify(tok); !ok {
		t.Fatal("a legacy-password unpair revoked the install's device token")
	}
	if len(reg.List()) != 1 {
		t.Fatal("a legacy-password unpair dropped the token pairing from the roster")
	}
}

func TestCrossInstallUnpairLeavesTheTargetTokenValid(t *testing.T) {
	// An unpair authenticated by another install's live token must not reach
	// install-1's token.
	rev, tok, reg := revokerWithToken(t, "install-1")
	other, _, err := rev.tokens.Issue("install-2", "")
	if err != nil {
		t.Fatal(err)
	}
	push := pushWithDevice(t, rev, reg, "install-1")
	ctx := mobilebridge.WithCredential(context.Background(), mobilebridge.HashPassword(other))
	if err := push.UnpairFrom(ctx, "install-1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := rev.tokens.Verify(tok); !ok {
		t.Fatal("an unpair authenticated by another credential revoked the current token")
	}
}

func TestPhoneUnpairRevokesOnlyItsOwnToken(t *testing.T) {
	rev, tok, reg := revokerWithToken(t, "install-1")
	other, _, err := rev.tokens.Issue("install-2", "")
	if err != nil {
		t.Fatal(err)
	}
	push := pushWithDevice(t, rev, reg, "install-1")
	ctx := mobilebridge.WithCredential(context.Background(), mobilebridge.HashPassword(tok))
	if err := push.UnpairFrom(ctx, "ExponentPushToken[abc]"); err != nil {
		t.Fatal(err)
	}
	if _, ok := rev.tokens.Verify(tok); ok {
		t.Fatal("the phone's own token is still valid after it unpaired")
	}
	if _, ok := rev.tokens.Verify(other); !ok {
		t.Fatal("unpair revoked another install's token")
	}
}
