package httpd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

func callAPIBody(m *LANManager, from, token string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	m.dispatch().ServeHTTP(w, overTLS(reqFrom(from, "Bearer "+token)))
	return w
}

// A phone whose pairing was revoked keeps polling with its old token until it
// learns so. Those requests must not lock out the source, or the fresh pairing
// scan meant to fix it is refused with LOCKED_OUT (seen in the field).
func TestRevokedDeviceTokenDoesNotFeedLockoutOrBlockRepair(t *testing.T) {
	m, tokens := newPairingManager(t, "Code1234")
	tok := tokenFrom(t, exchange(m, "192.168.1.192:5000", "", `{"code":"Code1234","installId":"i-1"}`))
	if _, err := tokens.RevokeInstall("i-1"); err != nil {
		t.Fatal(err)
	}
	for i := range 4 * globalFailureLimit {
		w := callAPIBody(m, "192.168.1.192:5001", tok)
		if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "DEVICE_TOKEN_REVOKED") {
			t.Fatalf("revoked token call %d = %d %s, want 401 DEVICE_TOKEN_REVOKED", i, w.Code, w.Body)
		}
	}
	m.SetPasswordHash(mobilebridge.HashPassword("Code5678"))
	tokenFrom(t, exchange(m, "192.168.1.192:5002", "", `{"code":"Code5678","installId":"i-1"}`))
}

// A never-issued token of the same shape is equally no guess: 256 random bits.
func TestUnknownDeviceTokenShapeIsRevokedNotAGuess(t *testing.T) {
	m, _ := newPairingManager(t, "Code1234")
	unknown := "vot_" + strings.Repeat("A", 43)
	for range 2 * sourceFailureLimit {
		if w := callAPIBody(m, "192.168.1.40:5000", unknown); !strings.Contains(w.Body.String(), "DEVICE_TOKEN_REVOKED") {
			t.Fatalf("unknown device token = %d %s, want DEVICE_TOKEN_REVOKED", w.Code, w.Body)
		}
	}
	tokenFrom(t, exchange(m, "192.168.1.40:5001", "", `{"code":"Code1234","installId":"i-4"}`))
}

// Password guessing stays throttled: a bearer that is not device-token shaped,
// including a near miss on the prefix, still counts toward the lockout.
func TestPasswordGuessesStillLockOut(t *testing.T) {
	m, _ := newPairingManager(t, "Code1234")
	for _, guess := range []string{"guess1", "vot_short", "vot_" + strings.Repeat("A", 42) + "!", "guess4", "guess5"} {
		if w := callAPIBody(m, "192.168.1.50:5000", guess); w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "BAD_PASSWORD") {
			t.Fatalf("guess %q = %d %s, want 401 BAD_PASSWORD", guess, w.Code, w.Body)
		}
	}
	if w := callAPIBody(m, "192.168.1.50:5000", "guess6"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("guess after the limit = %d, want 429", w.Code)
	}
	if w := exchange(m, "192.168.1.50:5001", "", `{"code":"Code1234","installId":"i-5"}`); w.Code != http.StatusTooManyRequests {
		t.Fatalf("pairing code after password guesses = %d, want 429", w.Code)
	}
}

// A different Tallpine Mac that now holds a phone's saved address must answer
// 421, not 401: the phone treats a 401 as its pairing being removed and would
// stop using a healthy token. The host check therefore runs before auth.
func TestForeignDeviceTokenWithMismatchedHostGets421(t *testing.T) {
	m := NewMobileLAN(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }), "h_B", 0, nil)
	tokens, err := mobilebridge.LoadDeviceTokens(t.TempDir() + "/tokens.json")
	if err != nil {
		t.Fatal(err)
	}
	m.SetDeviceTokens(tokens)
	foreign := "vot_" + strings.Repeat("A", 43) // issued by h_A, unknown here
	for _, path := range []string{"/api/v1/sessions", lanPairExchangePath} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.RemoteAddr = "192.168.1.192:5000"
		r.Header.Set("Authorization", "Bearer "+foreign)
		r.Header.Set("X-AO-Expected-Host-ID", "h_A")
		w := httptest.NewRecorder()
		m.dispatch().ServeHTTP(w, overTLS(r))
		if w.Code != http.StatusMisdirectedRequest || !strings.Contains(w.Body.String(), "HOST_ID_MISMATCH") {
			t.Fatalf("%s = %d %s, want 421 HOST_ID_MISMATCH", path, w.Code, w.Body)
		}
	}
}
