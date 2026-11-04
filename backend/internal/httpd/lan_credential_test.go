package httpd

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

// The phone unpair route scopes revocation by the credential the LAN listener
// recorded, so the listener must record exactly the one that authenticated.
func TestLANAuthRecordsTheAuthenticatingCredential(t *testing.T) {
	tokens, err := mobilebridge.LoadDeviceTokens(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got string
	m := NewLANManager(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = mobilebridge.CredentialFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	}), &authState{}, 0, nil)
	m.SetDeviceTokens(tokens)
	m.SetLegacyPassword(mobilebridge.HashPassword("oldpass1"), time.Now().Add(time.Hour))
	tok, _, err := tokens.Issue("i-1", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ bearer, want string }{
		{tok, mobilebridge.HashPassword(tok)},
		{"oldpass1", mobilebridge.LegacyCredential},
	} {
		got = ""
		w := httptest.NewRecorder()
		m.dispatch().ServeHTTP(w, reqFrom("192.168.1.20:5000", "Bearer "+tc.bearer))
		if w.Code != http.StatusOK || got != tc.want {
			t.Fatalf("status %d credential %q, want 200 %q", w.Code, got, tc.want)
		}
	}
}
