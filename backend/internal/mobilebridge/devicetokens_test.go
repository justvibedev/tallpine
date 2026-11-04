package mobilebridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTokens(t *testing.T) (*DeviceTokens, string, *time.Time) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mobile", "device-tokens.json")
	d, err := LoadDeviceTokens(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	d.SetClock(func() time.Time { return now })
	return d, path, &now
}

func TestDeviceTokenIssueVerifyAndOnlyHashOnDisk(t *testing.T) {
	d, path, _ := newTokens(t)
	tok, _, err := d.Issue("install-1", "Pixel")
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) < 40 || !strings.HasPrefix(tok, "vot_") {
		t.Fatalf("token too short or unprefixed: %q", tok)
	}
	got, ok := d.Verify(tok)
	if !ok || got.InstallID != "install-1" {
		t.Fatalf("Verify = %+v, %v", got, ok)
	}
	if _, ok := d.Verify(tok + "x"); ok {
		t.Fatal("tampered token verified")
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), tok) || strings.Contains(string(raw), strings.TrimPrefix(tok, "vot_")) {
		t.Fatal("plaintext token on disk")
	}
	if !strings.Contains(string(raw), HashPassword(tok)) {
		t.Fatal("hash missing on disk")
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
	reloaded, err := LoadDeviceTokens(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Verify(tok); !ok {
		t.Fatal("token lost across reload")
	}
}

func TestDeviceTokenExpiresAndSlides(t *testing.T) {
	d, _, now := newTokens(t)
	tok, _, _ := d.Issue("i", "")
	*now = now.Add(DeviceTokenTTL - time.Hour)
	if _, ok := d.Verify(tok); !ok {
		t.Fatal("token rejected before expiry")
	}
	// The use above slid the expiry: still valid beyond the original TTL.
	*now = now.Add(2 * time.Hour)
	if _, ok := d.Verify(tok); !ok {
		t.Fatal("sliding expiry did not extend the token")
	}
	*now = now.Add(DeviceTokenTTL + time.Hour)
	if _, ok := d.Verify(tok); ok {
		t.Fatal("expired token verified")
	}
	if d.Count() != 0 {
		t.Fatal("expired token not deleted")
	}
}

func TestDeviceTokenRevokeAndReplace(t *testing.T) {
	d, _, _ := newTokens(t)
	first, _, _ := d.Issue("same", "")
	second, replaced, _ := d.Issue("same", "")
	if len(replaced) != 1 || replaced[0] != HashPassword(first) {
		t.Fatalf("replaced = %v", replaced)
	}
	if _, ok := d.Verify(first); ok {
		t.Fatal("re-pair left the old token valid")
	}
	hashes, err := d.RevokeInstall("same")
	if err != nil || len(hashes) != 1 {
		t.Fatalf("RevokeInstall = %v, %v", hashes, err)
	}
	if _, ok := d.Verify(second); ok {
		t.Fatal("revoked token verified")
	}
}

func TestDeviceTokenStoreIsCapped(t *testing.T) {
	d, _, now := newTokens(t)
	for i := range maxDeviceTokens + 5 {
		*now = now.Add(time.Second)
		if _, _, err := d.Issue(strings.Repeat("a", 1)+string(rune('A'+i%26))+strings.Repeat("b", i), ""); err != nil {
			t.Fatal(err)
		}
	}
	if d.Count() != maxDeviceTokens {
		t.Fatalf("count = %d, want %d", d.Count(), maxDeviceTokens)
	}
}

func TestLooksLikeDeviceTokenMatchesIssuedShapeOnly(t *testing.T) {
	d, _, _ := newTokens(t)
	tok, _, err := d.Issue("install-1", "Pixel")
	if err != nil {
		t.Fatal(err)
	}
	if !LooksLikeDeviceToken(tok) {
		t.Fatalf("issued token %q not recognised", tok)
	}
	for _, s := range []string{"", "Code1234", "vot_", "vot_short", tok[:len(tok)-1], tok + "A", "xot_" + tok[4:], tok[:10] + "!" + tok[11:]} {
		if LooksLikeDeviceToken(s) {
			t.Fatalf("%q recognised as a device token", s)
		}
	}
}
